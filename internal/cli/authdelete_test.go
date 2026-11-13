package cli

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/VitalyOstanin/mm-cli/internal/config"
)

// keyringApp returns a test App whose tokens go to the (mock) OS secret store.
func keyringApp(t *testing.T) *App {
	t.Helper()
	keyring.MockInit()
	t.Setenv(config.EnvTokenStorage, "")
	app, _ := newTestApp(t)
	app.Store.Tokens = nil
	return app
}

func TestAuthDeleteRemovesKeyringEntry(t *testing.T) {
	app := keyringApp(t)
	cfg, err := app.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	acc := config.KeyringAccount("test", cfg.Servers["test"].URL)
	if err := keyring.Set(config.KeyringService, acc, "tok"); err != nil {
		t.Fatal(err)
	}
	mustCode(t, run(app, "auth", "delete", "test", "--confirm"), 0)
	if _, err := keyring.Get(config.KeyringService, acc); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("keyring entry %q left behind: %v", acc, err)
	}
}

func TestAuthDeleteDoesNotMigrateTokenFile(t *testing.T) {
	app := keyringApp(t)
	cfg, err := app.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	acc := config.KeyringAccount("test", cfg.Servers["test"].URL)
	if err := app.Store.FileBackend().Set("test", "legacy"); err != nil {
		t.Fatal(err)
	}
	r := run(app, "auth", "delete", "test", "--confirm")
	mustCode(t, r, 0)
	if _, err := keyring.Get(config.KeyringService, acc); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("the token file was moved into the keyring: %v", err)
	}
	if _, err := app.Store.FileBackend().Get("test"); !errors.Is(err, config.ErrNoSession) {
		t.Fatalf("token file not removed: %v", err)
	}
	if r.err == "" {
		t.Fatal("the legacy token file counts as a session: the reminder is expected")
	}
}
