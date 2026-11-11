package cli

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// newPinToggleCmd returns post pin or post unpin, after Pin to Channel and
// Unpin from Channel in the app's message menu.
func newPinToggleCmd(app *App, action string) *cobra.Command {
	return newPostToggleCmd(app, action, pinActions[action])
}

var pinActions = map[string]toggleAction{
	"pin": {short: "Pin a message to its channel", verb: "Pinned", done: "pinned",
		long: `Pin a message to its channel; every member of the channel sees the pin.

With --json it prints {"action": "pinned", "id": "<post id>"}. Exit code 2 for
an invalid id, 4 without access, 5 when the message does not exist.`,
		example: `  mm-cli post pin <post-id>
  mm-cli post pin <post-id> --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.PinPost(ctx, id)
		}},
	"unpin": {short: "Unpin a message", verb: "Unpinned", done: "unpinned",
		long: `Unpin a message for every member of its channel.

With --json it prints {"action": "unpinned", "id": "<post id>"}. Exit code 2 for
an invalid id, 4 without access, 5 when the message does not exist.`,
		example: `  mm-cli post unpin <post-id>
  mm-cli post unpin <post-id> --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.UnpinPost(ctx, id)
		}},
}

// newPinnedCmd returns channel pinned, after Pinned messages in the channel
// header of the app.
func newPinnedCmd(app *App) *cobra.Command {
	var truncate int
	cmd := &cobra.Command{
		Use:         "pinned <channel>",
		Annotations: effect(effectRead),
		Short:       "Show pinned messages, oldest first",
		Long: channelHelp + `

Shows the pinned messages of the channel, oldest first, without marking
anything read. Pin and unpin with mm-cli post pin and post unpin. With
--json it prints an array of messages (mm-cli help output); [] when nothing
is pinned. Exit code 2 for an invalid channel address, 5 when the channel does
not exist or is not visible to you.`,
		Example: `  mm-cli channel pinned town-square
  mm-cli channel pinned myteam:town-square --json`,
		Args: argsChannel(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			var posts []*model.Post
			err = s.withChannel(ctx, args[0], func(id string) error {
				pl, resp, err := s.Client.GetPinnedPosts(ctx, id, "")
				if err != nil {
					return clierr.API(resp, err)
				}
				posts = render.Sorted(pl)
				return nil
			})
			if err != nil {
				return err
			}
			return s.printPosts(ctx, posts, truncate)
		},
	}
	addTruncateFlag(cmd, &truncate)
	return cmd
}
