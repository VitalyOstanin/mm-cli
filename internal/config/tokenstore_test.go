package config_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/config/configtest"
)

// settingsStore is a store whose backend is picked by the settings, as in
// production. Tests using it must choose "file" or fail before the real
// secret store would be used.
func settingsStore(t *testing.T) config.Store {
	t.Helper()
	s := testStore(t)
	s.Tokens = nil
	return s
}

func TestBackendSelection(t *testing.T) {
	cases := []struct {
		name, env, cfg, want string
	}{
		{"default", "", "", config.TokenStorageKeyring},
		{"config file", "", config.TokenStorageFile, config.TokenStorageFile},
		{"config keyring", "", config.TokenStorageKeyring, config.TokenStorageKeyring},
		{"env overrides config", config.TokenStorageFile, config.TokenStorageKeyring, config.TokenStorageFile},
		{"env keyring overrides config file", config.TokenStorageKeyring, config.TokenStorageFile, config.TokenStorageKeyring},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := settingsStore(t)
			t.Setenv(config.EnvTokenStorage, c.env)
			if c.cfg != "" {
				if err := s.Save(&config.Config{TokenStorage: c.cfg}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.TokenStorage()
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

func TestBackendSelectionInvalid(t *testing.T) {
	s := settingsStore(t)
	t.Setenv(config.EnvTokenStorage, "vault")
	_, err := s.TokenStorage()
	if !errors.Is(err, config.ErrInvalidConfig) || !strings.Contains(err.Error(), "keyring or file") {
		t.Fatalf("env: %v", err)
	}
	t.Setenv(config.EnvTokenStorage, "")
	if err := s.Save(&config.Config{TokenStorage: "vault"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, config.ErrInvalidConfig) || !strings.Contains(err.Error(), "keyring or file") {
		t.Fatalf("config: %v", err)
	}
	if _, err := s.TokenStorage(); !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("config via backend: %v", err)
	}
}

func TestSessionInSecretStore(t *testing.T) {
	s := testStore(t)
	if err := s.SaveSession("work", "tok1"); err != nil {
		t.Fatal(err)
	}
	if tok, err := s.LoadSession("work"); err != nil || tok != "tok1" {
		t.Fatalf("got %q %v", tok, err)
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); !os.IsNotExist(err) {
		t.Fatalf("token file written: %v", err)
	}
	if err := s.DeleteSession("work"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("work"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := s.LoadSession("work"); !errors.Is(err, config.ErrNoSession) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestSaveSessionStoreUnavailable(t *testing.T) {
	s := testStore(t)
	s.Tokens.(*configtest.MemoryBackend).Err = errors.New("no D-Bus session")
	err := s.SaveSession("work", "tok")
	if !errors.Is(err, config.ErrTokenStore) {
		t.Fatalf("got %v", err)
	}
	for _, want := range []string{"no D-Bus session", "token_storage: file", "MM_TOKEN_STORAGE=file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %v", want, err)
		}
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); !os.IsNotExist(err) {
		t.Fatalf("a file was written as a fallback: %v", err)
	}
}

func TestMigrateTokenFile(t *testing.T) {
	s := testStore(t)
	if err := s.FileBackend().Set("work", "old"); err != nil {
		t.Fatal(err)
	}
	var moved []string
	s.OnMigrate = func(n string) { moved = append(moved, n) }
	tok, err := s.LoadSession("work")
	if err != nil || tok != "old" {
		t.Fatalf("got %q %v", tok, err)
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); !os.IsNotExist(err) {
		t.Fatalf("file not deleted: %v", err)
	}
	if got, _ := s.Tokens.Get("work"); got != "old" {
		t.Fatalf("store holds %q", got)
	}
	if len(moved) != 1 || moved[0] != "work" {
		t.Fatalf("OnMigrate calls: %v", moved)
	}
	if _, err := s.LoadSession("work"); err != nil || len(moved) != 1 {
		t.Fatalf("second read: %v, calls %v", err, moved)
	}
}

// The file is the only copy until the stored token reads back equal.
func TestMigrateKeepsFileUntilReadBackMatches(t *testing.T) {
	s := testStore(t)
	if err := s.FileBackend().Set("work", "old"); err != nil {
		t.Fatal(err)
	}
	s.Tokens.(*configtest.MemoryBackend).Corrupt = true
	if _, err := s.LoadSession("work"); err == nil || !strings.Contains(err.Error(), "file is kept") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); err != nil {
		t.Fatalf("file lost on a mismatch: %v", err)
	}
}

func TestMigrateStoreUnavailableKeepsFile(t *testing.T) {
	s := testStore(t)
	if err := s.FileBackend().Set("work", "old"); err != nil {
		t.Fatal(err)
	}
	s.Tokens.(*configtest.MemoryBackend).Err = errors.New("collection is locked")
	_, err := s.LoadSession("work")
	if !errors.Is(err, config.ErrTokenStore) || !strings.Contains(err.Error(), "MM_TOKEN_STORAGE=file") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); err != nil {
		t.Fatalf("file lost: %v", err)
	}
}

func TestDeleteSessionRemovesLeftoverFile(t *testing.T) {
	s := testStore(t)
	if err := s.SaveSession("work", "new"); err != nil {
		t.Fatal(err)
	}
	if err := s.FileBackend().Set("work", "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("work"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); !os.IsNotExist(err) {
		t.Fatalf("file left: %v", err)
	}
	if _, err := s.Tokens.Get("work"); !errors.Is(err, config.ErrNoSession) {
		t.Fatalf("store keeps the token: %v", err)
	}
}

func TestSaveSessionRemovesLeftoverFile(t *testing.T) {
	s := testStore(t)
	if err := s.FileBackend().Set("work", "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession("work", "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); !os.IsNotExist(err) {
		t.Fatalf("plain copy left: %v", err)
	}
}

func TestFileModeNeverTouchesSecretStore(t *testing.T) {
	s := testStore(t)
	mem := s.Tokens.(*configtest.MemoryBackend)
	mem.Err = errors.New("must not be used")
	s.Tokens = nil
	t.Setenv(config.EnvTokenStorage, config.TokenStorageFile)
	if err := s.SaveSession("work", "tok"); err != nil {
		t.Fatal(err)
	}
	if tok, err := s.LoadSession("work"); err != nil || tok != "tok" {
		t.Fatalf("got %q %v", tok, err)
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("work"); err != nil {
		t.Fatal(err)
	}
}
