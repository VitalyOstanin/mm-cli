package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
	"github.com/VitalyOstanin/mm-cli/internal/resolve"
)

func TestUsernamesFailureWarnsOnce(t *testing.T) {
	app, s := newTestApp(t)
	servePosts(s)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 500, map[string]any{"message": "boom"})
	for _, args := range [][]string{{"post", "list", "town-square"}, {"post", "list", "town-square", "--json"}} {
		r := run(app, args...)
		mustCode(t, r, 0)
		if n := strings.Count(r.err, "warning: cannot look up user names"); n != 1 {
			t.Fatalf("%v: warning count %d, stderr:\n%s", args, n, r.err)
		}
		if strings.Contains(r.out, "warning") {
			t.Fatalf("warning leaked to stdout:\n%s", r.out)
		}
	}
}

func TestUsernamesWarnsOncePerSession(t *testing.T) {
	app, s := newTestApp(t)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 500, map[string]any{"message": "boom"})
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	sess.usernames(context.Background(), []string{bobID})
	sess.usernames(context.Background(), []string{fakemm.ID("carol")})
	if n := strings.Count(app.Err.(interface{ String() string }).String(), "cannot look up user names"); n != 1 {
		t.Fatalf("warning count %d", n)
	}
}

func TestUnreadableCacheWarnsOnce(t *testing.T) {
	app, _ := newTestApp(t)
	if err := os.MkdirAll(filepath.Dir(app.Store.CachePath("test")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app.Store.CachePath("test"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
	if n := strings.Count(app.Err.(interface{ String() string }).String(), "warning: cannot read the cache"); n != 1 {
		t.Fatalf("warning count %d", n)
	}
}

// seedNames writes a cache file whose name of bob was fetched at the given time.
func seedNames(t *testing.T, app *App, name string, at time.Time) {
	t.Helper()
	c := resolve.NewCache()
	c.Now = func() time.Time { return at }
	c.PutUser(bobID, name)
	if err := c.Save(app.Store.CachePath("test")); err != nil {
		t.Fatal(err)
	}
}

func TestUsernamesFreshNameNoRequest(t *testing.T) {
	app, s := newTestApp(t)
	seedNames(t, app, "bob", now.Add(-resolve.NameTTL+time.Hour))
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.usernames(context.Background(), []string{bobID}); got[bobID] != "bob" {
		t.Fatalf("names %v", got)
	}
	if n := s.Count(http.MethodPost, "/api/v4/users/ids"); n != 0 {
		t.Fatalf("requests %d, want 0", n)
	}
}

func TestUsernamesStaleNameRefreshed(t *testing.T) {
	app, s := newTestApp(t)
	seedNames(t, app, "bob", now.Add(-resolve.NameTTL-time.Hour))
	carolID := fakemm.ID("carol")
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "robert"}, {Id: carolID, Username: "carol"}})
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	got := sess.usernames(context.Background(), []string{bobID, carolID})
	if got[bobID] != "robert" || got[carolID] != "carol" {
		t.Fatalf("names %v", got)
	}
	if n := s.Count(http.MethodPost, "/api/v4/users/ids"); n != 1 {
		t.Fatalf("requests %d, want 1", n)
	}
	var ids []string
	_ = json.Unmarshal(s.Last(http.MethodPost, "/api/v4/users/ids").Body, &ids)
	if len(ids) != 2 {
		t.Fatalf("one batch with both ids expected, got %v", ids)
	}
	sess.Close()
	c := resolve.LoadCache(app.Store.CachePath("test"))
	c.Now = app.Now
	if n, _ := c.Name(bobID); n != "robert" || c.NameStale(bobID) {
		t.Fatalf("saved %q stale=%v", n, c.NameStale(bobID))
	}
}

func TestUsernamesOldCacheFileRefreshed(t *testing.T) {
	app, s := newTestApp(t)
	p := app.Store.CachePath("test")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"channels":{},"users":{"bob":"` + bobID + `"},"names":{"` + bobID + `":"bob"}}`
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []*model.User{{Id: bobID, Username: "robert"}})
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.usernames(context.Background(), []string{bobID}); got[bobID] != "robert" {
		t.Fatalf("names %v", got)
	}
	if n := s.Count(http.MethodPost, "/api/v4/users/ids"); n != 1 {
		t.Fatalf("requests %d, want 1", n)
	}
}

func TestUsernamesStaleNameShownOnFailure(t *testing.T) {
	app, s := newTestApp(t)
	seedNames(t, app, "bob", now.Add(-resolve.NameTTL-time.Hour))
	s.JSON(http.MethodPost, "/api/v4/users/ids", 500, map[string]any{"message": "boom"})
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.usernames(context.Background(), []string{bobID}); got[bobID] != "bob" {
		t.Fatalf("names %v, want the stale name", got)
	}
	if n := strings.Count(app.Err.(interface{ String() string }).String(), "warning: cannot look up user names"); n != 1 {
		t.Fatalf("warning count %d", n)
	}
}

// openAt opens a session whose clock reads at; the cache holds bob as
// "oldbob", fetched at fetched.
func openAt(t *testing.T, app *App, at, fetched time.Time) *Session {
	t.Helper()
	app.Now = func() time.Time { return fetched }
	sess, err := app.openSession()
	if err != nil {
		t.Fatal(err)
	}
	sess.Resolver.Cache.PutUser(bobID, "oldbob")
	app.Now = func() time.Time { return at }
	return sess
}

func TestUsernamesFreshNameNeedsNoRequest(t *testing.T) {
	app, s := newTestApp(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sess := openAt(t, app, t0.Add(resolve.NameTTL-time.Hour), t0)
	if got := sess.usernames(context.Background(), []string{bobID})[bobID]; got != "oldbob" {
		t.Fatalf("name %q, want oldbob", got)
	}
	if n := s.Count(http.MethodPost, "/api/v4/users/ids"); n != 0 {
		t.Fatalf("%d user lookups for a fresh name, want 0", n)
	}
}

func TestUsernamesStaleNameIsFetchedAgain(t *testing.T) {
	app, s := newTestApp(t)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 200, []map[string]any{{"id": bobID, "username": "bob"}})
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := t0.Add(resolve.NameTTL + time.Hour)
	sess := openAt(t, app, later, t0)
	if got := sess.usernames(context.Background(), []string{bobID})[bobID]; got != "bob" {
		t.Fatalf("name %q, want the renamed bob", got)
	}
	if n := s.Count(http.MethodPost, "/api/v4/users/ids"); n != 1 {
		t.Fatalf("%d user lookups, want 1", n)
	}
	if sess.Resolver.Cache.NameStale(bobID) {
		t.Fatal("the fetched name is still stale")
	}
}

func TestUsernamesStaleNameShownWhenServerFails(t *testing.T) {
	app, s := newTestApp(t)
	s.JSON(http.MethodPost, "/api/v4/users/ids", 500, map[string]any{"message": "boom"})
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	errb := &bytes.Buffer{}
	app.Err = errb
	sess := openAt(t, app, t0.Add(resolve.NameTTL+time.Hour), t0)
	if got := sess.usernames(context.Background(), []string{bobID})[bobID]; got != "oldbob" {
		t.Fatalf("name %q, want the stale oldbob rather than an id", got)
	}
	if !strings.Contains(errb.String(), "cannot look up user names") {
		t.Fatalf("no warning, stderr: %s", errb.String())
	}
}
