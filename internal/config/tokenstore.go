package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

// Where the session token lives (docs/adr/0025-session-token-in-os-secret-store.md;
// the account naming is that of docs/adr/0026-token-account-names-server-and-url.md).
const (
	// TokenStorageKeyring keeps session tokens in the OS secret store.
	TokenStorageKeyring = "keyring"
	// TokenStorageFile keeps them in sessions/<name>.token, mode 0600.
	TokenStorageFile = "file"
	// EnvTokenStorage overrides the token_storage key of config.yaml.
	EnvTokenStorage = "MM_TOKEN_STORAGE"
	// KeyringService is the service name under which the store holds tokens;
	// the account is KeyringAccount: the saved server's name and its address.
	KeyringService = "mm-cli"
)

// ErrTokenStore marks a failure of the OS secret store (absent, locked, no
// D-Bus session, unsupported platform). It is never answered with a silent
// fall back to a file: the user chooses the file explicitly.
var ErrTokenStore = errors.New("OS secret store unavailable")

// TokenBackend holds the session tokens of the saved servers.
type TokenBackend interface {
	// Name is TokenStorageKeyring or TokenStorageFile.
	Name() string
	// Get returns ErrNoSession when nothing is stored for the server.
	Get(server string) (string, error)
	Set(server, token string) error
	// Delete treats a missing token as success.
	Delete(server string) error
}

// checkTokenStorage validates a token_storage value; empty means the default.
func checkTokenStorage(v string) error {
	switch v {
	case "", TokenStorageKeyring, TokenStorageFile:
		return nil
	}
	return fmt.Errorf("invalid token storage %q: use %s or %s", v, TokenStorageKeyring, TokenStorageFile)
}

// FileBackend returns the backend that keeps tokens in session files.
func (s Store) FileBackend() TokenBackend { return fileBackend{s} }

// backend picks the token storage: an injected one, else $MM_TOKEN_STORAGE,
// else token_storage of config.yaml, else the OS secret store.
func (s Store) backend() (TokenBackend, error) {
	if s.Tokens != nil {
		return s.Tokens, nil
	}
	mode := strings.TrimSpace(os.Getenv(EnvTokenStorage))
	if mode == "" {
		cfg, err := s.Load()
		if err != nil {
			return nil, err
		}
		mode = cfg.TokenStorage
	} else if err := checkTokenStorage(mode); err != nil {
		return nil, fmt.Errorf("%w: $%s: %w", ErrInvalidConfig, EnvTokenStorage, err)
	}
	if mode == TokenStorageFile {
		return s.FileBackend(), nil
	}
	return keyringBackend{s}, nil
}

// TokenStorage reports the storage in use: "keyring" or "file".
func (s Store) TokenStorage() (string, error) {
	b, err := s.backend()
	if err != nil {
		return "", err
	}
	return b.Name(), nil
}

type fileBackend struct{ s Store }

func (fileBackend) Name() string { return TokenStorageFile }

func (b fileBackend) Get(server string) (string, error) {
	data, err := readPrivate(b.s.sessionPath(server))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoSession
	}
	if err != nil {
		return "", err
	}
	tok := string(trimNewline(data))
	if tok == "" {
		return "", ErrNoSession
	}
	return tok, nil
}

func (b fileBackend) Set(server, token string) error {
	return WriteFileAtomic(b.s.sessionPath(server), []byte(token+"\n"), 0o600)
}

func (b fileBackend) Delete(server string) error {
	err := os.Remove(b.s.sessionPath(server))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// KeyringAccount is the account name of a server's token in the secret store:
// the saved name and the address, as "chat https://chat.example.com". The
// address is part of it so that a token is never read, replaced or sent for
// another address that happens to share the name; the pair stays readable in
// the keyring's own UI.
func KeyringAccount(name, url string) string { return name + " " + url }

// keyringBackend finds the address of a server in config.yaml, or in the
// login in progress while the server is not saved yet.
type keyringBackend struct{ s Store }

func (keyringBackend) Name() string { return TokenStorageKeyring }

// account returns "" when the address of the server is not known.
func (b keyringBackend) account(name string) (string, error) {
	cfg, err := b.s.Load()
	if err != nil {
		return "", err
	}
	if srv := cfg.Servers[name]; srv != nil && srv.URL != "" {
		return KeyringAccount(name, srv.URL), nil
	}
	if p, err := b.s.LoadPending(name); err == nil && p.URL != "" {
		return KeyringAccount(name, p.URL), nil
	}
	return "", nil
}

func (b keyringBackend) Get(server string) (string, error) {
	acc, err := b.account(server)
	if err != nil {
		return "", err
	}
	if acc == "" {
		return "", ErrNoSession
	}
	tok, err := keyring.Get(KeyringService, acc)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNoSession
	}
	if err != nil {
		return "", StoreError(err)
	}
	if tok == "" {
		return "", ErrNoSession
	}
	return tok, nil
}

func (b keyringBackend) Set(server, token string) error {
	acc, err := b.account(server)
	if err != nil {
		return err
	}
	if acc == "" {
		return fmt.Errorf("server %s has no saved address to bind its token to", server)
	}
	if err := keyring.Set(KeyringService, acc, token); err != nil {
		return StoreError(err)
	}
	return nil
}

// Delete treats a server whose address is gone from config.yaml as having no
// token: the entry cannot be named any more.
func (b keyringBackend) Delete(server string) error {
	acc, err := b.account(server)
	if err != nil || acc == "" {
		return err
	}
	err = keyring.Delete(KeyringService, acc)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return StoreError(err)
}

// StoreError wraps a secret store failure into ErrTokenStore. The message names
// the cause and both ways out, so that it alone is enough for a person or an
// agent to carry on. It is exported so that the test double in configtest
// reports failures with the same text.
func StoreError(err error) error {
	return fmt.Errorf("%w: %w; start or unlock the secret store, or keep tokens in files with "+
		"`token_storage: file` in config.yaml or MM_TOKEN_STORAGE=file", ErrTokenStore, err)
}

// SaveSession stores the session token in the selected storage. After a
// successful save into the secret store a leftover token file is removed, so
// that no plain copy stays behind.
func (s Store) SaveSession(name, token string) error {
	if err := ValidateServerName(name); err != nil {
		return err
	}
	b, err := s.backend()
	if err != nil {
		return err
	}
	if err := b.Set(name, token); err != nil {
		return err
	}
	if b.Name() != TokenStorageFile {
		return s.FileBackend().Delete(name)
	}
	return nil
}

// LoadSession reads the session token. With the secret store, a token file is
// moved into it first, and that file wins over an entry already there: the
// store's own writes remove the file, so a file that is present was written
// later, in file mode (keyring -> file -> keyring would otherwise bring the
// old token back). File mode never touches the secret store, so its entry
// is not cleaned up there; it is replaced by this move or removed by a
// logout while the store is selected (ADR 0026).
func (s Store) LoadSession(name string) (string, error) {
	if err := ValidateServerName(name); err != nil {
		return "", err
	}
	b, err := s.backend()
	if err != nil {
		return "", err
	}
	if b.Name() == TokenStorageFile {
		return b.Get(name)
	}
	tok, err := s.migrate(b, name)
	if !errors.Is(err, ErrNoSession) {
		return tok, err
	}
	return b.Get(name)
}

// HasSession reports whether a token is saved for the server, in the selected
// storage or in a token file. Unlike LoadSession it never moves a file into
// the secret store and never writes: a caller that only needs to know, such as
// the reminder after a deletion, must not change what it looks at. A storage
// that cannot be read is an error, not "no session".
func (s Store) HasSession(name string) (bool, error) {
	if err := ValidateServerName(name); err != nil {
		return false, err
	}
	_, err := s.FileBackend().Get(name)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, ErrNoSession) {
		return false, err
	}
	b, err := s.backend()
	if err != nil {
		return false, err
	}
	if b.Name() == TokenStorageFile {
		return false, nil // the file backend was just asked
	}
	_, err = b.Get(name)
	if errors.Is(err, ErrNoSession) {
		return false, nil
	}
	return err == nil, err
}

// migrate moves sessions/<name>.token into b: write, read back and compare,
// and only then delete the file. Any failure leaves the file in place.
func (s Store) migrate(b TokenBackend, name string) (string, error) {
	f := s.FileBackend()
	tok, err := f.Get(name)
	if err != nil {
		return "", err // ErrNoSession when there is no file
	}
	// Every failure before the file is deleted reports the same outcome.
	kept := func(err error) error {
		return fmt.Errorf("moving the token file of server %s: %w (the file is kept)", name, err)
	}
	if err := b.Set(name, tok); err != nil {
		return "", kept(err)
	}
	back, err := b.Get(name)
	if err != nil || back != tok {
		_ = b.Delete(name)
		if err == nil {
			err = errors.New("the stored token differs from the file")
		}
		return "", kept(err)
	}
	// The token is already stored and verified, so a file that cannot be
	// removed is only a warning: failing here would lock the user out.
	if err := f.Delete(name); err != nil && s.OnWarn != nil {
		s.OnWarn(name, fmt.Errorf("the token is in the secret store, but its file could not be removed: %w", err))
	}
	if s.OnMigrate != nil {
		s.OnMigrate(name)
	}
	return tok, nil
}

// DeleteSession removes the token from the selected storage and any token
// file; a missing token is not an error. The file is removed even when the
// secret store fails.
func (s Store) DeleteSession(name string) error {
	if err := ValidateServerName(name); err != nil {
		return err
	}
	b, err := s.backend()
	if err != nil {
		return err
	}
	err = b.Delete(name)
	if b.Name() != TokenStorageFile {
		err = errors.Join(err, s.FileBackend().Delete(name))
	}
	return err
}
