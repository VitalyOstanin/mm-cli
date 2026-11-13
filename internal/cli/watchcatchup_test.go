package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// wsPerConn authenticates every connection and sends the frames of the n-th
// one to it. A connection other than the last is closed after its frames (a
// drop); the last is held open.
func wsPerConn(t *testing.T, token string, frames [][][]byte) http.HandlerFunc {
	up := websocket.Upgrader{}
	var mu sync.Mutex
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = c.Close() }()
		var challenge struct {
			Seq  int64          `json:"seq"`
			Data map[string]any `json:"data"`
		}
		if err := c.ReadJSON(&challenge); err != nil {
			return
		}
		status := model.StatusOk
		if challenge.Data["token"] != token {
			status = model.StatusFail
		}
		_ = c.WriteJSON(map[string]any{"status": status, "seq_reply": challenge.Seq})
		if i < len(frames) {
			for _, f := range frames[i] {
				_ = c.WriteMessage(websocket.TextMessage, f)
			}
		}
		if i >= len(frames)-1 {
			_, _, _ = c.ReadMessage()
		}
	}
}

// catchUpServer serves the lists a catch-up reads: your team, your channels
// and the new messages of each channel.
func catchUpServer(s *fakemm.Server, chans []*model.Channel, posts map[string][]*model.Post) {
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: teamID, Name: "myteam"}})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, chans)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}, {Id: reactCarol, Username: "carol"}})
	for id, ps := range posts {
		pl := &model.PostList{Posts: map[string]*model.Post{}}
		for _, p := range ps {
			pl.Order = append(pl.Order, p.Id)
			pl.Posts[p.Id] = p
		}
		s.JSON(http.MethodGet, "/api/v4/channels/"+id+"/posts", 200, pl)
	}
}

func missed(id, channel, text string) *model.Post {
	return &model.Post{Id: id, ChannelId: channel, UserId: bobID, Message: text, CreateAt: now.UnixMilli()}
}

func newestChannel(id, name string, lastPost time.Time) *model.Channel {
	return &model.Channel{Id: id, TeamId: teamID, Name: name, DisplayName: name, Type: model.ChannelTypeOpen, LastPostAt: lastPost.UnixMilli()}
}

// stepClock is the clock of the retry tests: a failing catch-up step moves
// it ten minutes on, so a retry that restarts from "just now" instead of the
// failed point would miss what the failed one should have found.
type stepClock struct{ offset atomic.Int64 }

func (c *stepClock) install(app *App) {
	app.Now = func() time.Time { return now.Add(time.Duration(c.offset.Load())) }
}

func (c *stepClock) step() { c.offset.Add(int64(10 * time.Minute)) }

// helloFrame is a frame that is not an event: it only proves the connection
// is alive, which moves the "last sign of life" of the watcher.
var helloFrame = []byte(`{"event":"hello"}`)

func noSleep(app *App) {
	app.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
}

func TestWatchCatchUpPrintsMissedMessageOnce(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	catchUpServer(s, []*model.Channel{newestChannel(townSq, "town-square", now)},
		map[string][]*model.Post{townSq: {missed(fakemm.ID("p1"), townSq, "written during the drop")}})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{
		nil,
		{
			// The same message also arrives live after the reconnect.
			postedEvent(t, townSq, "town-square", "@bob", fakemm.ID("p1"), "written during the drop"),
			postedEvent(t, townSq, "town-square", "@bob", fakemm.ID("p2"), "sentinel"),
		},
	}))
	out, errb, stop := startWatch(t, app)
	waitFor(t, "the sentinel", func() bool { return strings.Contains(out.String(), "sentinel") })
	stop()
	if n := strings.Count(out.String(), "written during the drop"); n != 1 {
		t.Errorf("message printed %d times:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "[town-square] ") || !strings.Contains(out.String(), "@bob") {
		t.Errorf("channel or sender missing:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "mm-cli: reconnected; caught up 1 messages and 0 reactions since 11:59:00") {
		t.Errorf("stderr: %s", errb.String())
	}
	if n := s.Count(http.MethodGet, "/api/v4/users/"+meID+"/channels"); n != 1 {
		t.Errorf("%d channel lists, want 1", n)
	}
	if n := s.Count(http.MethodGet, "/api/v4/channels/"+townSq+"/posts"); n != 1 {
		t.Errorf("%d requests for the channel's messages, want 1", n)
	}
	if got := s.Last(http.MethodGet, "/api/v4/channels/"+townSq+"/posts").Query.Get("since"); got != "1772625540000" {
		t.Errorf("since = %s, want a minute before the last frame", got)
	}
}

func TestWatchNoCatchUpOnFirstConnection(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	catchUpServer(s, []*model.Channel{newestChannel(townSq, "town-square", now)}, nil)
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{
		{postedEvent(t, townSq, "town-square", "@bob", fakemm.ID("p1"), "live")},
	}))
	out, _, stop := startWatch(t, app)
	waitFor(t, "the post", func() bool { return strings.Contains(out.String(), "live") })
	stop()
	if n := s.Total() - s.Count(http.MethodGet, "/api/v4/websocket"); n != 0 {
		t.Errorf("%d requests on the first connection, want none", n)
	}
}

func TestWatchCatchUpSkipsQuietAndUnwatchedChannels(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	quiet, other := fakemm.ID("quiet"), fakemm.ID("other")
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", http.StatusOK,
		&model.Channel{Id: townSq, Name: "town-square", Type: model.ChannelTypeOpen})
	catchUpServer(s, []*model.Channel{
		newestChannel(townSq, "town-square", now),
		newestChannel(other, "other", now),
		newestChannel(quiet, "quiet", now.Add(-time.Hour)),
	}, map[string][]*model.Post{
		townSq: {missed(fakemm.ID("p1"), townSq, "in town")},
		other:  {missed(fakemm.ID("p2"), other, "elsewhere")},
		quiet:  {missed(fakemm.ID("p3"), quiet, "old news")},
	})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, nil}))
	out, _, stop := startWatch(t, app, "town-square")
	waitFor(t, "the catch-up", func() bool { return strings.Contains(out.String(), "in town") })
	stop()
	if strings.Contains(out.String(), "elsewhere") || strings.Contains(out.String(), "old news") {
		t.Errorf("output:\n%s", out.String())
	}
	for _, id := range []string{other, quiet} {
		if n := s.Count(http.MethodGet, "/api/v4/channels/"+id+"/posts"); n != 0 {
			t.Errorf("channel %s asked for messages %d times", id, n)
		}
	}
}

func TestWatchCatchUpWithoutNewPostAtAllAsksNoChannel(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	catchUpServer(s, []*model.Channel{newestChannel(townSq, "town-square", now.Add(-time.Hour))},
		map[string][]*model.Post{townSq: {missed(fakemm.ID("p1"), townSq, "old")}})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, {postedEvent(t, townSq, "town-square", "@bob", fakemm.ID("p9"), "live")}}))
	out, _, stop := startWatch(t, app)
	waitFor(t, "the live post", func() bool { return strings.Contains(out.String(), "live") })
	stop()
	if n := s.Count(http.MethodGet, "/api/v4/channels/"+townSq+"/posts"); n != 0 {
		t.Errorf("%d requests for messages of a channel without news", n)
	}
}

func TestWatchCatchUpReactionPrintedOnce(t *testing.T) {
	app, s := newTestApp(t)
	// The searches are ten minutes apart, which the limit lets through.
	var rig throttleRig
	rig.install(app)
	p1 := fakemm.ID("p1")
	serveReactions(s, reactionPost(p1, townSq, 2*time.Hour,
		reaction(p1, reactCarol, "eyes", -(9*time.Minute+30*time.Second)),
		reaction(p1, meID, "heart", time.Second),
		reaction(p1, reactCarol, "old", 3*time.Hour)))
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200, &model.Channel{Id: townSq, TeamId: teamID, Name: "town-square", Type: model.ChannelTypeOpen})
	s.JSON(http.MethodGet, "/api/v4/teams/"+teamID, 200, &model.Team{Id: teamID, Name: "myteam"})
	s.Handle(http.MethodGet, "/api/v4/websocket", rig.ws(t, []time.Duration{0, 10 * time.Minute, 20 * time.Minute}))
	out, _, stop := startWatch(t, app, "--reactions")
	waitFor(t, "the reaction", func() bool { return strings.Contains(out.String(), ":eyes:") })
	// The third connection repeats the catch-up; nothing may be printed again.
	waitFor(t, "two searches", func() bool { return s.Count(http.MethodPost, "/api/v4/posts/search") >= 2 })
	stop()
	got := out.String()
	if n := strings.Count(got, ":eyes:"); n != 1 {
		t.Errorf(":eyes: printed %d times:\n%s", n, got)
	}
	if strings.Contains(got, ":heart:") || strings.Contains(got, ":old:") {
		t.Errorf("own or old reaction printed:\n%s", got)
	}
	if !strings.Contains(got, "@carol") {
		t.Errorf("reactor missing:\n%s", got)
	}
}

func TestWatchCatchUpIncludeOwnReaction(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	got := notifyRecorder(app)
	p1 := fakemm.ID("p1")
	serveReactions(s, reactionPost(p1, townSq, 2*time.Hour,
		reaction(p1, meID, "heart", time.Second)))
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200, &model.Channel{Id: townSq, TeamId: teamID, Name: "town-square", Type: model.ChannelTypeOpen})
	s.JSON(http.MethodGet, "/api/v4/teams/"+teamID, 200, &model.Team{Id: teamID, Name: "myteam"})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, nil}))
	out, _, stop := startWatch(t, app, "--reactions", "--notify", "--include-own")
	waitFor(t, "the own reaction", func() bool { return strings.Contains(out.String(), ":heart:") })
	waitFor(t, "the notification", func() bool { return len(got()) > 0 })
	stop()
}

func notifyRecorder(app *App) func() []Notification {
	var mu sync.Mutex
	var got []Notification
	app.Notify = func(_ context.Context, n Notification) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, n)
	}
	return func() []Notification {
		mu.Lock()
		defer mu.Unlock()
		return append([]Notification(nil), got...)
	}
}

func manyMissed(n int) []*model.Post {
	var out []*model.Post
	for i := range n {
		out = append(out, missed(fakemm.ID("m"+string(rune('a'+i))), townSq, "missed "+string(rune('a'+i))))
	}
	return out
}

func TestWatchCatchUpSummaryNotification(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	got := notifyRecorder(app)
	catchUpServer(s, []*model.Channel{newestChannel(townSq, "town-square", now)}, map[string][]*model.Post{townSq: manyMissed(4)})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, nil}))
	out, _, stop := startWatch(t, app, "--notify")
	waitFor(t, "all messages", func() bool { return strings.Contains(out.String(), "missed d") })
	waitFor(t, "the notification", func() bool { return len(got()) > 0 })
	stop()
	ns := got()
	if len(ns) != 1 || ns[0].Title != "mm-cli: 4 missed messages" || ns[0].AppLink != "" {
		t.Errorf("notifications: %+v", ns)
	}
	if n := strings.Count(out.String(), "\n"); n < 4 {
		t.Errorf("every message must still be printed:\n%s", out.String())
	}
}

func TestWatchCatchUpFewNotificationsOneByOne(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	got := notifyRecorder(app)
	catchUpServer(s, []*model.Channel{newestChannel(townSq, "town-square", now)}, map[string][]*model.Post{townSq: manyMissed(3)})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, nil}))
	_, _, stop := startWatch(t, app, "--notify")
	waitFor(t, "three notifications", func() bool { return len(got()) >= 3 })
	stop()
	ns := got()
	if len(ns) != 3 || !strings.HasPrefix(ns[0].Title, "bob in ") || ns[0].AppLink == "" {
		t.Errorf("notifications: %+v", ns)
	}
}

func TestWatchCatchUpErrorDoesNotStopWatching(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	denied := fakemm.ID("denied")
	catchUpServer(s, []*model.Channel{
		newestChannel(denied, "denied", now),
		newestChannel(townSq, "town-square", now),
	}, map[string][]*model.Post{townSq: {missed(fakemm.ID("p1"), townSq, "readable")}})
	s.Handle(http.MethodGet, "/api/v4/channels/"+denied+"/posts", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusForbidden, "no access")
	})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{
		nil, {postedEvent(t, townSq, "town-square", "@bob", fakemm.ID("p2"), "still alive")},
	}))
	out, errb, stop := startWatch(t, app)
	waitFor(t, "the live post", func() bool { return strings.Contains(out.String(), "still alive") })
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out.String(), "readable") {
		t.Errorf("the readable channel was not caught up:\n%s", out.String())
	}
	if n := strings.Count(errb.String(), "mm-cli: catch-up: cannot read channel denied"); n != 1 {
		t.Errorf("stderr:\n%s", errb.String())
	}
}

func TestWatchCatchUpUnauthorizedIsAuthError(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	got := notifyRecorder(app)
	catchUpServer(s, []*model.Channel{newestChannel(townSq, "town-square", now)}, nil)
	s.Handle(http.MethodGet, "/api/v4/channels/"+townSq+"/posts", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusUnauthorized, "expired")
	})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, nil}))
	r := run(app, "watch", "--notify")
	mustCode(t, r, 3)
	if ns := got(); len(ns) != 1 || !strings.Contains(ns[0].Title, "sign in again") {
		t.Errorf("notifications: %+v", ns)
	}
}

// A catch-up that cannot list your channels or search your messages loses
// nothing: the next reconnect retries from the same point.
func TestWatchCatchUpRetriesAfterFailedChannelList(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	var clock stepClock
	clock.install(app)
	chans := []*model.Channel{newestChannel(townSq, "town-square", now)}
	catchUpServer(s, chans, map[string][]*model.Post{townSq: {missed(fakemm.ID("p1"), townSq, "lost then found")}})
	var calls atomic.Int32
	s.Handle(http.MethodGet, "/api/v4/users/"+meID+"/channels", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			clock.step()
			fakemm.AppError(w, http.StatusInternalServerError, "temporary")
			return
		}
		_ = json.NewEncoder(w).Encode(chans)
	})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, {helloFrame}, nil}))
	out, errb, stop := startWatch(t, app)
	waitFor(t, "the missed message", func() bool { return strings.Contains(out.String(), "lost then found") })
	stop()
	if !strings.Contains(errb.String(), "catch-up: cannot read your channels") {
		t.Errorf("failure not reported:\n%s", errb.String())
	}
}

func TestWatchCatchUpRetriesAfterFailedSearch(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	var clock stepClock
	clock.install(app)
	p1 := fakemm.ID("p1")
	serveReactions(s, reactionPost(p1, townSq, 2*time.Hour,
		reaction(p1, reactCarol, "eyes", time.Second)))
	pl := &model.PostList{Order: []string{p1}, Posts: map[string]*model.Post{p1: reactionPost(p1, townSq, 2*time.Hour,
		reaction(p1, reactCarol, "eyes", time.Second))}}
	var calls atomic.Int32
	s.Handle(http.MethodPost, "/api/v4/posts/search", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			clock.step()
			fakemm.AppError(w, http.StatusInternalServerError, "temporary")
			return
		}
		_ = json.NewEncoder(w).Encode(pl)
	})
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200, &model.Channel{Id: townSq, TeamId: teamID, Name: "town-square", Type: model.ChannelTypeOpen})
	s.JSON(http.MethodGet, "/api/v4/teams/"+teamID, 200, &model.Team{Id: teamID, Name: "myteam"})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{nil, {helloFrame}, nil}))
	out, errb, stop := startWatch(t, app, "--reactions")
	waitFor(t, "the reaction", func() bool { return strings.Contains(out.String(), ":eyes:") })
	stop()
	if !strings.Contains(errb.String(), "catch-up: cannot read your messages") {
		t.Errorf("failure not reported:\n%s", errb.String())
	}
}

// TestWatchSlowCatchUpKeepsAnsweringPings: a catch-up that is stuck on the
// server must not stop the connection from answering pings, or the server
// (and the read deadline) would drop it and the catch-up would start over. A
// live event that arrives meanwhile is shown after the catch-up output.
func TestWatchSlowCatchUpKeepsAnsweringPings(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	catchUpServer(s, []*model.Channel{newestChannel(townSq, "town-square", now)},
		map[string][]*model.Post{townSq: {missed(fakemm.ID("p1"), townSq, "from the gap")}})
	release := make(chan struct{})
	var inCatchUp atomic.Bool
	s.Handle(http.MethodGet, "/api/v4/channels/"+townSq+"/posts", func(w http.ResponseWriter, _ *http.Request) {
		inCatchUp.Store(true)
		select {
		case <-release:
		case <-time.After(10 * time.Second):
		}
		pl := &model.PostList{Order: []string{fakemm.ID("p1")}, Posts: map[string]*model.Post{
			fakemm.ID("p1"): missed(fakemm.ID("p1"), townSq, "from the gap"),
		}}
		_ = json.NewEncoder(w).Encode(pl)
	})
	ponged := make(chan struct{}, 1)
	up := websocket.Upgrader{}
	var conns atomic.Int32
	s.Handle(http.MethodGet, "/api/v4/websocket", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		var challenge struct {
			Seq int64 `json:"seq"`
		}
		if c.ReadJSON(&challenge) != nil {
			return
		}
		_ = c.WriteJSON(map[string]any{"status": model.StatusOk, "seq_reply": challenge.Seq})
		if conns.Add(1) == 1 {
			_ = c.WriteMessage(websocket.TextMessage, helloFrame)
			return // the drop
		}
		c.SetPongHandler(func(string) error {
			ponged <- struct{}{}
			return nil
		})
		_ = c.WriteMessage(websocket.TextMessage, postedEvent(t, townSq, "town-square", "@bob", fakemm.ID("p2"), "live meanwhile"))
		// Ping only once the catch-up is stuck on the server.
		for !inCatchUp.Load() {
			time.Sleep(5 * time.Millisecond)
		}
		_ = c.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	out, _, stop := startWatch(t, app)
	waitFor(t, "a pong while the catch-up is stuck", func() bool {
		select {
		case <-ponged:
			return true
		default:
			return false
		}
	})
	close(release)
	waitFor(t, "the live post", func() bool { return strings.Contains(out.String(), "live meanwhile") })
	stop()
	got := out.String()
	if i, j := strings.Index(got, "from the gap"), strings.Index(got, "live meanwhile"); i < 0 || j < i {
		t.Errorf("catch-up output must come before the live post:\n%s", got)
	}
}

// The server's "posts since" also returns posts deleted (or edited) during
// the outage, because deleting bumps UpdateAt; the deleted one must not be
// shown as a missed message.
func TestWatchCatchUpSkipsDeletedPost(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	gone := missed(fakemm.ID("p1"), townSq, "deleted during the drop")
	gone.DeleteAt = now.UnixMilli()
	catchUpServer(s, []*model.Channel{newestChannel(townSq, "town-square", now)},
		map[string][]*model.Post{townSq: {gone, missed(fakemm.ID("p2"), townSq, "kept message")}})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{
		nil,
		{postedEvent(t, townSq, "town-square", "@bob", fakemm.ID("p3"), "sentinel")},
	}))
	out, _, stop := startWatch(t, app)
	waitFor(t, "the sentinel", func() bool { return strings.Contains(out.String(), "sentinel") })
	stop()
	if strings.Contains(out.String(), "deleted during the drop") {
		t.Errorf("deleted post printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "kept message") {
		t.Errorf("live post missing:\n%s", out.String())
	}
}
