package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

const fileListMaxPages = 5

func newFileCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Attachments: list, download, replace, remove",
		Long: `Attachments of messages. To send files, use mm-cli send -f <file>. File ids
are 26 characters, from the "files" of a message in --json output or from
file list. replace and remove work only on attachments of your own messages.`,
	}
	cmd.AddCommand(newFileListCmd(app), newFileDownloadCmd(app), newFileReplaceCmd(app), newFileRemoveCmd(app))
	return cmd
}

type fileEntry struct {
	render.File
	PostID   string      `json:"post_id"`
	CreateAt string      `json:"create_at"`
	User     render.User `json:"user"`
}

func newFileListCmd(app *App) *cobra.Command {
	var n int
	cmd := &cobra.Command{
		Use:   "list <channel>",
		Short: "List recent attachments of a channel, newest first",
		Long: fmt.Sprintf(`List recent attachments of a channel, newest first. Looks through at most the
last %d messages; -n limits how many attachments are listed. Marks nothing
read.

With --json it prints an array of {"id": ..., "name": ..., "size": N,
"mime": ..., "post_id": ..., "create_at": "<RFC 3339>", "user": {"id": ...,
"username": ...}}; [] when there are none. Download with
mm-cli file download <id> --file.

%s`, fileListMaxPages*maxPerPage, channelHelp),
		Example: `  mm-cli file list town-square
  mm-cli file list @me -n 50 --json`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := checkLimit(n); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			var found []*model.Post
			files := 0
			err = s.withChannel(ctx, args[0], func(id string) error {
				found, files = nil, 0
				for page := 0; page < fileListMaxPages && files < n; page++ {
					pl, resp, err := s.Client.GetPostsForChannel(ctx, id, page, maxPerPage, "", false, false)
					if err != nil {
						return clierr.API(resp, err)
					}
					posts := render.Sorted(pl)
					slices.Reverse(posts)
					for _, p := range posts {
						if len(p.FileIds) > 0 && files < n {
							found = append(found, p)
							files += len(p.FileIds)
						}
					}
					if len(pl.Order) < maxPerPage {
						break
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			ids := make([]string, len(found))
			for i, p := range found {
				ids[i] = p.UserId
			}
			names := s.usernames(ctx, ids)
			out := []fileEntry{}
			for _, p := range found {
				rp := render.FromModel(p, names)
				for _, f := range rp.Files {
					if len(out) < n {
						out = append(out, fileEntry{File: f, PostID: p.Id, CreateAt: rp.CreateAt, User: rp.User})
					}
				}
			}
			if app.json {
				return app.printJSON(out)
			}
			for _, f := range out {
				ts := f.CreateAt
				if t, err := time.Parse(time.RFC3339, f.CreateAt); err == nil {
					ts = t.In(app.Location).Format("2006-01-02 15:04")
				}
				who := f.User.Username
				if who == "" {
					who = f.User.ID
				}
				fmt.Fprintf(app.Out, "%s  @%s  %s (%s)  file %s  post %s\n", ts, who, f.Name, render.HumanSize(f.Size), f.ID, f.PostID)
			}
			return nil
		},
	}
	addLimitFlag(cmd, &n, "files")
	return cmd
}

// idKind says how file download treats an id.
type idKind int

const (
	idAny  idKind = iota // a message id first, then a file id
	idPost               // a message id only
	idFile               // a file id only
)

// filesOf returns the attachments of a post id, or the file itself when the id
// is a file id. kind limits the id to one type, which saves the request for
// the other one.
func (s *Session) filesOf(ctx context.Context, id string, kind idKind) ([]*model.FileInfo, error) {
	if kind != idFile {
		p, resp, err := s.Client.GetPost(ctx, id, "")
		if err == nil {
			if p.Metadata != nil && len(p.Metadata.Files) > 0 {
				return p.Metadata.Files, nil
			}
			if len(p.FileIds) == 0 {
				return nil, clierr.New(clierr.CodeNotFound, "message %s has no attachments", id)
			}
			infos, resp, err := s.Client.GetFileInfosForPost(ctx, id, "")
			if err != nil {
				return nil, clierr.API(resp, err)
			}
			return infos, nil
		}
		e := clierr.API(resp, err)
		if clierr.ExitCode(e) != clierr.CodeNotFound {
			return nil, e
		}
		if kind == idPost {
			return nil, clierr.Wrap(clierr.CodeNotFound, err, "no message with id %s", id)
		}
	}
	info, resp, err := s.Client.GetFileInfo(ctx, id)
	if err != nil {
		if e := clierr.API(resp, err); clierr.ExitCode(e) == clierr.CodeNotFound {
			if kind == idFile {
				return nil, clierr.Wrap(clierr.CodeNotFound, err, "no file with id %s", id)
			}
			return nil, clierr.Wrap(clierr.CodeNotFound, err, "no message or file with id %s", id)
		}
		return nil, clierr.API(resp, err)
	}
	return []*model.FileInfo{info}, nil
}

// windowsReserved are the device names Windows resolves in any directory and
// with any extension.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"COM\u00b9": true, "COM\u00b2": true, "COM\u00b3": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"LPT\u00b9": true, "LPT\u00b2": true, "LPT\u00b3": true,
}

// safeName turns a server-supplied file name into a name that stays inside the
// output directory on the current system.
func safeName(name, fallback string) string {
	return sanitizeName(name, fallback, runtime.GOOS == "windows")
}

// sanitizeName keeps only the last element of name. With windows set it also
// replaces the characters Windows does not allow (":" would open an alternate
// data stream), drops trailing dots and spaces, and prefixes device names such
// as CON or NUL with "_".
func sanitizeName(name, fallback string, windows bool) string {
	n := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if windows {
		n = strings.Map(func(r rune) rune {
			if r < 0x20 || strings.ContainsRune(`<>:"|?*`, r) {
				return '_'
			}
			return r
		}, n)
		n = strings.TrimRight(n, ". ")
		stem, _, _ := strings.Cut(n, ".")
		if windowsReserved[strings.ToUpper(strings.TrimRight(stem, " "))] {
			n = "_" + n
		}
	}
	if n == "." || n == ".." || n == "/" || n == "" || strings.ContainsRune(n, 0) {
		return fallback
	}
	return n
}

// uniqueName returns name, or name with " (2)", " (3)" and so on before the
// extension when an earlier file of the same run already took it. Names are
// compared ignoring case, as on the default file systems of macOS and Windows.
func uniqueName(name string, taken map[string]bool) string {
	ext := path.Ext(name)
	if ext == name {
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	n := name
	for i := 2; taken[strings.ToLower(n)]; i++ {
		n = fmt.Sprintf("%s (%d)%s", stem, i, ext)
	}
	taken[strings.ToLower(n)] = true
	return n
}

// bodyReader remembers a read error so that it can be told from a write error.
type bodyReader struct {
	r   io.Reader
	err error
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.err = err
	}
	return n, err
}

// downloadFile streams a file into a temporary file next to dest and then moves
// it into place: an interrupted download leaves nothing behind, and an existing
// symbolic link at dest is replaced rather than written through.
func (s *Session) downloadFile(ctx context.Context, id, dest string, force bool) (n int64, err error) {
	r, err := s.Client.DoAPIGet(ctx, "/files/"+url.PathEscape(id), "")
	if err != nil {
		return 0, clierr.API(model.BuildResponse(r), err)
	}
	defer func() { _ = r.Body.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".mm-cli-*.part")
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	body := &bodyReader{r: r.Body}
	if n, err = io.Copy(tmp, body); err != nil {
		if body.err != nil {
			return 0, clierr.API(model.BuildResponse(r), err)
		}
		return 0, err
	}
	if err = tmp.Close(); err != nil {
		return 0, err
	}
	if force {
		err = os.Rename(tmp.Name(), dest)
	} else {
		err = placeNew(tmp.Name(), dest)
	}
	return n, err
}

// placeNew moves tmp to dest unless dest exists. A hard link fails when dest
// exists, so a file created after the earlier check is not overwritten; file
// systems without hard links fall back to a check and a rename.
func placeNew(tmp, dest string) error {
	err := os.Link(tmp, dest)
	if err == nil {
		_ = os.Remove(tmp)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return clierr.Usage("%s already exists; pass --force to overwrite", dest)
	}
	if _, err := os.Lstat(dest); err == nil {
		return clierr.Usage("%s already exists; pass --force to overwrite", dest)
	}
	return os.Rename(tmp, dest)
}

// plannedFile is one attachment of a download run and the path it is saved to.
type plannedFile struct {
	id, path string
}

func newFileDownloadCmd(app *App) *cobra.Command {
	var dir string
	var force, asFile, asPost bool
	cmd := &cobra.Command{
		Use:   "download <post-or-file-id>...",
		Short: "Download attachments of messages, or files by id",
		Long: `Download attachments. An id is tried as a message first, then as a file;
--post or --file takes every id as that type and saves the other request.
Attachments with the same name are saved as "name (2).ext", "name (3).ext" in
the order of the arguments. Existing files are kept unless --force is given; all
conflicts are reported before anything is downloaded (exit code 2).

With --json it prints an array of {"file_id": ..., "path": ..., "size": N}.
When a download fails part-way, the files already saved are listed first and
then the error. Exit code 2 for a missing output directory or existing files,
4 without access, 5 when an id is neither a message nor a file, or the
message has no attachments.`,
		Example: `  mm-cli file download <post-id> -o ~/Downloads
  mm-cli file download <file-id> --file -o ~/Downloads --json
  mm-cli file download <post-id> <post-id> --force`,
		Args: argsRange(1, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if asFile && asPost {
				return clierr.Usage("--file and --post cannot be used together")
			}
			kind := idAny
			switch {
			case asFile:
				kind = idFile
			case asPost:
				kind = idPost
			}
			for _, id := range args {
				if err := validID(id, "id"); err != nil {
					return err
				}
			}
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				return clierr.Usage("output directory %q does not exist", dir)
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			// Plan every path first, so that conflicts are reported before
			// anything is downloaded. A file given twice is saved once; equal
			// names get " (2)", " (3)" in the order of the arguments.
			var plan []plannedFile
			seen, taken := map[string]bool{}, map[string]bool{}
			for _, id := range args {
				infos, err := s.filesOf(ctx, id, kind)
				if err != nil {
					return err
				}
				for _, info := range infos {
					if seen[info.Id] {
						continue
					}
					seen[info.Id] = true
					name := uniqueName(safeName(info.Name, info.Id), taken)
					plan = append(plan, plannedFile{id: info.Id, path: filepath.Join(dir, name)})
				}
			}
			var existing []string
			for _, f := range plan {
				st, err := os.Lstat(f.path)
				switch {
				case err == nil && st.IsDir():
					return clierr.Usage("%s is a directory", f.path)
				case err == nil && !force:
					existing = append(existing, f.path)
				case err != nil && !errors.Is(err, fs.ErrNotExist):
					return err
				}
			}
			switch len(existing) {
			case 0:
			case 1:
				return clierr.Usage("%s already exists; pass --force to overwrite", existing[0])
			default:
				return clierr.Usage("these files already exist: %s; pass --force to overwrite", strings.Join(existing, ", "))
			}
			// With --json the saved files are printed together at the end,
			// also when a later download fails.
			done := []downloadedFile{}
			for _, f := range plan {
				n, err := s.downloadFile(ctx, f.id, f.path, force)
				if err != nil {
					if app.json && len(done) > 0 {
						_ = app.printJSON(done)
					}
					return err
				}
				done = append(done, downloadedFile{FileID: f.id, Path: f.path, Size: n})
				if !app.json {
					fmt.Fprintf(app.Out, "Downloaded: %s (%s)\n", f.path, render.HumanSize(n))
				}
			}
			if app.json {
				return app.printJSON(done)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&dir, "output", "o", ".", "directory to save into")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	cmd.Flags().BoolVar(&asFile, "file", false, "the ids are file ids")
	cmd.Flags().BoolVar(&asPost, "post", false, "the ids are message ids")
	return cmd
}

func newFileReplaceCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "replace <post-id> <file-id> <new-file>",
		Short: "Replace one attachment of your message",
		Long: `Replace one attachment of your message with a local file: the new file is
uploaded, then the message is edited to carry it in place of the old one. The
message text stays.

With --json it prints {"action": "replaced", "post_id": ..., "old_file_id": ...,
"file_id": "<new file id>"}. Exit code 2 for invalid ids or an unreadable
file, 4 for a message of someone else, 5 when the message has no such
attachment.`,
		Example: `  mm-cli file replace <post-id> <file-id> report-v2.pdf
  mm-cli file replace <post-id> <file-id> report-v2.pdf --json`,
		Args: argsRange(3, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := validID(args[0], "post id"); err != nil {
				return err
			}
			if err := validID(args[1], "file id"); err != nil {
				return err
			}
			data, err := os.ReadFile(args[2])
			if err != nil {
				return clierr.Usage("%v", err)
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			p, resp, err := s.Client.GetPost(ctx, args[0], "")
			if err != nil {
				return clierr.API(resp, err)
			}
			i := slices.Index(p.FileIds, args[1])
			if i < 0 {
				return clierr.New(clierr.CodeNotFound, "message %s has no attachment %s", args[0], args[1])
			}
			newID, err := s.uploadOne(ctx, data, p.ChannelId, args[2], "")
			if err != nil {
				return err
			}
			ids := slices.Clone(p.FileIds)
			ids[i] = newID
			if _, resp, err := s.Client.PatchPost(ctx, p.Id, &model.PostPatch{FileIds: &ids}); err != nil {
				return clierr.API(resp, err)
			}
			return app.printDone(actionResult{Action: "replaced", PostID: p.Id, OldFileID: args[1], FileID: newID}, "Replaced: %s -> %s", args[1], newID)
		},
	}
}

func newFileRemoveCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <post-id> <file-id>",
		Short: "Remove one attachment from your message",
		Long: `Remove one attachment from your message; the message and its other
attachments stay. The only attachment of a message without text cannot be
removed (exit code 2): delete the message with mm-cli post delete instead.

Asks for confirmation on a terminal; without a terminal it needs --yes,
otherwise exit code 2. With --json it prints {"action": "removed",
"post_id": ..., "file_id": ...}. Exit code 4 for a message of someone else, 5
when the message has no such attachment.`,
		Example: `  mm-cli file remove <post-id> <file-id>
  mm-cli file remove <post-id> <file-id> --yes --json`,
		Args: argsRange(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := validID(args[0], "post id"); err != nil {
				return err
			}
			if err := validID(args[1], "file id"); err != nil {
				return err
			}
			if err := app.confirm(cmd.Context(), fmt.Sprintf("Remove attachment %s?", args[1]), yes); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			p, resp, err := s.Client.GetPost(ctx, args[0], "")
			if err != nil {
				return clierr.API(resp, err)
			}
			i := slices.Index(p.FileIds, args[1])
			if i < 0 {
				return clierr.New(clierr.CodeNotFound, "message %s has no attachment %s", args[0], args[1])
			}
			ids := slices.Delete(slices.Clone(p.FileIds), i, i+1)
			if len(ids) == 0 && strings.TrimSpace(p.Message) == "" {
				return clierr.Usage("this is the only attachment of a message without text; delete the message instead")
			}
			if _, resp, err := s.Client.PatchPost(ctx, p.Id, &model.PostPatch{FileIds: &ids}); err != nil {
				return clierr.API(resp, err)
			}
			return app.printDone(actionResult{Action: "removed", PostID: p.Id, FileID: args[1]}, "Removed: %s", args[1])
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

// downloadedFile is one entry of the --json output of file download.
type downloadedFile struct {
	FileID string `json:"file_id"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
}
