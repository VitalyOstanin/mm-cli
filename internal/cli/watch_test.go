package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/client"
	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// syncBuffer is a bytes.Buffer safe for a reader in another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func postedEvent(t *testing.T, channelID, channelName, sender, id, text string) []byte {
	t.Helper()
	post, err := json.Marshal(&model.Post{Id: id, ChannelId: channelID, UserId: fakemm.ID("bob"), Message: text, CreateAt: 1_700_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := json.Marshal(map[string]any{
		"event": "posted",
		"seq":   1,
		"data": map[string]any{
			"post": string(post), "sender_name": sender,
			"channel_display_name": channelName, "channel_name": channelName,
		},
		"broadcast": map[string]any{"channel_id": channelID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// wsHandler answers the authentication challenge, then sends the frames and,
// when hold is set, keeps the connection open until the client goes away.
func wsHandler(t *testing.T, token string, frames [][]byte, hold bool, conns *int, mu *sync.Mutex) http.HandlerFunc {
	up := websocket.Upgrader{}
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*conns++
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
		if status != model.StatusOk {
			// Keep the socket open so that the client sees the reply first.
			_, _, _ = c.ReadMessage()
			return
		}
		for _, f := range frames {
			_ = c.WriteMessage(websocket.TextMessage, f)
		}
		if hold {
			_, _, _ = c.ReadMessage()
		}
	}
}

func startWatch(t *testing.T, app *App, args ...string) (*syncBuffer, *syncBuffer, func() int) {
	t.Helper()
	out, errb := &syncBuffer{}, &syncBuffer{}
	app.Out, app.Err = out, errb
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Execute(ctx, app, append([]string{"watch"}, args...)) }()
	stop := func() int {
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(10 * time.Second):
			t.Fatal("watch did not stop")
			return -1
		}
	}
	t.Cleanup(func() { cancel() })
	return out, errb, stop
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWatchFiltersChannelsAndPrintsSender(t *testing.T) {
	app, s := newTestApp(t)
	town := fakemm.ID("town")
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", http.StatusOK,
		&model.Channel{Id: town, Name: "town-square", Type: model.ChannelTypeOpen})
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", [][]byte{
		postedEvent(t, fakemm.ID("other"), "Other", "@carol", fakemm.ID("p1"), "not for us"),
		postedEvent(t, town, "Town Square", "@bob", fakemm.ID("p2"), "hello town"),
	}, true, &conns, &mu))

	out, _, stop := startWatch(t, app, "town-square")
	waitFor(t, "the post", func() bool { return strings.Contains(out.String(), "hello town") })
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	got := out.String()
	if strings.Contains(got, "not for us") {
		t.Errorf("message from an unwatched channel printed:\n%s", got)
	}
	if !strings.Contains(got, "[Town Square] ") || !strings.Contains(got, "@bob") {
		t.Errorf("channel or sender missing:\n%s", got)
	}
	if n := s.Count(http.MethodPost, "/api/v4/users/ids"); n != 0 {
		t.Errorf("%d user lookups; the sender name comes with the event", n)
	}
}

func TestWatchJSONLines(t *testing.T) {
	app, s := newTestApp(t)
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", [][]byte{
		postedEvent(t, fakemm.ID("a"), "A", "@bob", fakemm.ID("p1"), "one"),
		postedEvent(t, fakemm.ID("b"), "B", "@bob", fakemm.ID("p2"), "two"),
	}, true, &conns, &mu))

	out, _, stop := startWatch(t, app, "--json")
	waitFor(t, "two lines", func() bool { return strings.Count(out.String(), "\n") >= 2 })
	stop()
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var p watchedPost
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		if p.User.Username != "bob" || p.Channel == "" || p.ID == "" {
			t.Errorf("incomplete object: %+v", p)
		}
	}
}

func TestWatchReconnectsWithGrowingPause(t *testing.T) {
	app, s := newTestApp(t)
	var mu sync.Mutex
	conns := 0
	// The server closes every connection right after authentication.
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", nil, false, &conns, &mu))
	var pauses []time.Duration
	app.Sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		pauses = append(pauses, d)
		mu.Unlock()
		return ctx.Err()
	}

	_, errb, stop := startWatch(t, app)
	waitFor(t, "four reconnects", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(pauses) >= 4
	})
	stop()
	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i, w := range want {
		if pauses[i] != w {
			t.Fatalf("pauses = %v, want prefix %v", pauses, want)
		}
	}
	if !strings.Contains(errb.String(), "reconnecting in 1s") {
		t.Errorf("stderr: %s", errb.String())
	}
}

func TestWatchRejectedSessionIsAuthError(t *testing.T) {
	app, s := newTestApp(t)
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "another-token", nil, true, &conns, &mu))
	app.Sleep = func(context.Context, time.Duration) error {
		t.Error("watch retried after the session was rejected")
		return nil
	}
	r := run(app, "watch")
	mustCode(t, r, 3)
}

func TestWatchRefreshesStaleCachedChannel(t *testing.T) {
	app, s := newTestApp(t)
	stale, town := fakemm.ID("stale"), fakemm.ID("town")
	cacheChannel(t, app, "myteam/town-square", stale)
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", http.StatusOK,
		&model.Channel{Id: town, Name: "town-square", Type: model.ChannelTypeOpen})
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", [][]byte{
		postedEvent(t, town, "Town Square", "@bob", fakemm.ID("p1"), "hello town"),
	}, true, &conns, &mu))

	out, _, stop := startWatch(t, app, "town-square")
	waitFor(t, "the post", func() bool { return strings.Contains(out.String(), "hello town") })
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if n := s.Count(http.MethodGet, "/api/v4/channels/"+stale); n != 1 {
		t.Errorf("%d checks of the cached channel, want 1", n)
	}
}

func TestWatchUnknownChannelIDFails(t *testing.T) {
	app, s := newTestApp(t)
	app.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", nil, false, &conns, &mu))
	r := run(app, "watch", fakemm.ID("missing"))
	mustCode(t, r, clierr.CodeNotFound)
	mu.Lock()
	defer mu.Unlock()
	if conns != 0 {
		t.Errorf("%d connections; watch must stop before listening", conns)
	}
}

func TestWatchFreshAddressNeedsNoCheck(t *testing.T) {
	app, s := newTestApp(t)
	town := fakemm.ID("town")
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", http.StatusOK,
		&model.Channel{Id: town, Name: "town-square", Type: model.ChannelTypeOpen})
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", nil, true, &conns, &mu))
	_, _, stop := startWatch(t, app, "town-square")
	waitFor(t, "the connection", func() bool { mu.Lock(); defer mu.Unlock(); return conns > 0 })
	stop()
	if n := s.Count(http.MethodGet, "/api/v4/channels/"+town); n != 0 {
		t.Errorf("%d extra checks of a just resolved channel", n)
	}
}

// failingWriter rejects every write with err.
type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

func TestWatchStopsOnOutputError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"disk full", syscall.ENOSPC, clierr.CodeError},
		{"closed pipe", syscall.EPIPE, clierr.CodeOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, s := newTestApp(t)
			var mu sync.Mutex
			conns := 0
			s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", [][]byte{
				postedEvent(t, fakemm.ID("a"), "A", "@bob", fakemm.ID("p1"), "one"),
			}, true, &conns, &mu))
			app.Sleep = func(context.Context, time.Duration) error {
				t.Error("watch reconnected after an output error")
				return context.Canceled
			}
			errb := &syncBuffer{}
			app.Out, app.Err = failingWriter{err: fmt.Errorf("write /dev/stdout: %w", tc.err)}, errb
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			code := Execute(ctx, app, []string{"watch"})
			if ctx.Err() != nil {
				t.Fatal("watch did not stop on its own")
			}
			if code != tc.code {
				t.Errorf("exit code %d, want %d; stderr: %s", code, tc.code, errb.String())
			}
			if strings.Contains(errb.String(), "reconnecting") {
				t.Errorf("stderr: %s", errb.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if conns != 1 {
				t.Errorf("%d connections, want 1", conns)
			}
		})
	}
}

// fakeClock is a clock that only moves when the code under test sleeps.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.t = c.t.Add(d)
	}
	return nil
}

// handshakeTimes records when each WebSocket handshake arrives; the first
// `throttled` ones are refused with 429 and Retry-After.
func handshakeTimes(t *testing.T, clock *fakeClock, throttled int, retryAfter string) (http.HandlerFunc, func() []time.Time) {
	var mu sync.Mutex
	var times []time.Time
	conns := 0
	ws := wsHandler(t, "tok", nil, true, &conns, &mu)
	h := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, clock.Now())
		n := len(times)
		mu.Unlock()
		if n <= throttled {
			w.Header().Set("Retry-After", retryAfter)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		ws(w, r)
	}
	get := func() []time.Time {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Time(nil), times...)
	}
	return h, get
}

func TestWatchHandshake429WaitsRetryAfter(t *testing.T) {
	app, s := newTestApp(t)
	clock := &fakeClock{t: now}
	app.Now, app.Sleep = clock.Now, clock.Sleep
	h, times := handshakeTimes(t, clock, 1, "30")
	s.Handle(http.MethodGet, "/api/v4/websocket", h)

	_, errb, stop := startWatch(t, app)
	waitFor(t, "the second handshake", func() bool { return len(times()) >= 2 })
	stop()
	got := times()
	if d := got[1].Sub(got[0]); d < 30*time.Second {
		t.Errorf("second handshake %s after a 429 with Retry-After: 30", d)
	}
	if !strings.Contains(errb.String(), "reconnecting in 30s") {
		t.Errorf("stderr: %s", errb.String())
	}
	state, err := os.ReadFile(filepath.Join(filepath.Dir(app.Store.CachePath("test")), client.StateFile))
	if err != nil || !strings.Contains(string(state), "not_before") {
		t.Errorf("the ban is not shared with other runs: %q, %v", state, err)
	}
}

func TestWatchHonoursSavedBan(t *testing.T) {
	app, s := newTestApp(t)
	clock := &fakeClock{t: now}
	app.Now, app.Sleep = clock.Now, clock.Sleep
	path := filepath.Join(filepath.Dir(app.Store.CachePath("test")), client.StateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	ban := now.Add(40 * time.Second)
	if err := os.WriteFile(path, []byte(`{"not_before":"`+ban.Format(time.RFC3339Nano)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h, times := handshakeTimes(t, clock, 0, "")
	s.Handle(http.MethodGet, "/api/v4/websocket", h)

	_, _, stop := startWatch(t, app)
	waitFor(t, "the handshake", func() bool { return len(times()) >= 1 })
	stop()
	if got := times()[0]; got.Before(ban) {
		t.Errorf("connected at %s, before the saved ban ends at %s", got, ban)
	}
}
