package cli

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

func newSavedCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "saved",
		Short: "Saved messages (visible only to you)",
		Long: `Save messages for later and list them, as "Save" in the app does. Saved
messages are visible only to you; others do not see that you saved them.`,
	}
	cmd.AddCommand(newSavedToggleCmd(app, "add"), newSavedToggleCmd(app, "remove"), newSavedListCmd(app))
	return cmd
}

func newSavedToggleCmd(app *App, action string) *cobra.Command {
	return newPostToggleCmd(app, action, savedActions[action])
}

var savedActions = map[string]toggleAction{
	"add": {short: "Save a message", verb: "Saved", done: "saved",
		long: `Save a message; only you see it under saved messages.

With --json it prints {"action": "saved", "id": "<post id>"}. Exit code 2 for an
invalid id.`,
		example: `  mm-cli saved add <post-id>
  mm-cli saved add <post-id> --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.UpdatePreferences(ctx, s.Profile.UserID, flaggedPref(s, id))
		}},
	"remove": {short: "Remove a message from saved", verb: "Removed from saved", done: "unsaved",
		long: `Remove a message from your saved messages; the message itself stays.

With --json it prints {"action": "unsaved", "id": "<post id>"}. Exit code 2 for
an invalid id.`,
		example: `  mm-cli saved remove <post-id>
  mm-cli saved remove <post-id> --json`,
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
		Long: `Show your saved messages from all channels, oldest first; -n limits how many
are fetched (default 20, at most 200). Marks nothing read.

With --json it prints an array of messages (mm-cli help output); [] when
nothing is saved.`,
		Example: `  mm-cli saved list
  mm-cli saved list -n 50 --json`,
		Args: argsRange(0, 0),
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
