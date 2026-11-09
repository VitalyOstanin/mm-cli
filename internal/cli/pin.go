package cli

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

func newPinCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "pin", Short: "Pinned messages of a channel"}
	cmd.AddCommand(newPinToggleCmd(app, "add"), newPinToggleCmd(app, "remove"), newPinListCmd(app))
	return cmd
}

func newPinToggleCmd(app *App, action string) *cobra.Command {
	return newPostToggleCmd(app, action, pinActions[action])
}

var pinActions = map[string]toggleAction{
	"add": {short: "Pin a message to its channel", verb: "Pinned", done: "pinned",
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.PinPost(ctx, id)
		}},
	"remove": {short: "Unpin a message", verb: "Unpinned", done: "unpinned",
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.UnpinPost(ctx, id)
		}},
}

func newPinListCmd(app *App) *cobra.Command {
	var truncate int
	cmd := &cobra.Command{
		Use:   "list <channel>",
		Short: "Show pinned messages, oldest first",
		Long:  channelHelp,
		Args:  argsRange(1, 1),
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
