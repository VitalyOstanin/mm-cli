package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/godbus/dbus/v5"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

func reactionEvent(t *testing.T, channelID, postID, userID, emoji string) []byte {
	t.Helper()
	r, err := json.Marshal(&model.Reaction{PostId: postID, UserId: userID, EmojiName: emoji, CreateAt: 1_700_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := json.Marshal(map[string]any{
		"event":     "reaction_added",
		"seq":       1,
		"data":      map[string]any{"reaction": string(r)},
		"broadcast": map[string]any{"channel_id": channelID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// postedBy is a posted event with the given author.
func postedBy(t *testing.T, channelID, userID, sender, id, text string) []byte {
	t.Helper()
	post, err := json.Marshal(&model.Post{Id: id, ChannelId: channelID, UserId: userID, Message: text, CreateAt: 1_700_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := json.Marshal(map[string]any{
		"event": "posted", "seq": 1,
		"data": map[string]any{
			"post": string(post), "sender_name": sender,
			"channel_display_name": "Town Square", "channel_name": "town-square",
		},
		"broadcast": map[string]any{"channel_id": channelID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

type reactSetup struct {
	app  *App
	s    *fakemm.Server
	town string
}

// reactServer is a fake server with town-square in myteam, a message of alice
// (mine) and one of carol, and the given WebSocket frames.
func reactServer(t *testing.T, frames func(town string) [][]byte) reactSetup {
	t.Helper()
	app, s := newTestApp(t)
	town := fakemm.ID("town")
	ch := &model.Channel{Id: town, Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TeamId: fakemm.ID("team")}
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", http.StatusOK, ch)
	s.JSON(http.MethodGet, "/api/v4/channels/"+town, http.StatusOK, ch)
	s.JSON(http.MethodGet, "/api/v4/teams/"+fakemm.ID("team"), http.StatusOK, &model.Team{Id: fakemm.ID("team"), Name: "myteam"})
	s.JSON(http.MethodPost, "/api/v4/users/ids", http.StatusOK, []*model.User{{Id: bobID, Username: "bob"}})
	s.JSON(http.MethodGet, "/api/v4/posts/"+fakemm.ID("mine"), http.StatusOK,
		&model.Post{Id: fakemm.ID("mine"), UserId: meID, ChannelId: town, RootId: fakemm.ID("root"), Message: "my message"})
	s.JSON(http.MethodGet, "/api/v4/posts/"+fakemm.ID("carols"), http.StatusOK,
		&model.Post{Id: fakemm.ID("carols"), UserId: fakemm.ID("carol"), ChannelId: town, Message: "carol message"})
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "tok", frames(town), true, &conns, &mu))
	return reactSetup{app, s, town}
}

func TestWatchReactionOnMyPostText(t *testing.T) {
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{reactionEvent(t, town, fakemm.ID("mine"), bobID, "thumbsup")}
	})
	out, _, stop := startWatch(t, r.app, "--reactions")
	waitFor(t, "the reaction", func() bool { return strings.Contains(out.String(), "mattermost://") })
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	got := out.String()
	for _, want := range []string{"[town-square] ", "@bob", ":thumbsup:", "my message", "/myteam/pl/" + fakemm.ID("mine")} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if n := r.s.Count(http.MethodGet, "/api/v4/posts/"+fakemm.ID("mine")); n != 1 {
		t.Errorf("%d GET post, want 1", n)
	}
}

func TestWatchReactionJSONHasType(t *testing.T) {
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{reactionEvent(t, town, fakemm.ID("mine"), bobID, "heart")}
	})
	out, _, stop := startWatch(t, r.app, "--reactions", "--json")
	waitFor(t, "a line", func() bool { return strings.Contains(out.String(), "\n") })
	stop()
	var got map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"type": "reaction", "post_id": fakemm.ID("mine"), "root_id": fakemm.ID("root"), "user": "bob", "emoji": "heart",
		"channel": "town-square", "team": "myteam", "message": "my message",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if !strings.HasPrefix(got["app_link"], "mattermost://") || !strings.HasSuffix(got["app_link"], "/myteam/pl/"+fakemm.ID("mine")) {
		t.Errorf("app_link %q", got["app_link"])
	}
}

func TestWatchReactionsOnlyOnMyPostsByOthers(t *testing.T) {
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			// My own reaction: dropped before any request.
			reactionEvent(t, town, fakemm.ID("ownreact"), meID, "ok"),
			// Someone else's message.
			reactionEvent(t, town, fakemm.ID("carols"), bobID, "tada"),
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "sentinel"),
		}
	})
	out, _, stop := startWatch(t, r.app, "--reactions")
	waitFor(t, "the sentinel", func() bool { return strings.Contains(out.String(), ":sentinel:") })
	stop()
	if strings.Contains(out.String(), ":tada:") || strings.Contains(out.String(), ":ok:") {
		t.Errorf("foreign or own reaction printed:\n%s", out.String())
	}
	if n := r.s.Count(http.MethodGet, "/api/v4/posts/"+fakemm.ID("ownreact")); n != 0 {
		t.Errorf("own reaction made %d requests", n)
	}
	if n := r.s.Count(http.MethodGet, "/api/v4/posts/"+fakemm.ID("carols")); n != 1 {
		t.Errorf("%d GET for the foreign message, want 1", n)
	}
}

func TestWatchReactionPostCache(t *testing.T) {
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "one"),
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "two"),
			reactionEvent(t, town, fakemm.ID("carols"), bobID, "x"),
			reactionEvent(t, town, fakemm.ID("carols"), bobID, "y"),
			// Seen in `posted` as mine: no GET at all.
			postedBy(t, town, meID, "@alice", fakemm.ID("seen"), "written here"),
			reactionEvent(t, town, fakemm.ID("seen"), bobID, "three"),
		}
	})
	out, _, stop := startWatch(t, r.app, "--reactions")
	waitFor(t, "the last reaction", func() bool { return strings.Contains(out.String(), ":three:") })
	stop()
	if !strings.Contains(out.String(), ":two:") || !strings.Contains(out.String(), "written here") {
		t.Errorf("reactions missing:\n%s", out.String())
	}
	for id, want := range map[string]int{"mine": 1, "carols": 1, "seen": 0} {
		if n := r.s.Count(http.MethodGet, "/api/v4/posts/"+fakemm.ID(id)); n != want {
			t.Errorf("%s: %d GET, want %d", id, n, want)
		}
	}
	if n := r.s.Count(http.MethodGet, "/api/v4/channels/"+r.town); n != 1 {
		t.Errorf("%d channel lookups, want 1", n)
	}
	if n := r.s.Count(http.MethodPost, "/api/v4/users/ids"); n != 1 {
		t.Errorf("%d user lookups, want 1", n)
	}
}

func TestWatchModesSelectEvents(t *testing.T) {
	frames := func(town string) [][]byte {
		return [][]byte{
			postedBy(t, town, bobID, "@bob", fakemm.ID("p1"), "plain post"),
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "heart"),
			postedBy(t, town, bobID, "@bob", fakemm.ID("p2"), "last post"),
		}
	}
	cases := []struct {
		name         string
		args         []string
		post, react  bool
		waitFor      string
		getsExpected int
	}{
		{"default", nil, true, false, "last post", 0},
		{"messages", []string{"--messages"}, true, false, "last post", 0},
		{"reactions", []string{"--reactions"}, false, true, `"emoji":"heart"`, 1},
		{"both", []string{"--messages", "--reactions"}, true, true, "last post", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := reactServer(t, frames)
			out, _, stop := startWatch(t, r.app, append(c.args, "--json")...)
			// The last frame is a post, so in the reactions-only mode wait on
			// the reaction and then for the connection to settle on a request count.
			waitFor(t, c.waitFor, func() bool { return strings.Contains(out.String(), c.waitFor) })
			stop()
			var types []string
			for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
				var o struct{ Type string }
				if err := json.Unmarshal([]byte(line), &o); err != nil {
					t.Fatalf("line %q: %v", line, err)
				}
				types = append(types, o.Type)
			}
			wantTypes := map[string][]string{
				"default": {"post", "post"}, "messages": {"post", "post"}, "reactions": {"reaction"},
				"both": {"post", "reaction", "post"},
			}[c.name]
			if strings.Join(types, ",") != strings.Join(wantTypes, ",") {
				t.Errorf("types %v, want %v", types, wantTypes)
			}
			if n := r.s.Count(http.MethodGet, "/api/v4/posts/"+fakemm.ID("mine")); n != c.getsExpected {
				t.Errorf("%d GET post, want %d", n, c.getsExpected)
			}
		})
	}
}

func TestWatchReactionsHonourChannelFilter(t *testing.T) {
	other := fakemm.ID("other")
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			reactionEvent(t, other, fakemm.ID("mine"), bobID, "elsewhere"),
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "here"),
		}
	})
	out, _, stop := startWatch(t, r.app, "--reactions", "town-square")
	waitFor(t, "the reaction", func() bool { return strings.Contains(out.String(), ":here:") })
	stop()
	if strings.Contains(out.String(), ":elsewhere:") {
		t.Errorf("reaction of an unwatched channel printed:\n%s", out.String())
	}
	if n := r.s.Count(http.MethodGet, "/api/v4/posts/"+fakemm.ID("mine")); n != 1 {
		t.Errorf("%d GET post, want 1 (the filtered one costs none)", n)
	}
}

func TestWatchNotifyBothKinds(t *testing.T) {
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			reactionEvent(t, town, fakemm.ID("mine"), bobID, "thumbsup"),
			postedBy(t, town, bobID, "@bob", fakemm.ID("p1"), "hello  there"),
			postedBy(t, town, meID, "@alice", fakemm.ID("p2"), "my own, no notification"),
		}
	})
	var mu sync.Mutex
	var got []Notification
	r.app.Notify = func(_ context.Context, n Notification) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, n)
	}
	out, _, stop := startWatch(t, r.app, "--messages", "--reactions", "--notify")
	waitFor(t, "both messages", func() bool { return strings.Contains(out.String(), "my own, no notification") })
	stop()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("%d notifications, want 2: %+v", len(got), got)
	}
	host := strings.TrimPrefix(r.s.URL, "http://")
	want := []Notification{
		{Title: "bob reacted 👍", Body: "town-square: my message", AppLink: "mattermost://" + host + "/myteam/pl/" + fakemm.ID("mine")},
		{Title: "bob in Town Square", Body: "hello there", AppLink: "mattermost://" + host + "/myteam/pl/" + fakemm.ID("p1")},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("notification %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if !strings.Contains(out.String(), "hello  there") {
		t.Errorf("output must stay as it is:\n%s", out.String())
	}
}

func TestWatchNotifyWithoutBusFailsBeforeConnecting(t *testing.T) {
	app, s := newTestApp(t)
	app.GOOS = "linux"
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+t.TempDir()+"/none")
	r := run(app, "watch", "--notify")
	mustCode(t, r, 1)
	if !strings.Contains(r.err, "notification service") || !strings.Contains(r.err, "DBUS_SESSION_BUS_ADDRESS") {
		t.Errorf("message does not explain the failure: %q", r.err)
	}
	if s.Total() != 0 {
		t.Errorf("%d requests before the check", s.Total())
	}
}

func TestCheckNotify(t *testing.T) {
	missing := func(string) (string, error) { return "", http.ErrMissingFile }
	present := func(n string) (string, error) { return "/bin/" + n, nil }
	for _, c := range []struct {
		goos string
		look func(string) (string, error)
		fail string
	}{
		{"linux", missing, ""},
		{"windows", missing, "powershell.exe"},
		{"windows", present, ""},
		{"darwin", missing, ""},
	} {
		err := checkNotify(c.goos, c.look)
		switch {
		case c.fail == "" && err != nil:
			t.Errorf("%s: %v", c.goos, err)
		case c.fail != "" && (err == nil || !strings.Contains(err.Error(), c.fail)):
			t.Errorf("%s: error %v, want mention of %s", c.goos, err, c.fail)
		}
	}
}

func TestNotifyCommandDarwin(t *testing.T) {
	n := Notification{Title: `say "hi"`, Body: `back\slash "q"`, AppLink: "mattermost://h/t/pl/p"}
	c := notifyCommand("darwin", true, n, "grp")
	if c.name != "terminal-notifier" ||
		strings.Join(c.args, "|") != `-title|say "hi"|-message|back\slash "q"|-group|grp|-open|mattermost://h/t/pl/p` {
		t.Errorf("%+v", c)
	}
	c = notifyCommand("darwin", false, n, "grp")
	want := `display notification "back\\slash \"q\"" with title "say \"hi\""`
	if c.name != "osascript" || len(c.args) != 2 || c.args[0] != "-e" || c.args[1] != want {
		t.Errorf("%+v, want script %s", c, want)
	}
	if got := appleQuote("a\nb"); got != `"a b"` {
		t.Errorf("line break: %s", got)
	}
}

func TestNotifyCommandWindows(t *testing.T) {
	n := Notification{Title: `it's <b> & "q"`, Body: "line\nbreak 'x'", AppLink: "mattermost://h/t/pl/p?a=1&b=2"}
	c := notifyCommand("windows", false, n, "g")
	if c.name != "powershell.exe" || len(c.args) != 6 || strings.Join(c.args[:5], " ") != "-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand" {
		t.Fatalf("%+v", c)
	}
	raw, err := base64.StdEncoding.DecodeString(c.args[5])
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("not UTF-16LE base64: %v", err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	script := string(utf16.Decode(u))
	if script != toastScript(n) {
		t.Errorf("round trip changed the script")
	}
	// No text of the message reaches the script itself: only base64 does.
	for _, bad := range []string{"it's", "<b>", "&", "line", "x'", "mattermost"} {
		if strings.Contains(strings.ReplaceAll(script, "mattermost", ""), bad) && bad != "mattermost" {
			t.Errorf("script contains raw text %q", bad)
		}
	}
	if !strings.Contains(script, toastAppID) || !strings.Contains(script, "ContentType = WindowsRuntime") {
		t.Errorf("script lacks the notifier id or the WinRT types")
	}
	for _, v := range []struct{ in, want string }{
		{n.Title, "it&apos;s &lt;b&gt; &amp; &quot;q&quot;"},
		{n.Body, "line\nbreak &apos;x&apos;"},
		{n.AppLink, "mattermost://h/t/pl/p?a=1&amp;b=2"},
	} {
		if !strings.Contains(script, "'"+base64.StdEncoding.EncodeToString([]byte(v.want))+"'") {
			t.Errorf("escaped %q not embedded as %q", v.in, v.want)
		}
	}
}

// fakeNotifyBus mirrors dbusNotifier: signals reach the listener through
// forwardNotifySignals, which owns closing sigs, and close() only closes
// done. A fake that closed sigs itself would hide a forwarder that never
// does, i.e. a listener goroutine that hangs on exit.
type fakeNotifyBus struct {
	mu        sync.Mutex
	shown     []fakeShown
	raw       chan *dbus.Signal
	sigs      chan notifySignal
	done      chan struct{}
	closeOnce sync.Once
	nextID    uint32
}

type fakeShown struct {
	title, body string
	actions     []string
	urgency     urgency
	image       string
}

func newFakeNotifyBus() *fakeNotifyBus {
	f := &fakeNotifyBus{
		// Unbuffered, so that a returned emit means the previous signal has
		// been taken by the listener (see flush).
		raw:    make(chan *dbus.Signal),
		sigs:   make(chan notifySignal),
		done:   make(chan struct{}),
		nextID: 10,
	}
	go forwardNotifySignals(f.raw, f.sigs, f.done)
	return f
}

// emit delivers a signal the way the session bus would: as a raw D-Bus
// signal that the forwarder translates.
func (f *fakeNotifyBus) emit(ns notifySignal) {
	name := notifyDest + ".NotificationClosed"
	body := []any{ns.id, uint32(1)}
	if ns.action {
		name = notifyDest + ".ActionInvoked"
		body = []any{ns.id, "default"}
	}
	f.raw <- &dbus.Signal{Name: name, Body: body}
}

// flush returns once every emitted signal has reached the listener: the
// forwarder takes this ignored signal from raw only after handing the
// previous one over.
func (f *fakeNotifyBus) flush() {
	f.raw <- &dbus.Signal{Name: "flush", Body: []any{uint32(0)}}
}

func (f *fakeNotifyBus) notify(_ context.Context, title, body, image string, actions []string, urgency urgency) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.shown = append(f.shown, fakeShown{title, body, actions, urgency, image})
	return f.nextID, nil
}
func (f *fakeNotifyBus) signals() <-chan notifySignal { return f.sigs }
func (f *fakeNotifyBus) close() error {
	f.closeOnce.Do(func() {
		close(f.done)
	})
	return nil
}

// newBusNotifier is a desktopNotifier on a fake bus whose xdg-open calls are
// recorded; stop closes it and returns the recorded links.
func newBusNotifier() (d *desktopNotifier, bus *fakeNotifyBus, stop func() []string) {
	bus = newFakeNotifyBus()
	var mu sync.Mutex
	var opened []string
	d = &desktopNotifier{goos: "linux", bus: bus, links: map[uint32]string{},
		run: func(_ context.Context, name string, args ...string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			opened = append(opened, name+" "+strings.Join(args, " "))
			return "", nil
		}}
	d.wg.Add(1)
	go d.listen(bus.signals())
	return d, bus, func() []string {
		bus.flush()
		d.Close()
		mu.Lock()
		defer mu.Unlock()
		return opened
	}
}

func TestDesktopNotifierClickOpensLinkOnce(t *testing.T) {
	d, bus, stop := newBusNotifier()
	d.Notify(context.Background(), Notification{Title: "first", Body: "b1", AppLink: "mattermost://h/t/pl/1"})
	d.Notify(context.Background(), Notification{Title: "second", Body: "b2", AppLink: "mattermost://h/t/pl/2"})
	bus.emit(notifySignal{id: 11, action: true})
	bus.emit(notifySignal{id: 11, action: true}) // the id is forgotten after the first click
	bus.emit(notifySignal{id: 99, action: true}) // unknown id
	got := stop()
	if len(got) != 1 || got[0] != "xdg-open mattermost://h/t/pl/1" {
		t.Errorf("opened %v", got)
	}
	if len(bus.shown[0].actions) != 2 || bus.shown[0].actions[0] != "default" {
		t.Errorf("actions %v", bus.shown[0].actions)
	}
}

func TestDesktopNotifierClosedForgetsLink(t *testing.T) {
	d, bus, stop := newBusNotifier()
	d.Notify(context.Background(), Notification{Title: "t", Body: "b", AppLink: "mattermost://h/t/pl/1"})
	bus.emit(notifySignal{id: 11})
	bus.emit(notifySignal{id: 11, action: true})
	if got := stop(); len(got) != 0 {
		t.Errorf("opened after close: %v", got)
	}
}

func TestDesktopNotifierNoActionsWithoutLink(t *testing.T) {
	d, bus, stop := newBusNotifier()
	d.Notify(context.Background(), Notification{Title: "t", Body: "b"})
	d.NotifyNow(context.Background(), Notification{Title: "n", Body: "b", AppLink: "mattermost://h/t/pl/1"})
	bus.emit(notifySignal{id: 11, action: true})
	bus.emit(notifySignal{id: 12, action: true})
	if got := stop(); len(got) != 0 {
		t.Errorf("opened: %v", got)
	}
	for _, s := range bus.shown {
		if len(s.actions) != 0 {
			t.Errorf("%s has actions %v", s.title, s.actions)
		}
	}
}

func TestDesktopNotifierEscapesMarkup(t *testing.T) {
	d, bus, stop := newBusNotifier()
	d.Notify(context.Background(), Notification{Title: "a & <b>", Body: "x < y && <i>z</i>"})
	stop()
	if bus.shown[0].body != "x &lt; y &amp;&amp; &lt;i&gt;z&lt;/i&gt;" || bus.shown[0].title != "a & <b>" {
		t.Errorf("%+v", bus.shown[0])
	}
}

func TestDesktopNotifierBoundsTrackedLinks(t *testing.T) {
	d, _, stop := newBusNotifier()
	for i := 0; i < notifyMaxTracked+5; i++ {
		d.Notify(context.Background(), Notification{Title: "t", Body: "b", AppLink: "mattermost://h/t/pl/1"})
	}
	stop()
	if len(d.links) != notifyMaxTracked || len(d.order) != notifyMaxTracked {
		t.Errorf("%d links, %d ordered, want %d", len(d.links), len(d.order), notifyMaxTracked)
	}
	if _, ok := d.links[11]; ok {
		t.Errorf("oldest id still tracked")
	}
}

func TestWatchNotifyNoSessionAtStart(t *testing.T) {
	app, s := newTestApp(t)
	if err := app.Store.DeleteSession("test"); err != nil {
		t.Fatal(err)
	}
	var got []Notification
	app.Notify = func(_ context.Context, n Notification) { got = append(got, n) }
	r := run(app, "watch", "--reactions", "--notify")
	mustCode(t, r, 3)
	if len(got) != 1 {
		t.Fatalf("%d notifications, want 1: %+v", len(got), got)
	}
	if got[0].Title != "mm-cli: sign in again" || got[0].AppLink != "" ||
		!strings.Contains(got[0].Body, `"test"`) || !strings.Contains(got[0].Body, "mm-cli auth login") {
		t.Errorf("notification = %+v", got[0])
	}
	if s.Total() != 0 {
		t.Errorf("%d requests without a session", s.Total())
	}
}

func TestWatchNotifyRejectedSession(t *testing.T) {
	app, s := newTestApp(t)
	var mu sync.Mutex
	conns := 0
	s.Handle(http.MethodGet, "/api/v4/websocket", wsHandler(t, "another-token", nil, true, &conns, &mu))
	app.Sleep = func(context.Context, time.Duration) error {
		t.Error("watch retried after the session was rejected")
		return nil
	}
	var got []Notification
	app.Notify = func(_ context.Context, n Notification) { got = append(got, n) }
	r := run(app, "watch", "--notify")
	mustCode(t, r, 3)
	if len(got) != 1 || got[0].Title != "mm-cli: sign in again" || got[0].AppLink != "" {
		t.Errorf("notifications = %+v", got)
	}
}

func TestWatchNoSessionWithoutNotifyIsSilent(t *testing.T) {
	app, _ := newTestApp(t)
	if err := app.Store.DeleteSession("test"); err != nil {
		t.Fatal(err)
	}
	app.Notify = func(context.Context, Notification) { t.Error("notification without --notify") }
	mustCode(t, run(app, "watch"), 3)
}

func TestNotifyCommandWithoutLink(t *testing.T) {
	n := Notification{Title: "t", Body: "b"}
	if c := notifyCommand("darwin", true, n, "g"); strings.Contains(strings.Join(c.args, " "), "-open") {
		t.Errorf("darwin: %+v", c)
	}
	if sc := toastScript(n); strings.Contains(sc, "activationType") || strings.Contains(sc, "launch") {
		t.Errorf("windows script has an action:\n%s", sc)
	}
	if sc := toastScript(Notification{Title: "t", Body: "b", AppLink: "mattermost://h"}); !strings.Contains(sc, "activationType") {
		t.Errorf("windows script lost the action with a link:\n%s", sc)
	}
}

func TestDesktopNotifierNowRunsSynchronously(t *testing.T) {
	var calls []string
	d := &desktopNotifier{goos: "darwin",
		run: func(_ context.Context, name string, args ...string) (string, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return "", nil
		}}
	d.NotifyNow(context.Background(), Notification{Title: "t", Body: "b", AppLink: "mattermost://h"})
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "osascript ") {
		t.Errorf("calls = %v", calls)
	}
}

func TestWatchIncludeOwnReactionAndMessage(t *testing.T) {
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			reactionEvent(t, town, fakemm.ID("mine"), meID, "ownheart"),
			// Mine on someone else's message: still ignored.
			reactionEvent(t, town, fakemm.ID("carols"), meID, "foreign"),
			postedBy(t, town, meID, "@alice", fakemm.ID("ownpost"), "note to self"),
		}
	})
	got := notifyRecorder(r.app)
	out, _, stop := startWatch(t, r.app, "--messages", "--reactions", "--notify", "--include-own")
	waitFor(t, "both events", func() bool {
		return strings.Contains(out.String(), ":ownheart:") && strings.Contains(out.String(), "note to self")
	})
	waitFor(t, "two notifications", func() bool { return len(got()) >= 2 })
	stop()
	if strings.Contains(out.String(), ":foreign:") {
		t.Errorf("own reaction on a foreign message printed:\n%s", out.String())
	}
	if n := len(got()); n != 2 {
		t.Errorf("%d notifications, want 2: %+v", n, got())
	}
}

func TestWatchOwnMessageNoNotificationWithoutFlag(t *testing.T) {
	r := reactServer(t, func(town string) [][]byte {
		return [][]byte{
			reactionEvent(t, town, fakemm.ID("mine"), meID, "ownheart"),
			postedBy(t, town, meID, "@alice", fakemm.ID("ownpost"), "note to self"),
			postedBy(t, town, bobID, "@bob", fakemm.ID("bobpost"), "sentinel"),
		}
	})
	got := notifyRecorder(r.app)
	out, _, stop := startWatch(t, r.app, "--messages", "--reactions", "--notify")
	waitFor(t, "the sentinel", func() bool { return strings.Contains(out.String(), "sentinel") })
	waitFor(t, "a notification", func() bool { return len(got()) >= 1 })
	stop()
	if strings.Contains(out.String(), ":ownheart:") || len(got()) != 1 {
		t.Errorf("own events leaked: %d notifications\n%s", len(got()), out.String())
	}
}

func TestDesktopNotifierCommandIsBounded(t *testing.T) {
	old := notifyCmdTimeout
	notifyCmdTimeout = 50 * time.Millisecond
	t.Cleanup(func() { notifyCmdTimeout = old })
	started := make(chan struct{})
	d := &desktopNotifier{goos: "darwin",
		run: func(ctx context.Context, _ string, _ ...string) (string, error) {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		}}
	d.Notify(context.Background(), Notification{Title: "t", Body: "b"})
	<-started
	done := make(chan struct{})
	go func() { d.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return: the notifier command is not bounded")
	}
}
