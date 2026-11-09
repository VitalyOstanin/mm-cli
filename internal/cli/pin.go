package cli

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

func newPinCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pin",
		Short: "Pinned messages of a channel",
		Long: `Pin and unpin messages, and list the pinned messages of a channel. Pins are
visible to every member of the channel.`,
	}
	cmd.AddCommand(newPinToggleCmd(app, "add"), newPinToggleCmd(app, "remove"), newPinListCmd(app))
	return cmd
}

func newPinToggleCmd(app *App, action string) *cobra.Command {
	return newPostToggleCmd(app, action, pinActions[action])
}

var pinActions = map[string]toggleAction{
	"add": {short: "Pin a message to its channel", verb: "Pinned", done: "pinned",
		long: `Pin a message to its channel; every member of the channel sees the pin.

With --json it prints {"action": "pinned", "id": "<post id>"}. Exit code 2 for
an invalid id, 4 without access, 5 when the message does not exist.`,
		example: `  mm-cli pin add <post-id>
  mm-cli pin add <post-id> --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.PinPost(ctx, id)
		}},
	"remove": {short: "Unpin a message", verb: "Unpinned", done: "unpinned",
		long: `Unpin a message for every member of its channel.

With --json it prints {"action": "unpinned", "id": "<post id>"}. Exit code 2 for
an invalid id, 4 without access, 5 when the message does not exist.`,
		example: `  mm-cli pin remove <post-id>
  mm-cli pin remove <post-id> --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.UnpinPost(ctx, id)
		}},
}

func newPinListCmd(app *App) *cobra.Command {
	var truncate int
	cmd := &cobra.Command{
		Use:   "list <channel>",
		Short: "Show pinned messages, oldest first",
		Long: channelHelp + `

Shows the pinned messages of the channel, oldest first, without marking
anything read. With --json it prints an array of messages
(mm-cli help output); [] when nothing is pinned.`,
		Example: `  mm-cli pin list town-square
  mm-cli pin list myteam/town-square --json`,
		Args: argsRange(1, 1),
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
