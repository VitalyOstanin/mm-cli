package config

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

// keyringStore is a store whose backend is the real keyring code path over
// the library's in-memory mock, so no real secret store is touched.
func keyringStore(t *testing.T, url string) Store {
	t.Helper()
	t.Setenv(EnvTokenStorage, TokenStorageKeyring)
	d := t.TempDir()
	s := Store{ConfigDir: filepath.Join(d, "config"), CacheDir: filepath.Join(d, "cache")}
	if url != "" {
		if err := s.Save(&Config{Servers: map[string]*Server{"chat": {URL: url}}}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestKeyringAccount(t *testing.T) {
	if got := KeyringAccount("chat", "https://chat.example.com"); got != "chat https://chat.example.com" {
		t.Errorf("account = %q", got)
	}
}

// Two configurations may both call a server "chat" while pointing it at
// different addresses; a token saved for one must never be read, replaced
// or sent for the other.
func TestKeyringTokenIsBoundToServerURL(t *testing.T) {
	keyring.MockInit()
	a := keyringStore(t, "https://chat.example.com")
	b := keyringStore(t, "https://other.example.com")
	if err := a.SaveSession("chat", "token-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.LoadSession("chat"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("other URL read the token: %v", err)
	}
	if err := b.SaveSession("chat", "token-b"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.LoadSession("chat"); err != nil || got != "token-a" {
		t.Fatalf("token of the first URL = %q, %v", got, err)
	}
	if err := b.DeleteSession("chat"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.LoadSession("chat"); err != nil || got != "token-a" {
		t.Fatalf("delete for the other URL removed it: %q, %v", got, err)
	}
	// The same server in a second profile shares the entry.
	c := keyringStore(t, "https://chat.example.com")
	if got, err := c.LoadSession("chat"); err != nil || got != "token-a" {
		t.Fatalf("same URL, second profile = %q, %v", got, err)
	}
}

// During a login the server is not in config.yaml yet; its address comes
// from the pending login.
func TestKeyringSaveDuringLoginUsesPendingURL(t *testing.T) {
	keyring.MockInit()
	s := keyringStore(t, "")
	if err := s.SaveSession("chat", "t"); err == nil {
		t.Fatal("saved a token for a server without an address")
	}
	if err := s.SavePending("chat", Pending{URL: "https://chat.example.com", ClientToken: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession("chat", "t"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LoadSession("chat"); err != nil || got != "t" {
		t.Fatalf("got %q, %v", got, err)
	}
}
