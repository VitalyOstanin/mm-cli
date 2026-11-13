package config

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// keyring -> file -> keyring: the token saved while files were selected is
// the newer one and must win over the entry left in the secret store.
func TestSwitchBackToKeyringPrefersNewerFile(t *testing.T) {
	keyring.MockInit()
	s := keyringStore(t, "https://chat.example.com")
	if err := s.SaveSession("chat", "old"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvTokenStorage, TokenStorageFile)
	if err := s.SaveSession("chat", "new"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvTokenStorage, TokenStorageKeyring)
	got, err := s.LoadSession("chat")
	if err != nil || got != "new" {
		t.Fatalf("got %q, %v; want the file token", got, err)
	}
	if _, err := os.Stat(s.sessionPath("chat")); !os.IsNotExist(err) {
		t.Fatalf("file kept after the move: %v", err)
	}
	if got, err := keyring.Get(KeyringService, KeyringAccount("chat", "https://chat.example.com")); err != nil || got != "new" {
		t.Fatalf("store holds %q, %v", got, err)
	}
}
