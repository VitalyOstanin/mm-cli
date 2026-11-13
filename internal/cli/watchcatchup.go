package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/render"
)

const (
	// watchCatchUpSlack is how far before the last sign of life a catch-up
	// starts: it absorbs the difference between the clocks of this machine and
	// the server. Messages seen twice are dropped by the printed sets.
	watchCatchUpSlack = time.Minute
	// watchCatchUpPosts caps the messages printed for one channel by one
	// catch-up.
	watchCatchUpPosts = 200
	// watchReactionLookback is how far back a catch-up looks for your
	// messages when it searches them for reactions: the search is the only way
	// to find reactions made while disconnected, and an unbounded one would be
	// costly. A reaction on an older message is not caught up.
	watchReactionLookback = 7 * 24 * time.Hour
	// watchReactionCatchUpEvery is the least time between two searches of your
	// messages for reactions: a flapping connection would otherwise repeat the
	// search of the whole lookback (up to reactionSearchPages requests through
	// the rate limiter) on every reconnect. A skipped catch-up is not lost: it
	// runs once the time is up and covers the whole gap.
	watchReactionCatchUpEvery = 10 * time.Minute
	// watchSummaryAbove is how many notifications of one catch-up are shown one
	// by one; more become a single summary.
	watchSummaryAbove = 3
)

// catchItem is a message or a reaction found by a catch-up.
type catchItem struct {
	at       int64
	post     *model.Post
	channel  string
	names    map[string]string
	reaction *render.Reaction
	rch      watchChannel // channel of the reaction
	source   *model.Reaction
}

// afterAuth runs when the server accepts the session on a connection. The
// first connection has nothing to catch up on; later ones fetch what came in
// since the previous connection last showed a sign of life.
func (w *watcher) afterAuth(ctx context.Context, prevAlive time.Time) error {
	if !w.connected {
		w.connected = true
		return nil
	}
	if prevAlive.IsZero() {
		return nil
	}
	since := prevAlive.Add(-watchCatchUpSlack)
	if !w.missedSince.IsZero() && w.missedSince.Before(since) {
		since = w.missedSince
	}
	// The reactions have a boundary of their own: it stays put while their
	// catch-up is skipped or fails, whereas the messages are caught up on every
	// reconnect.
	rsince := since
	if !w.reactionsSince.IsZero() && w.reactionsSince.Before(rsince) {
		rsince = w.reactionsSince
	}
	return w.catchUp(ctx, since, rsince, w.messages, w.reactions && w.reactionsDue())
}

// reactionsDue tells whether the search for reactions may run now.
func (w *watcher) reactionsDue() bool {
	if w.reactionsAt.IsZero() {
		return true
	}
	d := w.s.app.Now().Sub(w.reactionsAt)
	return d < 0 || d >= watchReactionCatchUpEvery
}

// reactionRetryIn is how long until a skipped or failed catch-up of reactions
// is due; ok is false when none is waiting.
func (w *watcher) reactionRetryIn() (d time.Duration, ok bool) {
	if !w.reactions || w.reactionsSince.IsZero() {
		return 0, false
	}
	return max(w.reactionsAt.Add(watchReactionCatchUpEvery).Sub(w.s.app.Now()), 0), true
}

// retryReactions catches up the reactions that an earlier reconnect left
// pending, when the time between searches is up. It runs from the loop that
// handles the frames, like the catch-up after a reconnect, so that its output
// keeps the order of the live events.
func (w *watcher) retryReactions(ctx context.Context) error {
	if w.reactionsSince.IsZero() || !w.reactionsDue() {
		return nil
	}
	return w.catchUp(ctx, time.Time{}, w.reactionsSince, false, true)
}

// catchUp prints the messages and reactions since a time that were not
// printed yet; the reactions have their own start, rsince, and are looked for
// only when reactions is set. Only an expired session (errWatchAuth) and a
// failure to write the output end watching; anything else is reported on
// stderr, and the period is fetched again after the next reconnect (the
// reactions: also when the search was skipped).
func (w *watcher) catchUp(ctx context.Context, since, rsince time.Time, messages, reactions bool) error {
	s := w.s
	var msgFailed, reactFailed bool
	failer := func(flag *bool) failFunc {
		return func(what string, resp *model.Response, err error) error {
			if ctx.Err() != nil {
				return nil
			}
			err = clierr.API(resp, err)
			if clierr.ExitCode(err) == clierr.CodeAuth {
				return errWatchAuth
			}
			*flag = true
			fmt.Fprintf(s.app.Err, "mm-cli: catch-up: cannot read %s: %v\n", what, err)
			return nil
		}
	}
	finish := func(items []catchItem) error {
		if messages {
			if msgFailed {
				w.missedSince = since
			} else {
				w.missedSince = time.Time{}
			}
		}
		if w.reactions {
			if reactions && !reactFailed {
				w.reactionsSince = time.Time{}
			} else {
				w.reactionsSince = rsince
			}
		}
		return w.printCatchUp(ctx, cmp.Or(since, rsince), items)
	}

	teams, resp, err := s.Client.GetTeamsForUser(ctx, s.Server.UserID, "")
	if err != nil {
		if e := failer(&msgFailed)("your teams", resp, err); e != nil {
			return e
		}
		reactFailed = true
		return finish(nil)
	}
	s.rememberTeams(teams)
	var items []catchItem
	if messages {
		msgs, authErr := w.catchUpMessages(ctx, teams, since.UnixMilli(), failer(&msgFailed))
		if authErr != nil {
			return authErr
		}
		items = append(items, msgs...)
	}
	if reactions {
		w.reactionsAt = s.app.Now()
		rs, authErr := w.catchUpReactions(ctx, teams, rsince, failer(&reactFailed))
		if authErr != nil {
			return authErr
		}
		items = append(items, rs...)
	}
	// Stable: on a tie a message comes before a reaction.
	slices.SortStableFunc(items, func(a, b catchItem) int { return cmp.Compare(a.at, b.at) })
	return finish(items)
}

// failFunc reports a failed step of a catch-up: it marks that part failed
// (so the period is fetched again after the next reconnect) and returns an
// error only when watching must end.
type failFunc func(what string, resp *model.Response, err error) error

// catchUpMessages finds the messages created since sinceMs in your watched
// channels. A channel whose last message is older is not asked about.
func (w *watcher) catchUpMessages(ctx context.Context, teams []*model.Team, sinceMs int64, fail failFunc) ([]catchItem, error) {
	s := w.s
	chans, err := listChannels(ctx, s, teams, true)
	if err != nil {
		return nil, fail("your channels", nil, err)
	}
	type found struct {
		tc    teamChannel
		posts []*model.Post
	}
	var all []found
	var ids []string
	for _, tc := range chans {
		if tc.ch.LastPostAt < sinceMs || !w.wanted(tc.ch.Id) {
			continue
		}
		pl, resp, err := s.Client.GetPostsSince(ctx, tc.ch.Id, sinceMs, false)
		if err != nil {
			if e := fail("channel "+cmp.Or(tc.ch.Name, tc.ch.Id), resp, err); e != nil {
				return nil, e
			}
			continue
		}
		var posts []*model.Post
		for _, p := range render.Sorted(pl) {
			if p.CreateAt >= sinceMs && !w.seenPosts.has(p.Id) {
				posts = append(posts, p)
			}
		}
		if len(posts) > watchCatchUpPosts {
			fmt.Fprintf(s.app.Err, "mm-cli: warning: channel %s has more than %d new messages; showing the newest %d\n",
				cmp.Or(tc.ch.Name, tc.ch.Id), watchCatchUpPosts, watchCatchUpPosts)
			posts = posts[len(posts)-watchCatchUpPosts:]
		}
		if len(posts) == 0 {
			continue
		}
		all = append(all, found{tc, posts})
		if tc.ch.Type == model.ChannelTypeDirect {
			ids = append(ids, directPeer(tc.ch, s.Server.UserID))
		}
		for _, p := range posts {
			ids = append(ids, render.UserIDs(p)...)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	names := s.usernames(ctx, ids)
	fallback := s.linkTeam(ctx)
	var items []catchItem
	for _, f := range all {
		label := channelDisplayName(f.tc.ch, s.Server.UserID, names)
		// The listing already says what a notification link needs.
		if _, ok := w.chans[f.tc.ch.Id]; !ok {
			w.chans[f.tc.ch.Id] = watchChannel{
				name:          reactionChannelName(f.tc.ch, s.Server.UserID, names),
				display:       label,
				reactionLabel: channelLabel(f.tc.ch, s.Server.UserID, names, true),
				team:          cmp.Or(f.tc.team, fallback),
				ownTeam:       f.tc.team,
			}
		}
		for _, p := range f.posts {
			items = append(items, catchItem{at: p.CreateAt, post: p, channel: label, names: names})
		}
	}
	return items, nil
}

// catchUpReactions finds the reactions of other people made since a time on
// your messages of the last watchReactionLookback.
func (w *watcher) catchUpReactions(ctx context.Context, teams []*model.Team, since time.Time, fail failFunc) ([]catchItem, error) {
	s := w.s
	sinceMs := since.UnixMilli()
	posts, err := s.ownPostsSince(ctx, teams, s.app.Now().Add(-watchReactionLookback))
	if err != nil {
		return nil, fail("your messages", nil, err)
	}
	type found struct {
		r *model.Reaction
		p *model.Post
	}
	var fs []found
	var ids []string
	for _, p := range posts {
		w.posts.put(postInfoOf(p))
		if p.Metadata == nil || !w.wanted(p.ChannelId) {
			continue
		}
		for _, r := range p.Metadata.Reactions {
			if r == nil || (r.UserId == s.Server.UserID && !w.includeOwn) || r.CreateAt < sinceMs || w.seenReactions.has(reactionKey(r)) {
				continue
			}
			fs = append(fs, found{r, p})
			ids = append(ids, r.UserId)
		}
	}
	if len(fs) == 0 {
		return nil, nil
	}
	names := s.usernames(ctx, ids)
	items := make([]catchItem, len(fs))
	for i, f := range fs {
		ch := w.channel(ctx, f.p.ChannelId)
		rec := render.ReactionFromModel(f.r, f.p, names, ch.name, ch.display, ch.reactionLabel, ch.team, s.Server.URL)
		items[i] = catchItem{at: f.r.CreateAt, reaction: &rec, rch: ch, source: f.r}
	}
	return items, nil
}

// printCatchUp writes the items through the live output, then the notice on
// stderr and the notifications: one by one, or one summary above
// watchSummaryAbove.
func (w *watcher) printCatchUp(ctx context.Context, since time.Time, items []catchItem) error {
	s := w.s
	if w.notify != nil {
		w.held = &[]heldNote{}
	}
	nPosts, nReactions := 0, 0
	var outErr error
	for _, it := range items {
		var err error
		if it.reaction != nil {
			if !w.seenReactions.add(reactionKey(it.source)) {
				continue
			}
			nReactions++
			err = w.emitReaction(ctx, *it.reaction, it.rch)
		} else {
			if !w.seenPosts.add(it.post.Id) {
				continue
			}
			nPosts++
			err = w.emitPost(ctx, it.post, it.names, it.channel, it.post.UserId == s.Server.UserID)
		}
		if err != nil {
			outErr = &watchOutputError{err: err}
			break
		}
	}
	held := w.held
	w.held = nil
	if held != nil {
		if len(*held) > watchSummaryAbove {
			w.notify(ctx, summaryNotice(*held, since.In(s.app.Location)))
		} else {
			for _, h := range *held {
				w.notify(ctx, h.n)
			}
		}
	}
	if outErr != nil {
		return outErr
	}
	if nPosts+nReactions > 0 {
		fmt.Fprintf(s.app.Err, "mm-cli: reconnected; caught up %d messages and %d reactions since %s\n",
			nPosts, nReactions, since.In(s.app.Location).Format(time.TimeOnly))
	}
	return nil
}

// summaryNotice is the single notification for a long catch-up. It has no
// link: it stands for many messages.
func summaryNotice(held []heldNote, since time.Time) Notification {
	posts, reactions := 0, 0
	for _, h := range held {
		if h.reaction {
			reactions++
		} else {
			posts++
		}
	}
	title := fmt.Sprintf("mm-cli: %d missed messages, %d missed reactions", posts, reactions)
	switch {
	case reactions == 0:
		title = fmt.Sprintf("mm-cli: %d missed messages", posts)
	case posts == 0:
		title = fmt.Sprintf("mm-cli: %d missed reactions", reactions)
	}
	return Notification{Title: title, Body: "Since " + since.Format(time.TimeOnly) + "; see the output of mm-cli watch."}
}
