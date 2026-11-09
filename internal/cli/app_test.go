package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
	"github.com/VitalyOstanin/mm-cli/internal/resolve"
)

func TestUsageErrors(t *testing.T) {
	app := newEmptyApp(t)
	mustCode(t, run(app, "nope"), clierr.CodeUsage)
	mustCode(t, run(app, "post", "list", "--bogus"), clierr.CodeUsage)
	mustCode(t, run(app, "post", "get"), clierr.CodeUsage)
	mustCode(t, run(app, "post", "get", "short"), clierr.CodeUsage)
}

func TestNoServer(t *testing.T) {
	r := run(newEmptyApp(t), "auth", "check")
	mustCode(t, r, clierr.CodeAuth)
	if !strings.Contains(r.err, "mm-cli auth login") {
		t.Fatal(r.err)
	}
}

func TestExpiredSession(t *testing.T) {
	app, s := newTestApp(t)
	s.Handle(http.MethodGet, "/api/v4/users/me", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusUnauthorized, "Invalid or expired session")
	})
	r := run(app, "auth", "check")
	mustCode(t, r, clierr.CodeAuth)
	if !strings.Contains(r.err, "run `mm-cli auth login`") {
		t.Fatal(r.err)
	}
	r = run(app, "auth", "check", "--json")
	mustCode(t, r, clierr.CodeAuth)
	var obj struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.err), &obj); err != nil || obj.Error.Code != clierr.CodeAuth || obj.Error.Message == "" {
		t.Fatalf("json error %q: %v", r.err, err)
	}
}

func TestStaleCachedChannelIsRetried(t *testing.T) {
	app, s := newTestApp(t)
	stale, fresh := fakemm.ID("stale"), fakemm.ID("fresh")
	c := resolve.NewCache()
	c.Channels["myteam:town-square"] = stale
	c.PutUser(meID, "alice")
	if err := c.Save(app.Store.CachePath("test")); err != nil {
		t.Fatal(err)
	}
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", 200, &model.Channel{Id: fresh, Name: "town-square"})
	s.JSON(http.MethodGet, "/api/v4/channels/"+fresh+"/posts", 200, &model.PostList{Posts: map[string]*model.Post{}})
	mustCode(t, run(app, "post", "list", "town-square"), 0)
	if s.Count(http.MethodGet, "/api/v4/channels/"+stale+"/posts") != 1 || s.Count(http.MethodGet, "/api/v4/channels/"+fresh+"/posts") != 1 {
		t.Fatal("expected one try with the stale id and one with the fresh id")
	}
	if got := resolve.LoadCache(app.Store.CachePath("test")).Channels["myteam:town-square"]; got != fresh {
		t.Fatalf("cache holds %q", got)
	}
}

func TestConfirmWithoutTerminal(t *testing.T) {
	app, s := newTestApp(t)
	r := run(app, "post", "delete", fakemm.ID("p"))
	mustCode(t, r, clierr.CodeUsage)
	if s.Total() != 0 {
		t.Fatal("the server must not be called")
	}
}

func TestConfirmOnTerminal(t *testing.T) {
	app, s := newTestApp(t)
	app.IsTerminal = func() bool { return true }
	id := fakemm.ID("p")
	s.JSON(http.MethodDelete, "/api/v4/posts/"+id, 200, map[string]string{"status": "OK"})
	mustCode(t, runStdin(app, "n\n", "post", "delete", id), clierr.CodeError)
	mustCode(t, runStdin(app, "y\n", "post", "delete", id), 0)
	if s.Count(http.MethodDelete, "/api/v4/posts/"+id) != 1 {
		t.Fatal("expected exactly one delete")
	}
}

func TestParseSince(t *testing.T) {
	ok := map[string]time.Time{
		"2026-03-01T00:00:00Z": time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		"2h":                   now.Add(-2 * time.Hour),
		"3d":                   now.Add(-72 * time.Hour),
	}
	for in, want := range ok {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "x", "-2h", "0d", "1.5d", "d"} {
		if _, err := parseSince(bad, now); clierr.ExitCode(err) != clierr.CodeUsage {
			t.Errorf("parseSince(%q) err = %v", bad, err)
		}
	}
}

func TestTextOutputStripsTerminalControls(t *testing.T) {
	app, s := newTestApp(t)
	id := fakemm.ID("p1")
	evil := "hi\x1b]0;evil\x07 there\x1b[2J\rX"
	s.JSON(http.MethodGet, "/api/v4/posts/"+id, 200, &model.Post{Id: id, UserId: bobID, Message: evil, CreateAt: now.UnixMilli()})
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "bob"}})
	r := run(app, "post", "get", id)
	mustCode(t, r, 0)
	if strings.ContainsAny(r.out, "\x1b\x07\r") || !strings.Contains(r.out, "evil") {
		t.Fatalf("control characters in text output: %q", r.out)
	}
	r = run(app, "post", "get", id, "--json")
	mustCode(t, r, 0)
	var got struct{ Message string }
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got.Message != evil {
		t.Fatalf("JSON must carry the message unchanged: %q %v", r.out, err)
	}
}

func TestMessageMustBeUTF8(t *testing.T) {
	app, s := newTestApp(t)
	serveSend(t, s)
	cp1251 := string([]byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2})
	before := s.Total()
	r := runStdin(app, cp1251, "send", "@me", "-")
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "UTF-8") {
		t.Fatalf("stderr %q", r.err)
	}
	mustCode(t, run(app, "send", "@me", cp1251), clierr.CodeUsage)
	if s.Total() != before {
		t.Fatal("an invalid message must not reach the server")
	}
}

// cacheChannel stores a cached address of the test server.
func cacheChannel(t *testing.T, app *App, key, id string) {
	t.Helper()
	c := resolve.NewCache()
	c.Channels[key] = id
	c.PutUser(meID, "alice")
	if err := c.Save(app.Store.CachePath("test")); err != nil {
		t.Fatal(err)
	}
}

func TestNotFoundOfAnotherObjectIsNotRetried(t *testing.T) {
	app, s := newTestApp(t)
	cacheChannel(t, app, "@me", selfDM)
	uploaded := serveSend(t, s)
	s.Handle(http.MethodPost, "/api/v4/posts", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusNotFound, "no such root post")
	})
	s.JSON(http.MethodGet, "/api/v4/channels/"+selfDM, http.StatusOK, &model.Channel{Id: selfDM, Type: model.ChannelTypeDirect})
	files := writeFiles(t, "a.txt")
	r := run(app, "send", "@me", "reply", "-f", files[0], "--reply-to", fakemm.ID("missing"))
	mustCode(t, r, clierr.CodeNotFound)
	if len(*uploaded) != 1 {
		t.Errorf("uploaded %v; the file must be uploaded once", *uploaded)
	}
	if n := s.Count(http.MethodPost, "/api/v4/posts"); n != 1 {
		t.Errorf("%d post attempts, want 1", n)
	}
	if n := s.Count(http.MethodPost, "/api/v4/channels/direct"); n != 0 {
		t.Errorf("%d direct channel lookups; the cached channel exists", n)
	}
	if got := resolve.LoadCache(app.Store.CachePath("test")).Channels["@me"]; got != selfDM {
		t.Errorf("cache holds %q", got)
	}
}

func TestArchivedCachedChannelIsRetried(t *testing.T) {
	app, s := newTestApp(t)
	old, fresh := fakemm.ID("old"), fakemm.ID("fresh")
	cacheChannel(t, app, "myteam:town-square", old)
	s.Handle(http.MethodGet, "/api/v4/channels/"+old+"/posts", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusNotFound, "not found")
	})
	s.JSON(http.MethodGet, "/api/v4/channels/"+old, http.StatusOK, &model.Channel{Id: old, Name: "town-square", DeleteAt: 1})
	s.JSON(http.MethodGet, "/api/v4/teams/name/myteam/channels/name/town-square", http.StatusOK, &model.Channel{Id: fresh, Name: "town-square"})
	s.JSON(http.MethodGet, "/api/v4/channels/"+fresh+"/posts", http.StatusOK, &model.PostList{Posts: map[string]*model.Post{}})
	mustCode(t, run(app, "post", "list", "town-square"), 0)
	if s.Count(http.MethodGet, "/api/v4/channels/"+fresh+"/posts") != 1 {
		t.Fatal("expected a try with the fresh id")
	}
}

func TestShortVersionFlag(t *testing.T) {
	app, _ := newTestApp(t)
	r := run(app, "-V")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, app.Version) {
		t.Fatalf("-V printed %q, want the version %q", r.out, app.Version)
	}
}

func TestJSONErrorKeepsAngleBrackets(t *testing.T) {
	app, _ := newTestApp(t)
	errb := &bytes.Buffer{}
	app.Err = errb
	app.json = true
	app.PrintError(errors.New("bad value <x> & more"))
	app.json = false
	var out struct {
		Error struct{ Message string } `json:"error"`
	}
	raw := errb.String()
	if strings.Contains(raw, `\u003c`) {
		t.Fatalf("JSON error escapes HTML: %s", raw)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.Error.Message != "bad value <x> & more" {
		t.Fatalf("decoded %+v, err %v from %s", out, err, raw)
	}
}

// Two commands for one server run at the same time and close one after the
// other: the address cache keeps what both of them learned.
func TestSessionCloseKeepsCacheOfParallelSession(t *testing.T) {
	app, _ := newTestApp(t)
	s1, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	bob, carol := fakemm.ID("bob"), fakemm.ID("carol")
	s1.Resolver.Cache.PutUser(bob, "bob")
	s2.Resolver.Cache.PutUser(carol, "carol")
	s1.Close()
	s2.Close()
	c := resolve.LoadCache(app.Store.CachePath("test"))
	if c.Users["bob"] != bob || c.Users["carol"] != carol {
		t.Fatalf("cache users: %+v", c.Users)
	}
}
