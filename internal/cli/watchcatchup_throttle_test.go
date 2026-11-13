package cli

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// throttleRig runs a watch whose clock is moved by the test: the clock is set
// when a connection starts, so that every frame of it and the catch-up after
// it see one time. A wait of the catch-up timer (five minutes or more) blocks
// until release is closed; the pauses before a reconnect return at once.
type throttleRig struct {
	offset  atomic.Int64
	release chan struct{}
}

func (r *throttleRig) install(app *App) {
	r.release = make(chan struct{})
	app.Now = func() time.Time { return now.Add(time.Duration(r.offset.Load())) }
	app.Sleep = func(ctx context.Context, d time.Duration) error {
		if d < 5*time.Minute {
			return ctx.Err()
		}
		select {
		case <-r.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ws serves one connection per entry of at: it sets the clock to the entry,
// sends hello and drops, except the last connection, which stays open.
func (r *throttleRig) ws(t *testing.T, at []time.Duration) http.HandlerFunc {
	frames := make([][][]byte, len(at))
	for i := range frames {
		frames[i] = [][]byte{helloFrame}
	}
	inner := wsPerConn(t, "tok", frames)
	var mu sync.Mutex
	n := 0
	return func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		r.offset.Store(int64(at[min(n, len(at)-1)]))
		n++
		mu.Unlock()
		inner(w, req)
	}
}

// searchLater serves the search of your messages: the first call finds no
// reaction, later ones find one made at the given offset.
func searchLater(s *fakemm.Server, at time.Duration) {
	p1 := fakemm.ID("p1")
	plain := reactionPost(p1, townSq, 2*time.Hour)
	react := &model.Reaction{PostId: p1, UserId: reactCarol, EmojiName: "eyes", CreateAt: now.Add(at).UnixMilli()}
	with := reactionPost(p1, townSq, 2*time.Hour, react)
	serveReactions(s, plain)
	var calls atomic.Int32
	s.Handle(http.MethodPost, "/api/v4/posts/search", func(w http.ResponseWriter, _ *http.Request) {
		p := plain
		if calls.Add(1) > 1 {
			p = with
		}
		fakemm.WriteJSON(w, 200, &model.PostList{Order: []string{p1}, Posts: map[string]*model.Post{p1: p}})
	})
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq, 200, &model.Channel{Id: townSq, TeamId: teamID, Name: "town-square", Type: model.ChannelTypeOpen})
	s.JSON(http.MethodGet, "/api/v4/teams/"+teamID, 200, &model.Team{Id: teamID, Name: "myteam"})
}

func catchUps(s *fakemm.Server) int {
	return s.Count(http.MethodGet, "/api/v4/users/"+meID+"/teams")
}

// TestWatchReactionCatchUpIsLimited: reconnects within the limit search once;
// the skipped catch-up runs when the limit expires, without another
// reconnect, and finds a reaction made during the gap on an old message.
func TestWatchReactionCatchUpIsLimited(t *testing.T) {
	app, s := newTestApp(t)
	var rig throttleRig
	rig.install(app)
	searchLater(s, 2*time.Minute+30*time.Second)
	s.Handle(http.MethodGet, "/api/v4/websocket", rig.ws(t, []time.Duration{0, 2 * time.Minute, 3 * time.Minute, 3 * time.Minute}))
	out, _, stop := startWatch(t, app, "--reactions")
	waitFor(t, "three catch-ups", func() bool { return catchUps(s) == 3 })
	if n := s.Count(http.MethodPost, "/api/v4/posts/search"); n != 1 {
		t.Errorf("searches = %d after three reconnects within the limit, want 1", n)
	}
	rig.offset.Store(int64(12 * time.Minute))
	close(rig.release)
	waitFor(t, "the reaction", func() bool { return strings.Contains(out.String(), ":eyes:") })
	stop()
	if n := s.Count(http.MethodPost, "/api/v4/posts/search"); n != 2 {
		t.Errorf("searches = %d, want 2", n)
	}
	if n := strings.Count(out.String(), ":eyes:"); n != 1 {
		t.Errorf(":eyes: printed %d times:\n%s", n, out.String())
	}
}

// TestWatchReactionCatchUpKeepsBoundary: a reconnect after the limit searches
// again and covers the gap of the skipped one, not only its own.
func TestWatchReactionCatchUpKeepsBoundary(t *testing.T) {
	app, s := newTestApp(t)
	var rig throttleRig
	rig.install(app)
	// Made after the skipped reconnect's boundary (1m), before the last one's (2m).
	searchLater(s, 90*time.Second)
	s.Handle(http.MethodGet, "/api/v4/websocket", rig.ws(t, []time.Duration{0, 2 * time.Minute, 3 * time.Minute, 13 * time.Minute}))
	out, _, stop := startWatch(t, app, "--reactions")
	waitFor(t, "the reaction", func() bool { return strings.Contains(out.String(), ":eyes:") })
	stop()
	if n := s.Count(http.MethodPost, "/api/v4/posts/search"); n != 2 {
		t.Errorf("searches = %d, want 2 (the second reconnect skipped)", n)
	}
}

// TestWatchMessageCatchUpIgnoresReactionLimit: the messages of every
// reconnect are fetched although the search for reactions is skipped.
func TestWatchMessageCatchUpIgnoresReactionLimit(t *testing.T) {
	app, s := newTestApp(t)
	noSleep(app)
	serveReactions(s, reactionPost(fakemm.ID("p1"), townSq, 2*time.Hour))
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/channels", 200, []*model.Channel{newestChannel(townSq, "town-square", now)})
	s.JSON(http.MethodGet, "/api/v4/channels/"+townSq+"/posts", 200, &model.PostList{Posts: map[string]*model.Post{}})
	s.Handle(http.MethodGet, "/api/v4/websocket", wsPerConn(t, "tok", [][][]byte{{helloFrame}, {helloFrame}, nil}))
	_, _, stop := startWatch(t, app, "--messages", "--reactions")
	waitFor(t, "two reconnects", func() bool { return s.Count(http.MethodGet, "/api/v4/channels/"+townSq+"/posts") == 2 })
	stop()
	if n := s.Count(http.MethodPost, "/api/v4/posts/search"); n != 1 {
		t.Errorf("searches = %d, want 1", n)
	}
}
