// Package configtest holds test doubles for the config package. Production
// code must not import it, so that the doubles never ship in the binary.
package configtest

import (
	"sync"

	"github.com/VitalyOstanin/mm-cli/internal/config"
)

// MemoryBackend is an in-memory token storage that stands in for the OS
// secret store in tests, so that they never touch the real one.
type MemoryBackend struct {
	mu     sync.Mutex
	tokens map[string]string
	// Err, when set, fails every call as an unavailable store would.
	Err error
	// Corrupt, when set, makes Get return a different token than was stored.
	Corrupt bool
}

// NewMemoryBackend returns an empty storage that reports itself as the
// secret store.
func NewMemoryBackend() *MemoryBackend { return &MemoryBackend{tokens: map[string]string{}} }

func (*MemoryBackend) Name() string { return config.TokenStorageKeyring }

func (m *MemoryBackend) Get(server string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return "", config.StoreError(m.Err)
	}
	tok, ok := m.tokens[server]
	if !ok || tok == "" {
		return "", config.ErrNoSession
	}
	if m.Corrupt {
		return tok + "x", nil
	}
	return tok, nil
}

func (m *MemoryBackend) Set(server, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return config.StoreError(m.Err)
	}
	m.tokens[server] = token
	return nil
}

func (m *MemoryBackend) Delete(server string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return config.StoreError(m.Err)
	}
	delete(m.tokens, server)
	return nil
}
