package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
	"github.com/VitalyOstanin/mm-cli/internal/resolve"
)

func newThreadCmd(app *App) *cobra.Command {
	var truncate int
	cmd := &cobra.Command{
		Use:   "thread <post-id>",
		Short: "Show a whole thread, oldest first",
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
			pl, resp, err := s.Client.GetPostThread(cmd.Context(), args[0], "", false)
			if err != nil {
				return clierr.API(resp, err)
			}
			return s.printPosts(cmd.Context(), render.Sorted(pl), truncate)
		},
	}
	addTruncateFlag(cmd, &truncate)
	return cmd
}

func newSearchCmd(app *App) *cobra.Command {
	var in, from string
	var n, truncate int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search messages in the team",
		Long: `Search messages in the selected team with the Mattermost search syntax
("exact phrase", -excluded, in:channel, from:user, before:/after:/on:YYYY-MM-DD).
--in and --from add in: and from: to the query.`,
		Example: `  mm-cli post search "release notes" -n 10
  mm-cli post search deploy --in town-square --from @bob`,
		Args: argsRange(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := checkLimit(n); err != nil {
				return err
			}
			terms := strings.TrimSpace(args[0])
			if terms == "" {
				return clierr.Usage("the query is empty")
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			team := s.Team
			if in != "" {
				a, err := resolve.Parse(in)
				if err != nil {
					return err
				}
				if a.Kind != resolve.KindName {
					return clierr.Usage("--in takes a channel name or team/channel")
				}
				if a.Team != "" {
					team = a.Team
				}
				terms += " in:" + a.Name
			}
			if from != "" {
				u := strings.TrimPrefix(strings.ToLower(from), "@")
				if a, err := resolve.Parse("@" + u); err != nil || a.Kind != resolve.KindDirect {
					return clierr.Usage("invalid --from %q", from)
				}
				terms += " from:" + u
			}
			if team == "" {
				return clierr.Usage("no team selected; pass --team")
			}
			t, resp, err := s.Client.GetTeamByName(ctx, team, "")
			if err != nil {
				return clierr.API(resp, err)
			}
			page, isOr := 0, false
			// The server reads the offset in seconds (time.FixedZone), which
			// also keeps half-hour zones exact.
			_, offset := app.Now().In(app.Location).Zone()
			pl, resp, err := s.Client.SearchPostsWithParams(ctx, t.Id, &model.SearchParameter{
				Terms: &terms, IsOrSearch: &isOr, TimeZoneOffset: &offset, Page: &page, PerPage: &n,
			})
			if err != nil {
				return clierr.API(resp, err)
			}
			return s.printPosts(ctx, lastN(render.Sorted(pl), n), truncate)
		},
	}
	cmd.Flags().StringVar(&in, "in", "", "only in this channel (name or team/name)")
	cmd.Flags().StringVar(&from, "from", "", "only from this user (@name)")
	addLimitFlag(cmd, &n, "results")
	addTruncateFlag(cmd, &truncate)
	return cmd
}

func newHistoryCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "history <post-id>",
		Short: "Show earlier versions of your message",
		Long:  "Show earlier versions of your edited message, oldest first. The id of each version is what 'post restore' takes.",
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
			versions, resp, err := s.Client.GetEditHistoryForPost(cmd.Context(), args[0])
			if err != nil {
				return clierr.API(resp, err)
			}
			// Earlier versions are stored as deleted posts, so render.Sorted,
			// which drops deleted posts, does not fit here.
			slices.SortFunc(versions, func(a, b *model.Post) int {
				return cmp.Or(cmp.Compare(a.CreateAt, b.CreateAt), cmp.Compare(a.Id, b.Id))
			})
			return s.printPosts(cmd.Context(), versions, 0)
		},
	}
}

func newRestoreCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <post-id> <version-id>",
		Short: "Restore an earlier version of your message",
		Args:  argsRange(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validID(args[0], "post id"); err != nil {
				return err
			}
			if err := validID(args[1], "version id"); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			p, resp, err := s.Client.RestorePostVersion(cmd.Context(), args[0], args[1])
			if err != nil {
				return clierr.API(resp, err)
			}
			if app.json {
				return app.printJSON(s.ownPost(p))
			}
			fmt.Fprintf(app.Out, "Restored: %s\n", p.Id)
			return nil
		},
	}
}
