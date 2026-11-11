package cli

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// newSavedToggleCmd returns post save or post unsave, after Save and Unsave in
// the app. Saved messages are visible only to their owner.
func newSavedToggleCmd(app *App, action string) *cobra.Command {
	return newPostToggleCmd(app, action, savedActions[action])
}

var savedActions = map[string]toggleAction{
	"save": {short: "Save a message", verb: "Saved", done: "saved",
		long: `Save a message, as Save in the app does; only you see it under saved messages
(mm-cli post saved), and others do not see that you saved it.

With --json it prints {"action": "saved", "id": "<post id>"}. Exit code 2 for an
invalid id.`,
		example: `  mm-cli post save <post-id>
  mm-cli post save <post-id> --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.UpdatePreferences(ctx, s.Server.UserID, flaggedPref(s, id))
		}},
	"unsave": {short: "Remove a message from saved", verb: "Removed from saved", done: "unsaved",
		long: `Remove a message from your saved messages; the message itself stays.

With --json it prints {"action": "unsaved", "id": "<post id>"}. Exit code 2 for
an invalid id.`,
		example: `  mm-cli post unsave <post-id>
  mm-cli post unsave <post-id> --json`,
		call: func(ctx context.Context, s *Session, id string) (*model.Response, error) {
			return s.Client.DeletePreferences(ctx, s.Server.UserID, flaggedPref(s, id))
		}},
}

// flaggedPref is the preference that marks post id as saved.
func flaggedPref(s *Session, id string) model.Preferences {
	return model.Preferences{{
		UserId: s.Server.UserID, Category: model.PreferenceCategoryFlaggedPost, Name: id, Value: "true",
	}}
}

// newSavedListCmd returns post saved, after Saved messages in the app.
func newSavedListCmd(app *App) *cobra.Command {
	var n, truncate int
	cmd := &cobra.Command{
		Use:         "saved",
		Annotations: effect(effectRead),
		Short:       "Show saved messages, oldest first",
		Long: `Show your saved messages from all channels, oldest first; -n limits how many
are fetched (default 20, at most 200). Marks nothing read.

With --json it prints an array of messages (mm-cli help output); [] when
nothing is saved. Exit code 2 when -n is outside 1 to 200; 3 when nobody is
signed in.`,
		Example: `  mm-cli post saved
  mm-cli post saved -n 50 --json`,
		Args: argsRange(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkNumber(n); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			pl, resp, err := s.Client.GetFlaggedPostsForUser(cmd.Context(), s.Server.UserID, 0, n)
			if err != nil {
				return clierr.API(resp, err)
			}
			return s.printPosts(cmd.Context(), render.Sorted(pl), truncate)
		},
	}
	addNumberFlag(cmd, &n, "messages")
	addTruncateFlag(cmd, &truncate)
	return cmd
}
