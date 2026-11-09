package cli

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// addConfirmFlag declares --confirm of the commands that ask before they
// delete or send.
func addConfirmFlag(cmd *cobra.Command, confirmed *bool) {
	cmd.Flags().BoolVar(confirmed, "confirm", false, "do not ask for confirmation")
}

// addNumberFlag declares -n/--number, 20 by default; what names the counted
// items in the help text.
func addNumberFlag(cmd *cobra.Command, n *int, what string) {
	cmd.Flags().IntVarP(n, "number", "n", 20, fmt.Sprintf("number of %s (1-%d)", what, maxPerPage))
}

// checkNumber rejects an -n outside 1..maxPerPage.
func checkNumber(n int) error {
	if n < 1 || n > maxPerPage {
		return clierr.Usage("-n must be between 1 and %d", maxPerPage)
	}
	return nil
}

// addTruncateFlag declares --truncate for commands that print messages.
func addTruncateFlag(cmd *cobra.Command, truncate *int) {
	cmd.Flags().IntVar(truncate, "truncate", 0, "shorten messages to this many characters")
}

// lastN keeps the newest n of posts sorted oldest first.
func lastN(posts []*model.Post, n int) []*model.Post {
	if len(posts) > n {
		return posts[len(posts)-n:]
	}
	return posts
}

// ownPost renders a post written by the session's user, which needs no user
// lookup.
func (s *Session) ownPost(p *model.Post) render.Post {
	return render.FromModel(p, map[string]string{s.Server.UserID: s.Server.Username})
}

// uploadOne uploads the file at path (already read into data) to a channel
// and returns its file id. With a label, every error starts with
// "upload <label>:"; without one, API errors pass through as they are and only
// an unexpected answer is reported as "upload: ...".
func (s *Session) uploadOne(ctx context.Context, data []byte, channelID, path, label string) (string, error) {
	prefix := "upload"
	if label != "" {
		prefix += " " + label
	}
	up, resp, err := s.Client.UploadFile(ctx, data, channelID, filepath.Base(path))
	if err != nil {
		if label != "" {
			return "", fmt.Errorf("%s: %w", prefix, clierr.API(resp, err))
		}
		return "", clierr.API(resp, err)
	}
	if len(up.FileInfos) != 1 {
		return "", fmt.Errorf("%s: the server returned %d file records", prefix, len(up.FileInfos))
	}
	return up.FileInfos[0].Id, nil
}

// toggleAction is one side of a command pair such as pin add and pin remove.
type toggleAction struct {
	short   string // cobra Short
	long    string // cobra Long
	example string // cobra Example
	verb    string // start of the text result, e.g. "Pinned"
	done    string // action field of the JSON result, e.g. "pinned"
	call    func(ctx context.Context, s *Session, id string) (*model.Response, error)
}

// run calls the API for id and reports the result.
func (t toggleAction) run(ctx context.Context, app *App, s *Session, id string) error {
	if resp, err := t.call(ctx, s, id); err != nil {
		return clierr.API(resp, err)
	}
	return app.printDone(actionResult{Action: t.done, ID: id}, "%s: %s", t.verb, id)
}

// newPostToggleCmd builds "<action> <post-id>" running t.
func newPostToggleCmd(app *App, action string, t toggleAction) *cobra.Command {
	return &cobra.Command{
		Use:         action + " <post-id>",
		Annotations: effect(effectServer),
		Short:       t.short,
		Long:        t.long,
		Example:     t.example,
		Args:        argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "post id"); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			return t.run(cmd.Context(), app, s, args[0])
		},
	}
}

// doneList collects the results of a command that changes several things.
// Text lines are printed as each change succeeds; with --json the array is
// printed at the end, or before the error when a change fails, so that the
// output always lists what was done.
type doneList []actionResult

func (d *doneList) add(app *App, r actionResult, verb string) {
	*d = append(*d, r)
	if !app.json {
		fmt.Fprintf(app.Out, "%s: %s\n", verb, cmp.Or(r.ID, r.Team))
	}
}

// fail prints with --json what was done so far, and adds what failed to err,
// keeping its exit code.
func (d doneList) fail(app *App, what string, err error) error {
	if app.json && len(d) > 0 {
		_ = d.finish(app)
	}
	return clierr.Wrap(clierr.ExitCode(err), err, "%s: %v", what, err)
}

// finish prints the array with --json; an empty list is [].
func (d doneList) finish(app *App) error {
	if !app.json {
		return nil
	}
	if d == nil {
		d = doneList{}
	}
	return app.printJSON([]actionResult(d))
}

// validIDs rejects any argument that is not a 26-character id; what names
// the kind of id in the error.
func validIDs(ids []string, what string) error {
	for _, id := range ids {
		if err := validID(id, what); err != nil {
			return err
		}
	}
	return nil
}
