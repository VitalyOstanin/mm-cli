package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/spf13/cobra"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

func newThreadsCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thread",
		Short: "Followed threads: list, follow, unfollow, mark as read",
		Long: `Threads are the replies under a root post ("Threads" in the Mattermost
web app, Collapsed Reply Threads). A thread is identified by the id of its
root post: the same id that "mm-cli post thread <id>" reads and
"mm-cli send <channel> <text> --reply <id>" replies to. Giving the id of a
reply works too: the command acts on its root post.

You follow the threads you started or replied to, and any thread you follow
by hand. Reading (thread list, post thread, watch) does not mark anything as
read; only "mm-cli thread read" does.

Threads are kept per team: --team, or the profile's team, selects one;
without either, thread list and thread read --all cover all your teams.`,
		Example: `  mm-cli thread list --unread
  mm-cli post thread <root-post-id>
  mm-cli send town-square "my answer" --reply <root-post-id>
  mm-cli thread read <root-post-id>`,
	}
	cmd.AddCommand(newThreadListCmd(app), newThreadFollowCmd(app, true), newThreadFollowCmd(app, false), newThreadReadCmd(app))
	return cmd
}

func newThreadListCmd(app *App) *cobra.Command {
	var n, truncate int
	var unread bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show followed threads, most recent reply first",
		Long: `Show the threads you follow, the one with the most recent reply first: time
of the last reply, channel, author and text of the root post, number of
replies, unread replies and unread mentions, and the root post id.

--unread keeps only threads with unread replies. Listing does not mark
anything as read; read the replies with "mm-cli post thread <id>".

With --json the output is an array of objects with the fields id, team,
channel_id, channel (an address mm-cli accepts: team/name, @user, or an id
for group channels), channel_type (O, P, D, G), root (the root post, as in
post list), reply_count, unread_replies, unread_mentions and last_reply_at
(RFC 3339, empty when there are no replies).`,
		Example: `  mm-cli thread list --unread
  mm-cli thread list -n 50 --truncate 80
  mm-cli thread list --unread --json`,
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
			ctx := cmd.Context()
			teams, err := listTeams(ctx, s)
			if err != nil {
				return err
			}
			found, err := collectThreads(ctx, s, teams, unread, n)
			if err != nil {
				return err
			}
			out, err := s.renderThreads(ctx, teams, found)
			if err != nil {
				return err
			}
			if app.json {
				return app.printJSON(out)
			}
			return render.ThreadsText(app.Out, out, app.Location, truncate)
		},
	}
	cmd.Flags().BoolVar(&unread, "unread", false, "only threads with unread replies")
	addLimitFlag(cmd, &n, "threads")
	addTruncateFlag(cmd, &truncate)
	return cmd
}

// teamThread is a thread with the name of the team it was listed for.
type teamThread struct {
	th   *model.ThreadResponse
	team string
}

// collectThreads returns up to n followed threads of teams, most recent reply
// first. A thread of a direct or group channel, which the server lists for
// every team, is kept once.
func collectThreads(ctx context.Context, s *Session, teams []*model.Team, unread bool, n int) ([]teamThread, error) {
	seen := map[string]bool{}
	var out []teamThread
	for _, t := range teams {
		res, resp, err := s.Client.GetUserThreads(ctx, s.Profile.UserID, t.Id,
			model.GetUserThreadsOpts{PageSize: uint64(n), Extended: true, Unread: unread}) //nolint:gosec // n is 1-200, checked by checkLimit
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		for _, th := range res.Threads {
			if th.Post == nil || seen[th.PostId] {
				continue
			}
			seen[th.PostId] = true
			out = append(out, teamThread{th: th, team: t.Name})
		}
	}
	slices.SortFunc(out, func(a, b teamThread) int {
		return cmp.Or(cmp.Compare(b.th.LastReplyAt, a.th.LastReplyAt), strings.Compare(a.th.PostId, b.th.PostId))
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// renderThreads turns threads into output records: channel addresses from one
// channel listing, author names from one user lookup.
func (s *Session) renderThreads(ctx context.Context, teams []*model.Team, threads []teamThread) ([]render.Thread, error) {
	allTeams := s.Team == ""
	list, err := listChannels(ctx, s, teams, allTeams)
	if err != nil {
		return nil, err
	}
	channels := make(map[string]teamChannel, len(list))
	for _, tc := range list {
		channels[tc.ch.Id] = tc
	}
	var ids []string
	for _, t := range threads {
		ids = append(ids, t.th.Post.UserId)
		if tc, ok := channels[t.th.Post.ChannelId]; ok && tc.ch.Type == model.ChannelTypeDirect {
			ids = append(ids, directPeer(tc.ch, s.Profile.UserID))
		}
	}
	names := s.usernames(ctx, ids)
	out := make([]render.Thread, len(threads))
	for i, t := range threads {
		r := render.Thread{
			ID:             t.th.PostId,
			Team:           t.team,
			ChannelID:      t.th.Post.ChannelId,
			Root:           render.FromModel(t.th.Post, names),
			ReplyCount:     t.th.ReplyCount,
			UnreadReplies:  t.th.UnreadReplies,
			UnreadMentions: t.th.UnreadMentions,
		}
		// The server leaves the reply count of the root post at zero here.
		r.Root.ReplyCount = t.th.ReplyCount
		if t.th.LastReplyAt > 0 {
			r.LastReplyAt = time.UnixMilli(t.th.LastReplyAt).UTC().Format(time.RFC3339)
		}
		r.Channel, r.ChannelType, r.Label = threadChannel(channels, t.th.Post.ChannelId, allTeams && len(teams) > 1, names, s.Profile.UserID)
		out[i] = r
	}
	return out, nil
}

// threadChannel returns the address mm-cli accepts for a channel, its type and
// the label of text output. qualify adds the team to the label of team
// channels. A channel missing from the user's list is shown by id.
func threadChannel(channels map[string]teamChannel, id string, qualify bool, names map[string]string, me string) (addr, typ, label string) {
	tc, ok := channels[id]
	if !ok {
		return id, "", id
	}
	typ = string(tc.ch.Type)
	switch tc.ch.Type {
	case model.ChannelTypeOpen, model.ChannelTypePrivate:
		addr = tc.team + "/" + tc.ch.Name
		label = "#" + tc.ch.Name
		if qualify {
			label = "#" + addr
		}
	case model.ChannelTypeDirect:
		addr = id
		if name := names[directPeer(tc.ch, me)]; name != "" {
			addr = "@" + name
		}
		label = addr
	default:
		addr, label = id, "group"
	}
	return addr, typ, label
}

// threadResolver finds the thread of a post: its root post and the team whose
// thread routes serve it. Channels and the fallback team are looked up once
// per command.
type threadResolver struct {
	s        *Session
	channels map[string]*model.Channel
	fallback string
}

func newThreadResolver(s *Session) *threadResolver {
	return &threadResolver{s: s, channels: map[string]*model.Channel{}}
}

// resolve returns the root post id and the team id for post id, which may be
// a root post or a reply. The team is that of the post's channel; for a
// direct or group channel it is the selected team, or else the user's first
// team.
func (r *threadResolver) resolve(ctx context.Context, id string) (root, team string, err error) {
	p, resp, err := r.s.Client.GetPost(ctx, id, "")
	if err != nil {
		return "", "", clierr.API(resp, err)
	}
	ch, ok := r.channels[p.ChannelId]
	if !ok {
		if ch, resp, err = r.s.Client.GetChannel(ctx, p.ChannelId); err != nil {
			return "", "", clierr.API(resp, err)
		}
		r.channels[p.ChannelId] = ch
	}
	team = ch.TeamId
	if team == "" {
		if r.fallback == "" {
			teams, err := listTeams(ctx, r.s)
			if err != nil {
				return "", "", err
			}
			if len(teams) == 0 {
				return "", "", clierr.New(clierr.CodeNotFound, "you are not a member of any team")
			}
			r.fallback = teams[0].Id
		}
		team = r.fallback
	}
	return cmp.Or(p.RootId, p.Id), team, nil
}

// doneList collects the results of a command that changes several things.
// Text lines are printed as each change succeeds, so a failure part-way shows
// what was already done; with --json the whole array is printed at the end.
type doneList []actionResult

func (d *doneList) add(app *App, r actionResult, verb string) {
	*d = append(*d, r)
	if !app.json {
		fmt.Fprintf(app.Out, "%s: %s\n", verb, cmp.Or(r.ID, r.Team))
	}
}

// fail adds what failed to err, keeping its exit code.
func (d doneList) fail(what string, err error) error {
	return clierr.Wrap(clierr.ExitCode(err), err, "%s: %v", what, err)
}

func (d doneList) finish(app *App) error {
	if app.json {
		return app.printJSON([]actionResult(d))
	}
	return nil
}

// validIDs rejects any argument that is not a 26-character id.
func validIDs(ids []string) error {
	for _, id := range ids {
		if err := validID(id, "post id"); err != nil {
			return err
		}
	}
	return nil
}

func newThreadFollowCmd(app *App, follow bool) *cobra.Command {
	use, short, verb, action := "follow", "Follow threads", "Following", "followed"
	long := `Follow threads, so that new replies show up in thread list.
Following also marks the thread read: the server counts only replies that
come after it as unread.`
	if !follow {
		use, short, verb, action = "unfollow", "Stop following threads", "Unfollowed", "unfollowed"
		long = `Stop following threads; they disappear from thread list.`
	}
	return &cobra.Command{
		Use:   use + " <post-id>...",
		Short: short,
		Long: long + ` Each argument is a
post id: a root post, or a reply (the command acts on its root post).

With --json the output is an array of {"action": "` + action + `", "id": <root post id>}.
Lines are printed as each thread is done, so after an error the earlier
threads are already changed.

Exit code 2 for an invalid id, 4 without access to the post, 5 when the
post does not exist.`,
		Example: "  mm-cli thread " + use + " <post-id>\n  mm-cli thread " + use + " <post-id> <post-id> --json",
		Args:    argsRange(1, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validIDs(args); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := cmd.Context()
			res := newThreadResolver(s)
			var done doneList
			for _, id := range args {
				root, team, err := res.resolve(ctx, id)
				if err != nil {
					return done.fail(id, err)
				}
				if resp, err := s.Client.UpdateThreadFollowForUser(ctx, s.Profile.UserID, team, root, follow); err != nil {
					return done.fail(root, clierr.API(resp, err))
				}
				done.add(app, actionResult{Action: action, ID: root}, verb)
			}
			return done.finish(app)
		},
	}
}

func newThreadReadCmd(app *App) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "read (<post-id>... | --all)",
		Short: "Mark threads as read",
		Long: `Mark threads as read, up to now. Each argument is a post id: a root post, or
a reply (its thread is marked). --all marks every followed thread as read in
the selected team (--team or the profile's team), or in each of your teams.

With --json the output is an array of {"action": "marked_read", "id": ...},
or {"action": "marked_read", "team": ...} for --all.

Exit code 2 when neither ids nor --all are given, or both are; 4 without
access to the post; 5 when the post does not exist.`,
		Example: `  mm-cli thread read <post-id>
  mm-cli thread read --all
  mm-cli thread read --all --team myteam --json`,
		Args: argsRange(0, unlimited),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case all && len(args) > 0:
				return clierr.Usage("give post ids or --all, not both")
			case !all && len(args) == 0:
				return clierr.Usage("give at least one post id, or --all")
			}
			if err := validIDs(args); err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := cmd.Context()
			if all {
				return readAllThreads(ctx, app, s)
			}
			res := newThreadResolver(s)
			var done doneList
			for _, id := range args {
				root, team, err := res.resolve(ctx, id)
				if err != nil {
					return done.fail(id, err)
				}
				if _, resp, err := s.Client.UpdateThreadReadForUser(ctx, s.Profile.UserID, team, root, app.Now().UnixMilli()); err != nil {
					return done.fail(root, clierr.API(resp, err))
				}
				done.add(app, actionResult{Action: "marked_read", ID: root}, "Marked read")
			}
			return done.finish(app)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "mark every followed thread as read (in --team, or in all your teams)")
	return cmd
}

// readAllThreads marks every thread read in the selected team, or in each of
// the user's teams.
func readAllThreads(ctx context.Context, app *App, s *Session) error {
	teams, err := listTeams(ctx, s)
	if err != nil {
		return err
	}
	var done doneList
	for _, t := range teams {
		if resp, err := s.Client.UpdateThreadsReadForUser(ctx, s.Profile.UserID, t.Id); err != nil {
			return done.fail(t.Name, clierr.API(resp, err))
		}
		done.add(app, actionResult{Action: "marked_read", Team: t.Name}, "Marked all threads read")
	}
	return done.finish(app)
}
