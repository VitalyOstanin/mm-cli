package cli

import (
	"fmt"
	"net/http"
	"os"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

func newBookmarkCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bookmark",
		Short: "Channel bookmarks (links and files under the channel header)",
		Long: `Channel bookmarks: links and files shown under the channel header, visible to
every member. They need a server with bookmarks turned on; otherwise every
bookmark command exits with code 5 ("not found, or channel bookmarks are not
supported by this server").

A bookmark in --json output is {"id": ..., "channel_id": ...,
"display_name": ..., "type": "link"|"file", "link_url": ..., "file_id": ...,
"file_name": ..., "emoji": ..., "sort_order": N}; empty link_url, file and emoji
fields are left out.`,
	}
	cmd.AddCommand(newBookmarkAddCmd(app), newBookmarkGetCmd(app), newBookmarkListCmd(app), newBookmarkEditCmd(app), newBookmarkDeleteCmd(app))
	return cmd
}

type bookmarkInfo struct {
	ID          string `json:"id"`
	ChannelID   string `json:"channel_id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	LinkURL     string `json:"link_url,omitempty"`
	FileID      string `json:"file_id,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	Emoji       string `json:"emoji,omitempty"`
	SortOrder   int64  `json:"sort_order"`
}

func toBookmark(b *model.ChannelBookmarkWithFileInfo) bookmarkInfo {
	out := bookmarkInfo{}
	if b == nil || b.ChannelBookmark == nil {
		return out
	}
	out = bookmarkInfo{ID: b.Id, ChannelID: b.ChannelId, DisplayName: b.DisplayName, Type: string(b.Type),
		LinkURL: b.LinkUrl, FileID: b.FileId, Emoji: b.Emoji, SortOrder: b.SortOrder}
	if b.FileInfo != nil {
		out.FileName = b.FileInfo.Name
	}
	return out
}

// bookmarkErr turns the answers of servers without bookmarks into code 5.
func bookmarkErr(resp *model.Response, err error) error {
	if resp != nil && (resp.StatusCode == http.StatusNotImplemented || resp.StatusCode == http.StatusNotFound) {
		return clierr.Wrap(clierr.CodeNotFound, err, "not found, or channel bookmarks are not supported by this server")
	}
	return clierr.API(resp, err)
}

func (a *App) printBookmarks(list []bookmarkInfo) error {
	if a.json {
		return a.printJSON(list)
	}
	for _, b := range list {
		target := b.LinkURL
		if b.Type == string(model.ChannelBookmarkFile) {
			target = "file " + b.FileName + " " + b.FileID
		}
		emoji := ""
		if b.Emoji != "" {
			emoji = "  :" + b.Emoji + ":"
		}
		fmt.Fprintf(a.Out, "%s  %s  %s%s\n", b.ID, b.DisplayName, target, emoji)
	}
	return nil
}

func newBookmarkAddCmd(app *App) *cobra.Command {
	var link, file, name, emoji string
	cmd := &cobra.Command{
		Use:         "add <channel> (--link-url <url> | -f <file>) --display-name <title> [--emoji <name>]",
		Annotations: effect(effectServer),
		Short:       "Add a link or file bookmark to a channel",
		Long: channelHelp + `

Adds a bookmark for every member of the channel: a link (--link-url) or an
uploaded local file (-f), with a title (--display-name) and an optional emoji.

With --json it prints the new bookmark (see mm-cli bookmark --help). Exit code
2 without exactly one of --link-url and -f, without --display-name, or for an
unreadable file; 4 without the permission; 5 when bookmarks are not
supported.`,
		Example: `  mm-cli bookmark add town-square --link-url https://chat.example.com/docs --display-name Docs
  mm-cli bookmark add town-square -f handbook.pdf --display-name Handbook --emoji books --json`,
		Args: argsChannel(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if (link == "") == (file == "") {
				return clierr.Usage("pass exactly one of --link-url and -f")
			}
			if name == "" {
				return clierr.Usage("--display-name is required")
			}
			var data []byte
			if file != "" {
				var err error
				if data, err = os.ReadFile(file); err != nil {
					return clierr.Usage("%v", err)
				}
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				b := &model.ChannelBookmark{ChannelId: id, DisplayName: name, Emoji: emoji, Type: model.ChannelBookmarkLink, LinkUrl: link}
				if file != "" {
					fileID, err := s.uploadOne(ctx, data, id, file, "")
					if err != nil {
						return err
					}
					b.Type, b.FileId, b.LinkUrl = model.ChannelBookmarkFile, fileID, ""
				}
				created, resp, err := s.Client.CreateChannelBookmark(ctx, b)
				if err != nil {
					return bookmarkErr(resp, err)
				}
				if app.json {
					return app.printJSON(toBookmark(created))
				}
				fmt.Fprintf(app.Out, "Added: %s\n", created.Id)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&link, "link-url", "", "link target")
	cmd.Flags().StringVarP(&file, "file", "f", "", "file to upload as the bookmark")
	cmd.Flags().StringVar(&name, "display-name", "", "bookmark title")
	cmd.Flags().StringVar(&emoji, "emoji", "", "emoji name, without colons")
	return cmd
}

func (s *Session) listBookmarks(cmd *cobra.Command, channel string) ([]bookmarkInfo, error) {
	ctx := cmd.Context()
	var out []bookmarkInfo
	err := s.withChannel(ctx, channel, func(id string) error {
		list, resp, err := s.Client.ListChannelBookmarksForChannel(ctx, id, 0)
		if err != nil {
			return bookmarkErr(resp, err)
		}
		out = make([]bookmarkInfo, 0, len(list))
		for _, b := range list {
			if b.ChannelBookmark != nil && b.DeleteAt == 0 {
				out = append(out, toBookmark(b))
			}
		}
		return nil
	})
	return out, err
}

func newBookmarkListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "list <channel>",
		Annotations: effect(effectRead),
		Short:       "List the bookmarks of a channel",
		Long: channelHelp + `

With --json it prints an array of bookmarks (see mm-cli bookmark --help); []
when the channel has none. Exit code 5 when bookmarks are not supported.`,
		Example: `  mm-cli bookmark list town-square
  mm-cli bookmark list myteam:town-square --json`,
		Args: argsChannel(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			list, err := s.listBookmarks(cmd, args[0])
			if err != nil {
				return err
			}
			return app.printBookmarks(list)
		},
	}
}

func newBookmarkGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:         "get <channel> <bookmark-id>",
		Annotations: effect(effectRead),
		Short:       "Show one bookmark",
		Long: channelHelp + `

Shows one bookmark of the channel by its id (from bookmark list). With --json
it prints the bookmark (see mm-cli bookmark --help). Exit code 5 when the
channel has no such bookmark or bookmarks are not supported.`,
		Example: `  mm-cli bookmark get town-square <bookmark-id>
  mm-cli bookmark get town-square <bookmark-id> --json`,
		Args: argsChannel(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[1], "bookmark id"); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			list, err := s.listBookmarks(cmd, args[0])
			if err != nil {
				return err
			}
			for _, b := range list {
				if b.ID == args[1] {
					if app.json {
						return app.printJSON(b)
					}
					return app.printBookmarks([]bookmarkInfo{b})
				}
			}
			return clierr.New(clierr.CodeNotFound, "bookmark %s not found in the channel", args[1])
		},
	}
}

func newBookmarkEditCmd(app *App) *cobra.Command {
	var name, link, emoji string
	cmd := &cobra.Command{
		Use:         "edit <channel> <bookmark-id> [--display-name <title>] [--link-url <url>] [--emoji <name>]",
		Annotations: effect(effectServer),
		Short:       "Change a bookmark",
		Long: channelHelp + `

Changes only the fields passed: title, link target or emoji. A file bookmark
keeps its file. With --json it prints the changed bookmark (see
mm-cli bookmark --help). Exit code 2 when no field is passed, 4 without the
permission, 5 for an unknown bookmark or when bookmarks are not supported.`,
		Example: `  mm-cli bookmark edit town-square <bookmark-id> --display-name "Team docs"
  mm-cli bookmark edit town-square <bookmark-id> --emoji memo --json`,
		Args: argsChannel(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := validID(args[1], "bookmark id"); err != nil {
				return err
			}
			patch := &model.ChannelBookmarkPatch{}
			if cmd.Flags().Changed("display-name") {
				patch.DisplayName = &name
			}
			if cmd.Flags().Changed("link-url") {
				patch.LinkUrl = &link
			}
			if cmd.Flags().Changed("emoji") {
				patch.Emoji = &emoji
			}
			if patch.DisplayName == nil && patch.LinkUrl == nil && patch.Emoji == nil {
				return clierr.Usage("nothing to change: pass --display-name, --link-url or --emoji")
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				res, resp, err := s.Client.UpdateChannelBookmark(ctx, id, args[1], patch)
				if err != nil {
					return bookmarkErr(resp, err)
				}
				if app.json {
					return app.printJSON(toBookmark(res.Updated))
				}
				fmt.Fprintf(app.Out, "Edited: %s\n", args[1])
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "display-name", "", "new title")
	cmd.Flags().StringVar(&link, "link-url", "", "new link target")
	cmd.Flags().StringVar(&emoji, "emoji", "", "new emoji name")
	return cmd
}

func newBookmarkDeleteCmd(app *App) *cobra.Command {
	var confirmed bool
	cmd := &cobra.Command{
		Use:         "delete <channel> <bookmark-id>",
		Annotations: effect(effectServer),
		Short:       "Delete a bookmark",
		Long: channelHelp + `

Deletes the bookmark for every member of the channel. Asks for confirmation
on a terminal; without a terminal it needs --confirm, otherwise exit code 2.

With --json it prints {"action": "deleted", "id": "<bookmark id>",
"channel_id": ...}. Exit code 4 without the permission, 5 for an unknown
bookmark or when bookmarks are not supported.`,
		Example: `  mm-cli bookmark delete town-square <bookmark-id>
  mm-cli bookmark delete town-square <bookmark-id> --confirm --json`,
		Args: argsChannel(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := validID(args[1], "bookmark id"); err != nil {
				return err
			}
			if err := app.confirm(cmd.Context(), fmt.Sprintf("Delete bookmark %s?", args[1]), confirmed); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return s.withChannel(ctx, args[0], func(id string) error {
				if _, resp, err := s.Client.DeleteChannelBookmark(ctx, id, args[1]); err != nil {
					return bookmarkErr(resp, err)
				}
				return app.printDone(actionResult{Action: "deleted", ID: args[1], ChannelID: id}, "Deleted: %s", args[1])
			})
		},
	}
	addConfirmFlag(cmd, &confirmed)
	return cmd
}
