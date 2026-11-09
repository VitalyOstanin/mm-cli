package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/resolve"
)

func TestAuthList(t *testing.T) {
	app := newEmptyApp(t)
	r := run(app, "auth", "list")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "No servers saved") {
		t.Fatal(r.out)
	}
	cfg := &config.Config{Current: "b", Servers: map[string]*config.Server{
		"a": {URL: "https://a.example.com", Username: "alice", Team: "myteam"},
		"b": {URL: "https://b.example.com", Username: "bob"},
	}}
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	r = run(app, "auth", "list")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "  a  https://a.example.com  @alice  team myteam") || !strings.Contains(r.out, "* b  https://b.example.com") {
		t.Fatal(r.out)
	}
	r = run(app, "auth", "list", "--json")
	var got []serverSummary
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || len(got) != 2 || got[0].Name != "a" || !got[1].Current {
		t.Fatalf("%q %v", r.out, err)
	}
}

func TestAuthSet(t *testing.T) {
	app, _ := newTestApp(t)
	r := run(app, "auth", "current")
	mustCode(t, r, 0)
	if !strings.HasPrefix(r.out, "* test  ") {
		t.Fatal(r.out)
	}
	mustCode(t, run(app, "auth", "set"), clierr.CodeUsage)
	mustCode(t, run(app, "auth", "set", "missing"), clierr.CodeUsage)
	cfg, _ := app.Store.Load()
	cfg.Servers["second"] = &config.Server{URL: "https://b.example.com"}
	_ = app.Store.Save(cfg)
	mustCode(t, run(app, "auth", "set", "second"), 0)
	if cfg, _ := app.Store.Load(); cfg.Current != "second" {
		t.Fatal(cfg.Current)
	}
}

func TestAuthDelete(t *testing.T) {
	app, _ := newTestApp(t)
	c := resolve.NewCache()
	c.PutUser(meID, "alice")
	_ = c.Save(app.Store.CachePath("test"))
	mustCode(t, run(app, "auth", "delete", "test"), clierr.CodeUsage)
	mustCode(t, run(app, "auth", "delete", "missing", "--confirm"), clierr.CodeUsage)
	mustCode(t, run(app, "auth", "delete", "test", "--confirm"), 0)
	cfg, _ := app.Store.Load()
	if len(cfg.Servers) != 0 || cfg.Current != "" {
		t.Fatalf("%+v", cfg)
	}
	if _, err := app.Store.LoadSession("test"); err == nil {
		t.Fatal("session must be removed")
	}
	if _, err := os.Stat(app.Store.CachePath("test")); !os.IsNotExist(err) {
		t.Fatal("cache must be removed")
	}
}

func writeRawConfig(t *testing.T, app *App, body string) {
	t.Helper()
	if err := config.WriteFileAtomic(filepath.Join(app.Store.ConfigDir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyServerEntryIsAConfigError(t *testing.T) {
	for _, args := range [][]string{{"auth", "list"}, {"auth", "check"}, {"auth", "delete", "work", "--confirm"}} {
		app := newEmptyApp(t)
		writeRawConfig(t, app, "servers: {work: }\n")
		if err := app.Store.SaveSession("work", "tok"); err != nil {
			t.Fatal(err)
		}
		r := run(app, args...)
		mustCode(t, r, clierr.CodeUsage)
		if !strings.Contains(r.err, `"work"`) {
			t.Errorf("%v: %s", args, r.err)
		}
	}
}

func TestAuthDeleteDotDotKeepsParent(t *testing.T) {
	app := newEmptyApp(t)
	parent := filepath.Dir(app.Store.CacheDir)
	marker := filepath.Join(parent, "keep")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRawConfig(t, app, "servers:\n  \"..\": {url: https://chat.example.com}\n")
	mustCode(t, run(app, "auth", "delete", "..", "--confirm"), clierr.CodeUsage)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("parent of the cache dir removed: %v", err)
	}
}

func TestOpenSessionRejectsPlainHTTPServer(t *testing.T) {
	app := newEmptyApp(t)
	// .invalid never resolves (RFC 6761), so nothing leaves the machine even
	// without the check.
	writeRawConfig(t, app, "servers:\n  work: {url: \"http://chat.invalid\"}\n")
	if err := app.Store.SaveSession("work", "tok"); err != nil {
		t.Fatal(err)
	}
	r := run(app, "auth", "check")
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "plain http") {
		t.Fatal(r.err)
	}
}

func TestServerNameFromURLIsValid(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"https://chat.example.com", "chat-example-com"},
		{"https://chat.example.com:8443/mm", "chat-example-com-8443"},
		{"http://localhost:8065", "localhost-8065"},
		{"http://127.0.0.1:8065", "127-0-0-1-8065"},
		{"http://[::1]:8065", "1-8065"},
		{"http://[::1]", "1"},
	} {
		got := serverNameFromURL(tt.url)
		if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.url, got, tt.want)
		}
		if err := config.ValidateServerName(got); err != nil {
			t.Errorf("%s: %v", tt.url, err)
		}
	}
}

func TestLoginWithServerRejectsPlainHTTPURL(t *testing.T) {
	app := newEmptyApp(t)
	writeRawConfig(t, app, "servers:\n  work: {url: \"http://chat.invalid\"}\n")
	r := runStdin(app, "pat\n", "auth", "login", "--name", "work", "--token-stdin")
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "plain http") {
		t.Fatal(r.err)
	}
}

func TestAuthCurrentSetDeleteJSON(t *testing.T) {
	app, _ := newTestApp(t)
	decode := func(r result) map[string]any {
		t.Helper()
		mustCode(t, r, 0)
		var got map[string]any
		if err := json.Unmarshal([]byte(r.out), &got); err != nil {
			t.Fatalf("stdout is not JSON: %q %v", r.out, err)
		}
		return got
	}
	if got := decode(run(app, "auth", "current", "--json")); got["name"] != "test" || got["current"] != true {
		t.Fatal(got)
	}
	cfg, _ := app.Store.Load()
	cfg.Servers["second"] = &config.Server{URL: "https://b.example.com"}
	_ = app.Store.Save(cfg)
	if got := decode(run(app, "auth", "current", "--server", "second", "--json")); got["name"] != "second" || got["current"] != false {
		t.Fatal(got)
	}
	if got := decode(run(app, "auth", "set", "second", "--json")); got["action"] != "current_set" || got["server"] != "second" {
		t.Fatal(got)
	}
	if got := decode(run(app, "auth", "delete", "test", "--confirm", "--json")); got["action"] != "deleted" || got["server"] != "test" {
		t.Fatal(got)
	}
}
