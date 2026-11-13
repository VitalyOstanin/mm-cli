package cli

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsCloseOnChallenge accepts the upgrade and drops the socket on the
// authentication challenge without a reply, as a server does for a bad token.
func wsCloseOnChallenge(conns *atomic.Int32) http.HandlerFunc {
	up := websocket.Upgrader{}
	return func(w http.ResponseWriter, r *http.Request) {
		conns.Add(1)
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_, _, _ = c.ReadMessage()
		_ = c.Close()
	}
}

func countingMe(status int, calls *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"id":"u1","username":"alice"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"api.context.session_expired.app_error","message":"expired","status_code":401}`))
	}
}

func TestWatchDropBeforeAuthWithRejectedSessionExits3(t *testing.T) {
	app, s := newTestApp(t)
	var conns, me atomic.Int32
	s.Handle(http.MethodGet, "/api/v4/websocket", wsCloseOnChallenge(&conns))
	s.Handle(http.MethodGet, "/api/v4/users/me", countingMe(http.StatusUnauthorized, &me))
	app.Sleep = func(context.Context, time.Duration) error {
		t.Error("watch retried after the session was rejected")
		return context.Canceled
	}
	r := run(app, "watch")
	mustCode(t, r, 3)
	if conns.Load() != 1 || me.Load() != 1 {
		t.Errorf("connections %d, session checks %d; want 1 and 1", conns.Load(), me.Load())
	}
}

func TestWatchDropBeforeAuthWithValidSessionReconnects(t *testing.T) {
	app, s := newTestApp(t)
	var conns, me atomic.Int32
	s.Handle(http.MethodGet, "/api/v4/websocket", wsCloseOnChallenge(&conns))
	s.Handle(http.MethodGet, "/api/v4/users/me", countingMe(http.StatusOK, &me))
	var mu sync.Mutex
	pauses := 0
	app.Sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		pauses++
		mu.Unlock()
		return ctx.Err()
	}
	_, _, stop := startWatch(t, app)
	waitFor(t, "two reconnects", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pauses >= 2
	})
	if code := stop(); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if me.Load() < 2 {
		t.Errorf("session checks %d, want one per dropped connection", me.Load())
	}
}

func TestWatchDropAfterAuthDoesNotCheckSession(t *testing.T) {
	app, s := newTestApp(t)
	var mu sync.Mutex
	conns := 0
	var me atomic.Int32
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", nil, false, &conns, &mu))
	s.Handle(http.MethodGet, "/api/v4/users/me", countingMe(http.StatusUnauthorized, &me))
	pauses := 0
	app.Sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		pauses++
		mu.Unlock()
		return ctx.Err()
	}
	_, _, stop := startWatch(t, app)
	waitFor(t, "two reconnects", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return pauses >= 2
	})
	stop()
	if me.Load() != 0 {
		t.Errorf("session checked %d times on an authenticated connection", me.Load())
	}
}
