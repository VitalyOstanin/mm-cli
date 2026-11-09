package cli

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

func newSavedCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "saved", Short: "Saved messages (visible only to you)"}
	cmd.AddCommand(newSavedToggleCmd(app, "add"), newSavedToggleCmd(app, "remove"), newSavedListCmd(app))
	return cmd
}

func newSavedToggleCmd(app *App, action string) *cobra.Command {
	return newPostToggleCmd(app, action, savedActions[action])
}

var savedActions = map[string]toggleAction{
	"add": {short: "Save a message", verb: "Saved", done: "saved",
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.UpdatePreferences(ctx, s.Profile.UserID, flaggedPref(s, id))
		}},
	"remove": {short: "Remove a message from saved", verb: "Removed from saved", done: "unsaved",
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.DeletePreferences(ctx, s.Profile.UserID, flaggedPref(s, id))
		}},
}

// flaggedPref is the preference that marks post id as saved.
func flaggedPref(s *Session, id string) model.Preferences {
	return model.Preferences{{
		UserId: s.Profile.UserID, Category: model.PreferenceCategoryFlaggedPost, Name: id, Value: "true",
	}}
}

func newSavedListCmd(app *App) *cobra.Command {
	var n, truncate int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show saved messages, oldest first",
		Args:  argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkLimit(n); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			pl, resp, err := s.Client.GetFlaggedPostsForUser(cmd.Context(), s.Profile.UserID, 0, n)
			if err != nil {
				return clierr.API(resp, err)
			}
			return s.printPosts(cmd.Context(), render.Sorted(pl), truncate)
		},
	}
	addLimitFlag(cmd, &n, "messages")
	addTruncateFlag(cmd, &truncate)
	return cmd
}
