package config_test

import (
	"errors"
	"os"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/config/configtest"
)

func TestHasSession(t *testing.T) {
	// Injected backend (MemoryBackend): file-only, store-only, none.
	s := testStore(t)
	if ok, err := s.HasSession("work"); ok || err != nil {
		t.Fatalf("none: %v %v", ok, err)
	}
	if err := s.FileBackend().Set("work", "old"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HasSession("work"); !ok || err != nil {
		t.Fatalf("file only: %v %v", ok, err)
	}
	if _, err := s.Tokens.Get("work"); !errors.Is(err, config.ErrNoSession) {
		t.Fatalf("HasSession wrote to the store: %v", err)
	}
	if _, err := os.Stat(config.SessionPath(s, "work")); err != nil {
		t.Fatalf("file removed: %v", err)
	}
	if err := s.Tokens.Set("home", "tok"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HasSession("home"); !ok || err != nil {
		t.Fatalf("store only: %v %v", ok, err)
	}
	if _, err := s.HasSession("bad/name"); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestHasSessionStoreError(t *testing.T) {
	s := testStore(t)
	s.Tokens.(*configtest.MemoryBackend).Err = errors.New("locked")
	if ok, err := s.HasSession("work"); ok || err == nil {
		t.Fatalf("got %v %v", ok, err)
	}
}

// With the real keyring backend (mocked) a token file is only looked at.
func TestHasSessionKeyringBackend(t *testing.T) {
	keyring.MockInit()
	s := settingsStore(t)
	t.Setenv(config.EnvTokenStorage, config.TokenStorageKeyring)
	if err := s.Save(&config.Config{Servers: map[string]*config.Server{"work": {URL: "https://chat.example.com"}}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HasSession("work"); ok || err != nil {
		t.Fatalf("none: %v %v", ok, err)
	}
	if err := s.FileBackend().Set("work", "old"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HasSession("work"); !ok || err != nil {
		t.Fatalf("file only: %v %v", ok, err)
	}
	if _, err := keyring.Get(config.KeyringService, config.KeyringAccount("work", "https://chat.example.com")); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("HasSession wrote to the secret store: %v", err)
	}
	if err := s.FileBackend().Delete("work"); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(config.KeyringService, config.KeyringAccount("work", "https://chat.example.com"), "tok"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HasSession("work"); !ok || err != nil {
		t.Fatalf("store only: %v %v", ok, err)
	}
}
