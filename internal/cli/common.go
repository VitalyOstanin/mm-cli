package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// addLimitFlag declares -n/--limit, 20 by default; what names the counted
// items in the help text.
func addLimitFlag(cmd *cobra.Command, n *int, what string) {
	cmd.Flags().IntVarP(n, "limit", "n", 20, fmt.Sprintf("number of %s (1-%d)", what, maxPerPage))
}

// checkLimit rejects an -n outside 1..maxPerPage.
func checkLimit(n int) error {
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
	return render.FromModel(p, map[string]string{s.Profile.UserID: s.Profile.Username})
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
	short string // cobra Short
	verb  string // start of the text result, e.g. "Pinned"
	done  string // action field of the JSON result, e.g. "pinned"
	call  func(ctx context.Context, s *Session, id string) (*model.Response, error)
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
		Use:   action + " <post-id>",
		Short: t.short,
		Args:  argsRange(1, 1),
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
