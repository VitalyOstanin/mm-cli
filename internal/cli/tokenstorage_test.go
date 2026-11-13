package cli

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/config/configtest"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

func tokenFile(app *App, name string) string {
	return filepath.Join(app.Store.ConfigDir, "sessions", name+".token")
}

func TestTokenFileMovedOnFirstRead(t *testing.T) {
	for _, jsonOut := range []bool{false, true} {
		app, s := newTestApp(t)
		s.Handle(http.MethodGet, "/api/v4/users/me", func(w http.ResponseWriter, _ *http.Request) {
			fakemm.WriteJSON(w, 200, &model.User{Id: meID, Username: "alice"})
		})
		mem := app.Store.Tokens
		// Return to the state of an earlier version: a token file only.
		if err := mem.Delete("test"); err != nil {
			t.Fatal(err)
		}
		if err := app.Store.FileBackend().Set("test", "tok"); err != nil {
			t.Fatal(err)
		}
		args := []string{"auth", "check"}
		if jsonOut {
			args = append(args, "--json")
		}
		r := run(app, args...)
		mustCode(t, r, 0)
		moved := strings.Contains(r.err, "moved the session token of server test")
		if moved == jsonOut {
			t.Errorf("json=%v: stderr %q", jsonOut, r.err)
		}
		if _, err := os.Stat(tokenFile(app, "test")); !os.IsNotExist(err) {
			t.Errorf("json=%v: file not deleted: %v", jsonOut, err)
		}
		if tok, err := mem.Get("test"); err != nil || tok != "tok" {
			t.Errorf("json=%v: store holds %q %v", jsonOut, tok, err)
		}
	}
}

func TestTokenFileKeptWhenStoreUnavailable(t *testing.T) {
	app, _ := newTestApp(t)
	mem := app.Store.Tokens.(*configtest.MemoryBackend)
	if err := mem.Delete("test"); err != nil {
		t.Fatal(err)
	}
	if err := app.Store.FileBackend().Set("test", "tok"); err != nil {
		t.Fatal(err)
	}
	mem.Err = errors.New("no D-Bus session")
	r := run(app, "auth", "check")
	mustCode(t, r, clierr.CodeError)
	for _, want := range []string{"no D-Bus session", "token_storage: file", "MM_TOKEN_STORAGE=file"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("stderr lacks %q: %s", want, r.err)
		}
	}
	if _, err := os.Stat(tokenFile(app, "test")); err != nil {
		t.Fatalf("token file lost: %v", err)
	}
}

func TestLoginStoreUnavailableWritesNoFile(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
	app := newEmptyApp(t)
	mem := app.Store.Tokens.(*configtest.MemoryBackend)
	mem.Err = errors.New("collection is locked")
	r := runStdin(app, "pat\n", "auth", "login", s.URL, "--name", "work", "--token-stdin")
	mustCode(t, r, clierr.CodeError)
	if !strings.Contains(r.err, "MM_TOKEN_STORAGE=file") {
		t.Fatalf("stderr: %s", r.err)
	}
	if _, err := os.Stat(tokenFile(app, "work")); !os.IsNotExist(err) {
		t.Fatalf("a token file was written as a fallback: %v", err)
	}
}

func TestInvalidTokenStorageIsUsageError(t *testing.T) {
	app, _ := newTestApp(t)
	app.Store.Tokens = nil
	t.Setenv(config.EnvTokenStorage, "vault")
	r := run(app, "auth", "check")
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "keyring or file") {
		t.Fatalf("stderr: %s", r.err)
	}
}

func TestTokenStorageFileFromConfig(t *testing.T) {
	app, _ := newTestApp(t)
	app.Store.Tokens = nil
	writeRawConfig(t, app, "current: test\ntoken_storage: file\nservers:\n  test: {url: \"http://127.0.0.1:1\"}\n")
	if err := app.Store.SaveSession("test", "tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tokenFile(app, "test")); err != nil {
		t.Fatalf("file mode must write the token file: %v", err)
	}
}

func TestLogoutAndDeleteClearStoreAndFile(t *testing.T) {
	for _, cmd := range []string{"logout", "delete"} {
		app, s := newTestApp(t)
		s.Handle(http.MethodPost, "/api/v4/users/logout", func(w http.ResponseWriter, _ *http.Request) {
			fakemm.WriteJSON(w, 200, map[string]string{"status": "OK"})
		})
		if err := app.Store.FileBackend().Set("test", "old"); err != nil {
			t.Fatal(err)
		}
		args := []string{"auth", cmd}
		if cmd == "delete" {
			args = append(args, "test", "--confirm")
		}
		mustCode(t, run(app, args...), 0)
		if _, err := app.Store.Tokens.Get("test"); !errors.Is(err, config.ErrNoSession) {
			t.Errorf("%s: store keeps the token: %v", cmd, err)
		}
		if _, err := os.Stat(tokenFile(app, "test")); !os.IsNotExist(err) {
			t.Errorf("%s: token file left: %v", cmd, err)
		}
	}
}
