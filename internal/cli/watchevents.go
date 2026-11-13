package cli

import (
	"cmp"
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/render"
)

// watchPostCache is how many messages watch remembers the author of.
const watchPostCache = 2000

// postFetchTimeout bounds the single GET /posts/<id> made while reading events.
const postFetchTimeout = 10 * time.Second

// postInfo is what watch needs to know about a message that got a reaction.
// An empty author marks a message that could not be read.
type postInfo struct {
	id, author, rootID, message, channelID string
}

// postInfoOf is the cache entry for a post, with its author. A caller that
// must not record the author clears it afterwards.
func postInfoOf(p *model.Post) postInfo {
	return postInfo{id: p.Id, author: p.UserId, rootID: p.RootId, message: p.Message, channelID: p.ChannelId}
}

// lru is a bounded least-recently-used map. It backs both the message cache
// and the seen-event sets, which differ only in what they store.
type lru[V any] struct {
	max   int
	order *list.List
	items map[string]*list.Element
}

type lruEntry[V any] struct {
	key string
	val V
}

func newLRU[V any](max int) *lru[V] {
	return &lru[V]{max: max, order: list.New(), items: map[string]*list.Element{}}
}

// get returns the value and marks it as recently used.
func (c *lru[V]) get(key string) (V, bool) {
	e, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(e)
	return e.Value.(lruEntry[V]).val, true
}

// has reports presence without touching the order.
func (c *lru[V]) has(key string) bool {
	_, ok := c.items[key]
	return ok
}

// put stores or replaces a value and evicts the least recently used entry
// when the bound is exceeded.
func (c *lru[V]) put(key string, val V) {
	if e, ok := c.items[key]; ok {
		e.Value = lruEntry[V]{key, val}
		c.order.MoveToFront(e)
		return
	}
	c.items[key] = c.order.PushFront(lruEntry[V]{key, val})
	if c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(lruEntry[V]).key)
	}
}

// add remembers key (with a zero value) and reports whether it was new; an
// existing key is only refreshed.
func (c *lru[V]) add(key string) bool {
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		return false
	}
	var zero V
	c.put(key, zero)
	return true
}

// postCache caches messages by id, so that a stream of reactions costs one
// request per message at most.
type postCache struct{ lru[postInfo] }

func newPostCache(max int) *postCache {
	return &postCache{lru[postInfo]{max: max, order: list.New(), items: map[string]*list.Element{}}}
}

func (c *postCache) put(p postInfo) { c.lru.put(p.id, p) }

// watchChannel is a channel as watch shows it and links to it. name is the
// address, display the name the app shows; reactionLabel is what the text of a
// reaction shows. team is the one for links, which for a direct or group
// channel is a stand-in; ownTeam is the real one, empty for those.
type watchChannel struct{ name, display, reactionLabel, team, ownTeam string }

// watcher is the state of one `watch` run, kept across reconnects.
type watcher struct {
	s                   *Session
	filter              map[string]bool
	truncate            int
	messages, reactions bool
	// includeOwn also shows and announces your own reactions and messages, to
	// check notifications without a second account.
	includeOwn bool
	notify     func(context.Context, Notification)
	emoji      *emojiImages
	posts      *postCache
	chans      map[string]watchChannel
	teams      map[string]string
	// seenPosts and seenReactions remember what was printed, so that a
	// catch-up after a reconnect and the live stream never print one event
	// twice.
	seenPosts, seenReactions *seenSet
	// alive is when the last frame (message, event or ping) arrived; connected
	// is set after the first successful authentication, which needs no
	// catch-up. missedSince is the start of a catch-up that failed halfway, to
	// be repeated after the next reconnect.
	alive       time.Time
	connected   bool
	missedSince time.Time
	// reactionsSince is the start of the reactions not caught up yet: their
	// catch-up was skipped (see watchReactionCatchUpEvery) or failed. It is
	// separate from missedSince because the messages are caught up on every
	// reconnect. reactionsAt is when the last search for them started.
	reactionsSince, reactionsAt time.Time
	// held collects notifications while a catch-up runs, so that a long gap
	// gives one summary instead of a burst.
	held *[]heldNote
}

// heldNote is a notification kept back during a catch-up.
type heldNote struct {
	n        Notification
	reaction bool
}

// deliver shows a notification now, or keeps it back during a catch-up.
func (w *watcher) deliver(ctx context.Context, n Notification, reaction bool) {
	if w.held != nil {
		*w.held = append(*w.held, heldNote{n, reaction})
		return
	}
	w.notify(ctx, n)
}

// seenSet is a bounded least-recently-used set of strings.
type seenSet = lru[struct{}]

func newSeenSet(max int) *seenSet { return newLRU[struct{}](max) }

// reactionKey identifies a reaction; a reaction removed and added again is
// treated as printed already.
func reactionKey(r *model.Reaction) string {
	return r.PostId + "/" + r.UserId + "/" + r.EmojiName
}

func (w *watcher) wanted(channelID string) bool {
	return len(w.filter) == 0 || w.filter[channelID]
}

// validEventIDs reports whether every id of an event is a well-formed
// Mattermost id. Ids go into API paths and into the link a notification
// opens, so an event with anything else is dropped before either.
func validEventIDs(ids ...string) bool {
	for _, id := range ids {
		if !model.IsValidId(id) {
			return false
		}
	}
	return true
}

// posted handles a `posted` event. The event carries the sender's name, so no
// request is made per message; the channel's team is looked up (once per
// channel) only for a notification's link.
func (w *watcher) posted(ctx context.Context, ev *model.WebSocketEvent) error {
	s := w.s
	data := ev.GetData()
	raw, _ := data["post"].(string)
	var p model.Post
	if err := json.Unmarshal([]byte(raw), &p); err != nil || !validEventIDs(p.Id, p.ChannelId, p.UserId) {
		return nil
	}
	mine := p.UserId == s.Server.UserID
	if mine {
		// Known as mine without asking the server.
		w.posts.put(postInfoOf(&p))
	}
	if !w.messages || !w.wanted(p.ChannelId) {
		return nil
	}
	names := map[string]string{}
	sender, _ := data["sender_name"].(string)
	if sender != "" {
		names[p.UserId] = strings.TrimPrefix(sender, "@")
	}
	channel, _ := data["channel_display_name"].(string)
	if channel == "" {
		channel, _ = data["channel_name"].(string)
	}
	if !w.seenPosts.add(p.Id) {
		return nil
	}
	return w.emitPost(ctx, &p, names, channel, mine)
}

// printEvent writes one watch event: a JSON line with --json, otherwise the
// channel in brackets followed by the text form. Posts and reactions share it
// so that both stay in one format.
func (w *watcher) printEvent(jsonValue any, channel string, renderText func(io.Writer) error) error {
	out := w.s.app.Out
	if w.s.app.json {
		b, err := json.Marshal(jsonValue)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", b)
		return err
	}
	if _, err := fmt.Fprintf(out, "[%s] ", channel); err != nil {
		return err
	}
	if err := renderText(out); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out)
	return err
}

// emitPost prints a message and, with --notify, announces it unless it is
// yours. Live events and the catch-up share it.
func (w *watcher) emitPost(ctx context.Context, p *model.Post, names map[string]string, channel string, mine bool) error {
	s := w.s
	rp := render.FromModel(p, names)
	// The channel comes from the cache the reactions use, so a channel seen
	// once costs no request. Text output keeps the label it was given.
	var line any
	if s.app.json {
		ch := w.channel(ctx, p.ChannelId)
		line = watchedPost{Type: "post", Post: rp, Channel: ch.name, ChannelDisplayName: ch.display, Team: ch.ownTeam}
	}
	if err := w.printEvent(line, channel, func(out io.Writer) error {
		return render.PostsText(out, []render.Post{rp}, s.app.Location, w.truncate)
	}); err != nil {
		return err
	}
	if w.notify != nil && (!mine || w.includeOwn) {
		ch := w.channel(ctx, p.ChannelId)
		_, link := render.PostLinks(s.Server.URL, ch.team, p.Id)
		w.deliver(ctx, Notification{
			Title:   fmt.Sprintf("%s in %s", cmp.Or(names[p.UserId], p.UserId), channel),
			Body:    render.Snippet(p.Message, notifyBodyLength),
			AppLink: link,
		}, false)
	}
	return nil
}

// reactionAdded handles a `reaction_added` event: it keeps reactions of other
// people on your messages (yours too with --include-own). Your own reactions
// are otherwise dropped before any request; the author of the message comes
// from the cache or one GET /posts/<id>.
func (w *watcher) reactionAdded(ctx context.Context, ev *model.WebSocketEvent) error {
	s := w.s
	raw, _ := ev.GetData()["reaction"].(string)
	var r model.Reaction
	if err := json.Unmarshal([]byte(raw), &r); err != nil || !validEventIDs(r.PostId, r.UserId) || (r.UserId == s.Server.UserID && !w.includeOwn) {
		return nil
	}
	// Reaction events are sent to a channel's members: the channel is known
	// from the broadcast, before the message is fetched. A frame without a
	// broadcast is decoded to a nil one: the channel is then checked after the
	// message is known.
	if b := ev.GetBroadcast(); b != nil && b.ChannelId != "" && !w.wanted(b.ChannelId) {
		return nil
	}
	info, ok := w.posts.get(r.PostId)
	if !ok {
		// One request must not outlast the ping deadline of the read loop.
		gctx, cancel := context.WithTimeout(ctx, postFetchTimeout)
		p, resp, err := s.Client.GetPost(gctx, r.PostId, "")
		cancel()
		if err != nil {
			// 403 and 404 mean a message we cannot see: remember that, so
			// that more reactions on it cost no request. Other failures
			// (including a stop) skip this event only and are retried. None
			// of them is a problem of the connection.
			if resp != nil && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound) {
				w.posts.put(postInfo{id: r.PostId})
			}
			return nil
		}
		info = postInfoOf(p)
		if p.UserId != s.Server.UserID {
			info.author = ""
		}
		w.posts.put(info)
	}
	if info.author != s.Server.UserID || !w.wanted(info.channelID) {
		return nil
	}
	if !w.seenReactions.add(reactionKey(&r)) {
		return nil
	}
	names := s.usernames(ctx, []string{r.UserId})
	ch := w.channel(ctx, info.channelID)
	rec := render.ReactionFromModel(&r, &model.Post{Id: info.id, RootId: info.rootID, ChannelId: info.channelID, Message: info.message},
		names, ch.name, ch.display, ch.reactionLabel, ch.team, s.Server.URL)
	return w.emitReaction(ctx, rec, ch)
}

// emitReaction prints a reaction and, with --notify, announces it. Live
// events and the catch-up share it.
func (w *watcher) emitReaction(ctx context.Context, rec render.Reaction, ch watchChannel) error {
	s := w.s
	// The text shows the label, not the address of the channel.
	rec.ChannelLabel = ch.reactionLabel
	rec.ChannelDisplayName = ch.display
	line := watchedReaction{Type: "reaction", Reaction: rec, Team: ch.ownTeam}
	if err := w.printEvent(line, ch.reactionLabel, func(out io.Writer) error {
		return render.ReactionsText(out, []render.Reaction{rec}, s.app.Location, cmp.Or(w.truncate, reactionTruncate))
	}); err != nil {
		return err
	}
	if w.notify != nil {
		// A standard emoji is shown as its character; only a custom one needs
		// the server, for its picture.
		emoji, image := render.EmojiUnicode(rec.Emoji), ""
		if emoji == "" {
			emoji = ":" + rec.Emoji + ":"
			image = w.emoji.path(ctx, rec.Emoji)
		}
		w.deliver(ctx, Notification{
			Title:   fmt.Sprintf("%s reacted %s", cmp.Or(rec.User, rec.UserID), emoji),
			Body:    ch.reactionLabel + ": " + render.Snippet(rec.Message, notifyBodyLength),
			AppLink: rec.AppLink,
			Image:   image,
		}, true)
	}
	return nil
}

// channel returns the name and the team of a channel for output and links,
// asking the server once per channel for the session. Direct and group
// channels belong to no team; the selected team stands in, as the app does. A
// failed lookup shows the id.
func (w *watcher) channel(ctx context.Context, id string) watchChannel {
	if c, ok := w.chans[id]; ok {
		return c
	}
	s := w.s
	c := watchChannel{name: id, display: id, reactionLabel: id}
	ch, resp, err := s.Client.GetChannel(ctx, id)
	if err != nil {
		c.team = s.linkTeam(ctx)
		// Not cached after a stop or a passing failure.
		if ctx.Err() != nil || resp == nil || (resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound) {
			return c
		}
		w.chans[id] = c
		return c
	}
	var peers []string
	if ch.Type == model.ChannelTypeDirect {
		peers = append(peers, directPeer(ch, s.Server.UserID))
	}
	names, namesErr := s.usernamesErr(ctx, peers)
	c.name = reactionChannelName(ch, s.Server.UserID, names)
	c.display = channelDisplayName(ch, s.Server.UserID, names)
	c.reactionLabel = channelLabel(ch, s.Server.UserID, names, true)
	// A failed name lookup shows the id for this event only, as a failed
	// team lookup below does.
	cacheable := namesErr == nil
	if ch.TeamId != "" {
		if name, ok := w.teams[ch.TeamId]; ok {
			c.team = name
		} else if t, resp, err := s.Client.GetTeam(ctx, ch.TeamId, ""); err == nil {
			w.teams[ch.TeamId] = t.Name
			c.team = t.Name
		} else if ctx.Err() != nil || resp == nil || (resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound) {
			// A passing failure shows the fallback for this event only; caching
			// it would pin the wrong team for the whole session.
			cacheable = false
		}
	}
	c.ownTeam = c.team
	if c.team == "" {
		c.team = s.linkTeam(ctx)
	}
	if cacheable {
		w.chans[id] = c
	}
	return c
}
