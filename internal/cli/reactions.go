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

const (
	// reactionSearchPage is the page size of the search for your messages.
	reactionSearchPage = 100
	// reactionSearchPages bounds the paging, so that one command never turns
	// into an unbounded series of requests.
	reactionSearchPages = 10
	// reactionTruncate is how many characters of a message the text output
	// keeps when --truncate is not given; --json always has the whole text.
	reactionTruncate = 80
)

func newReactionsCmd(app *App) *cobra.Command {
	var since string
	var n, truncate int
	cmd := &cobra.Command{
		Use:         "list [<post-id>]",
		Annotations: effect(effectRead),
		Short:       "List reactions on one message, or on your messages",
		Long: `With a post id: list all reactions on that one message, yours and other
people's, whoever wrote it, oldest first. Without it: list the reactions other
people put on your messages (history). Mattermost itself has no such list.
Reactions are managed with post reaction add (or replace one of yours with
--replace) and post reaction remove; the reactions of a message also show in
the "reactions" field of messages and thread output.

With a post id the command makes one request for the reactions, one for the
message and one for its channel (and team), plus a name lookup. --since is a
usage error with a post id; -n then keeps only the newest N reactions, and
without -n all of them are listed. The records are as described below, with
"message" the text of that message.

History mode: only messages you posted since --since (default 7d) are looked at, replies
included: a reaction made today on an older message is not listed. Your own
reactions are left out. Without --team all your teams are searched with one
request (direct and group messages too); with --team only that team and your
direct and group messages. The messages come from the server search
(from:you after:date) and carry their reactions, so there are no further
requests for them; names of channels and users take one request each.

--since takes an RFC 3339 time or a duration back from now (90m, 2h, 3d).
-n keeps the newest N reactions (1-200, default 20); the output is oldest
first. In text, each reaction is a block: time, @user, the emoji (a standard
one as its character followed by its name, "👍 :+1:"; a custom one as
:name:), channel (its address; the display name for a group channel, whose
address is an id), the message on one line shortened to 80 characters
(--truncate N changes that), and the mattermost:// link that opens the
message in the Desktop app. Reading never marks anything read.

With --json it prints an array of {"post_id": ..., "root_id": the thread
root of the message ("" for a root post), "channel_id": ..., "channel": the
channel address (the name, @user for a direct channel, the id for a group
channel), "channel_display_name": the name the app shows, "team": the team
of the link (the selected team for direct and group messages), "user_id":
..., "user": the username of who reacted, "emoji": the name without colons,
"create_at": the time of the reaction (RFC 3339, UTC), "message": the whole
text of your message, "permalink": https://<server>/<team>/pl/<post-id>,
"app_link": mattermost://<server>/<team>/pl/<post-id>}; [] when there are
none. Exit code 2 for an invalid --since, -n or post id, or --since together
with a post id; 3 when not signed in; 4 without access to the message; 5 for
an unknown team or message.`,
		Example: `  mm-cli post reaction list
  mm-cli post reaction list --since 2d -n 50 --truncate 120
  mm-cli post reaction list --team myteam --json
  mm-cli post reaction list <post-id> --json`,
		Args: argsRange(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if len(args) == 1 {
				if cmd.Flags().Changed("since") {
					return clierr.Usage("--since cannot be used with a post id: all reactions of the message are listed")
				}
				if err := validID(args[0], "post id"); err != nil {
					return err
				}
				if cmd.Flags().Changed("number") {
					if err := checkNumber(n); err != nil {
						return err
					}
				} else {
					n = 0
				}
				return runPostReactions(ctx, app, args[0], n, truncate)
			}
			if err := checkNumber(n); err != nil {
				return err
			}
			when, err := parseSince(since, app.Now())
			if err != nil {
				return err
			}
			s, err := app.openSession()
			if err != nil {
				return err
			}
			defer s.Close()
			teams, err := reactionTeams(ctx, app, s)
			if err != nil {
				return err
			}
			posts, err := s.ownPostsSince(ctx, teams, when)
			if err != nil {
				return err
			}
			rs, err := s.reactionRecords(ctx, teams, posts)
			if err != nil {
				return err
			}
			if len(rs) > n {
				rs = rs[len(rs)-n:]
			}
			if app.json {
				if rs == nil {
					rs = []render.Reaction{}
				}
				return app.printJSON(rs)
			}
			if truncate == 0 {
				truncate = reactionTruncate
			}
			return render.ReactionsText(app.Out, rs, app.Location, truncate)
		},
	}
	cmd.Flags().StringVar(&since, "since", "7d", "only your messages after this time (RFC 3339 or 90m, 2h, 3d)")
	addNumberFlag(cmd, &n, "reactions")
	addTruncateFlag(cmd, &truncate)
	return cmd
}

// runPostReactions lists every reaction on one message, oldest first; n > 0
// keeps the newest n. The message and its channel are fetched for the links.
func runPostReactions(ctx context.Context, app *App, id string, n, truncate int) error {
	s, err := app.openSession()
	if err != nil {
		return err
	}
	defer s.Close()
	p, resp, err := s.Client.GetPost(ctx, id, "")
	if err != nil {
		return clierr.API(resp, err)
	}
	rs, resp, err := s.Client.GetReactions(ctx, id)
	if err != nil {
		return clierr.API(resp, err)
	}
	ch, resp, err := s.Client.GetChannel(ctx, p.ChannelId)
	if err != nil {
		return clierr.API(resp, err)
	}
	slices.SortStableFunc(rs, func(a, b *model.Reaction) int {
		return cmp.Or(cmp.Compare(a.CreateAt, b.CreateAt), strings.Compare(a.EmojiName, b.EmojiName), strings.Compare(a.UserId, b.UserId))
	})
	if n > 0 && len(rs) > n {
		rs = rs[len(rs)-n:]
	}
	var ids []string
	if ch.Type == model.ChannelTypeDirect {
		ids = append(ids, directPeer(ch, s.Server.UserID))
	}
	for _, r := range rs {
		ids = append(ids, r.UserId)
	}
	var out []render.Reaction
	if len(rs) > 0 {
		names := s.usernames(ctx, ids)
		var team string
		if ch.TeamId != "" {
			t, resp, err := s.Client.GetTeam(ctx, ch.TeamId, "")
			if err != nil {
				return clierr.API(resp, err)
			}
			team = t.Name
		} else {
			// A direct or group message has no team; the app's link needs one.
			team = s.linkTeam(ctx)
		}
		channel := reactionChannelName(ch, s.Server.UserID, names)
		display := channelDisplayName(ch, s.Server.UserID, names)
		label := channelLabel(ch, s.Server.UserID, names, true)
		for _, r := range rs {
			out = append(out, render.ReactionFromModel(r, p, names, channel, display, label, team, s.Server.URL))
		}
	}
	if app.json {
		if out == nil {
			out = []render.Reaction{}
		}
		return app.printJSON(out)
	}
	if truncate == 0 {
		truncate = reactionTruncate
	}
	return render.ReactionsText(app.Out, out, app.Location, truncate)
}

// reactionTeams returns the team named by --team, or every team of the user.
// The saved server's default team is not used: the command is about all
// your messages.
func reactionTeams(ctx context.Context, app *App, s *Session) ([]*model.Team, error) {
	if app.team != "" {
		t, resp, err := s.Client.GetTeamByName(ctx, app.team, "")
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		return []*model.Team{t}, nil
	}
	teams, resp, err := s.Client.GetTeamsForUser(ctx, s.Server.UserID, "")
	if err != nil {
		return nil, clierr.API(resp, err)
	}
	return teams, nil
}

// ownPostsSince returns your live messages created after when, oldest first.
// With one team in teams selected by --team the search is in that team; else
// one search covers all teams. The search's after: is a whole day, so it asks
// from the day before and the exact time is applied here.
func (s *Session) ownPostsSince(ctx context.Context, teams []*model.Team, when time.Time) ([]*model.Post, error) {
	app := s.app
	me := s.Server.Username
	if me == "" {
		me = s.usernames(ctx, []string{s.Server.UserID})[s.Server.UserID]
	}
	if me == "" {
		return nil, clierr.Usage("cannot tell your username; run `mm-cli auth login` again")
	}
	teamID := ""
	if app.team != "" && len(teams) == 1 {
		teamID = teams[0].Id
	}
	terms := "from:" + me + " after:" + when.In(app.Location).AddDate(0, 0, -1).Format(time.DateOnly)
	// The server reads the offset in seconds, as in post search.
	_, offset := app.Now().In(app.Location).Zone()
	isOr, perPage := false, reactionSearchPage
	var out []*model.Post
	seen := map[string]bool{}
	// One page past the bound is requested only to learn whether anything
	// is left: a full last page alone does not mean the search was cut.
	for page := 0; page <= reactionSearchPages; page++ {
		pl, resp, err := s.Client.SearchPostsWithParams(ctx, teamID, &model.SearchParameter{
			Terms: &terms, IsOrSearch: &isOr, TimeZoneOffset: &offset, Page: &page, PerPage: &perPage,
		})
		if err != nil {
			return nil, clierr.API(resp, err)
		}
		if page == reactionSearchPages {
			if pl != nil && len(pl.Order) > 0 {
				fmt.Fprintf(app.Err, "mm-cli: warning: only the newest %d messages were searched; use a later --since\n", reactionSearchPages*reactionSearchPage)
			}
			return out, nil
		}
		for _, p := range render.Sorted(pl) {
			if seen[p.Id] {
				continue
			}
			seen[p.Id] = true
			if p.UserId == s.Server.UserID && p.CreateAt > when.UnixMilli() {
				out = append(out, p)
			}
		}
		if pl == nil || len(pl.Order) < perPage {
			return out, nil
		}
	}
	return out, nil
}

// reactionRecords turns the reactions of others on posts into records, oldest
// first. The reactions come with the search results; the channels and users
// are looked up once for all of them. Nothing is requested when no one
// reacted.
func (s *Session) reactionRecords(ctx context.Context, teams []*model.Team, posts []*model.Post) ([]render.Reaction, error) {
	app := s.app
	type item struct {
		r *model.Reaction
		p *model.Post
	}
	var items []item
	for _, p := range posts {
		if p.Metadata == nil {
			continue
		}
		for _, r := range p.Metadata.Reactions {
			if r != nil && r.UserId != s.Server.UserID {
				items = append(items, item{r, p})
			}
		}
	}
	if len(items) == 0 {
		return nil, nil
	}
	chans, err := listChannels(ctx, s, teams, app.team == "")
	if err != nil {
		return nil, err
	}
	byID := make(map[string]teamChannel, len(chans))
	for _, tc := range chans {
		byID[tc.ch.Id] = tc
	}
	// Peers are looked up only for direct channels that hold a found
	// reaction: names of every direct partner would cost needless requests.
	var ids []string
	for _, it := range items {
		ids = append(ids, it.r.UserId)
		if tc, ok := byID[it.p.ChannelId]; ok && tc.ch.Type == model.ChannelTypeDirect {
			ids = append(ids, directPeer(tc.ch, s.Server.UserID))
		}
	}
	names := s.usernames(ctx, ids)
	// Direct and group messages belong to no team; the link needs one, so
	// the selected team stands in, as the app does.
	// The teams were fetched anyway: hand them over so linkTeam need not ask.
	s.rememberTeams(teams)
	fallback := s.linkTeam(ctx)
	slices.SortStableFunc(items, func(a, b item) int {
		return cmp.Or(cmp.Compare(a.r.CreateAt, b.r.CreateAt), strings.Compare(a.p.Id, b.p.Id), strings.Compare(a.r.EmojiName, b.r.EmojiName))
	})
	out := make([]render.Reaction, len(items))
	for i, it := range items {
		channel, team := it.p.ChannelId, fallback
		display, label := channel, channel
		if tc, ok := byID[it.p.ChannelId]; ok {
			channel = reactionChannelName(tc.ch, s.Server.UserID, names)
			display = channelDisplayName(tc.ch, s.Server.UserID, names)
			label = channelLabel(tc.ch, s.Server.UserID, names, true)
			team = cmp.Or(tc.team, fallback)
		}
		out[i] = render.ReactionFromModel(it.r, it.p, names, channel, display, label, team, s.Server.URL)
	}
	return out, nil
}

// reactionChannelName is the address of a channel, as mm-cli accepts it: the
// name of a public or private channel, @peer for a direct channel, the id for
// a group channel (its name is a hash, its display name is not unique).
func reactionChannelName(ch *model.Channel, me string, names map[string]string) string {
	switch ch.Type {
	case model.ChannelTypeDirect:
		if n := names[directPeer(ch, me)]; n != "" {
			return "@" + n
		}
		return ch.Id
	case model.ChannelTypeGroup:
		return ch.Id
	}
	return cmp.Or(ch.Name, ch.Id)
}

// channelDisplayName is the name of a channel as the app shows it: the display
// name, @peer for a direct channel (the id when the peer is unknown).
func channelDisplayName(ch *model.Channel, me string, names map[string]string) string {
	if ch.Type == model.ChannelTypeDirect {
		return reactionChannelName(ch, me, names)
	}
	return cmp.Or(ch.DisplayName, ch.Name, ch.Id)
}

// channelLabel is what the text output and notifications of watch show for a
// channel: the display name of a message's channel (@peer for a direct one).
// Reactions show the address, except in a group channel, whose address is an
// id nobody reads.
func channelLabel(ch *model.Channel, me string, names map[string]string, reaction bool) string {
	if reaction && ch.Type != model.ChannelTypeGroup {
		return reactionChannelName(ch, me, names)
	}
	return channelDisplayName(ch, me, names)
}
