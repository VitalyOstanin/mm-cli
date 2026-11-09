// Package config stores saved servers, session tokens and pending logins on
// disk.
package config

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// Server is a saved server: its address and the account signed in there.
type Server struct {
	URL        string     `yaml:"url"`
	Team       string     `yaml:"team,omitempty"`
	UserID     string     `yaml:"user_id,omitempty"`
	Username   string     `yaml:"username,omitempty"`
	AuthMethod string     `yaml:"auth_method,omitempty"`
	RateLimit  *RateLimit `yaml:"rate_limit,omitempty"`
	// UserAgent replaces the User-Agent header mm-cli/<version> in every
	// request to the server, the WebSocket of watch included.
	UserAgent string `yaml:"user_agent,omitempty"`
}

// RateLimit overrides the default request rate of a server.
type RateLimit struct {
	PerSecond float64 `yaml:"per_second"`
	Burst     int     `yaml:"burst"`
}

// Upper bounds for a server's rate_limit, a guard against a typo turning the
// CLI into a load generator: 10x the default rate (1 request/s) and about 7x
// the default burst (3); the defaults live in internal/client.
const (
	MaxPerSecond = 10
	MaxBurst     = 20
)

// Config is the content of config.yaml.
type Config struct {
	Current string             `yaml:"current,omitempty"`
	Servers map[string]*Server `yaml:"servers,omitempty"`
}

var (
	ErrNoServer        = errors.New("no server saved; run `mm-cli auth login <url>`")
	ErrNoSession       = errors.New("not logged in; run `mm-cli auth login`")
	ErrInsecureSession = errors.New("session file is accessible by other users; run `chmod 600` on it")
	ErrNoPending       = errors.New("no login in progress; run `mm-cli auth login <url>` first")
	// ErrInvalidConfig marks a config.yaml that cannot be used as written.
	ErrInvalidConfig = errors.New("invalid configuration")
	// ErrServerSelection marks a server choice that the command line has to
	// fix: an unknown server name, or several servers and no current one.
	ErrServerSelection = errors.New("cannot select a server")
)

// selectionError keeps its own message and matches ErrServerSelection.
type selectionError struct{ msg string }

func (e *selectionError) Error() string        { return e.msg }
func (e *selectionError) Is(target error) bool { return target == ErrServerSelection }

func selectionErrorf(format string, args ...any) error {
	return &selectionError{msg: fmt.Sprintf(format, args...)}
}

var serverNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidateServerName rejects names that are unsafe as file names.
func ValidateServerName(name string) error {
	if !serverNameRe.MatchString(name) {
		return fmt.Errorf("invalid server name %q: use 1-64 letters, digits, '-' or '_'", name)
	}
	return nil
}

// CheckServerURL accepts only a normalized server address that is safe to
// send the session token to: https, or plain http to a loopback host, without
// credentials, a query or a fragment.
func CheckServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("invalid server URL %q", raw)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil {
		return errors.New("the server URL must not contain credentials, a query or a fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return errors.New("refusing plain http for a non-local server: the session token would travel unencrypted")
		}
	default:
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	return nil
}

// Select picks a server: the --server flag, then $MM_SERVER, then the current
// one, then the only one. ErrNoServer when none is saved, whatever the name.
func (c *Config) Select(explicit, env string) (string, *Server, error) {
	// With no server saved, a name cannot help: the user has to sign in.
	if len(c.Servers) == 0 {
		return "", nil, ErrNoServer
	}
	name := explicit
	if name == "" {
		name = env
	}
	if name == "" {
		name = c.Current
	}
	if name == "" {
		switch len(c.Servers) {
		case 0:
			return "", nil, ErrNoServer
		case 1:
			name = c.Names()[0]
		default:
			names := c.Names()
			return "", nil, selectionErrorf("several servers (%v) and no current one; pass --server or run `mm-cli auth set <name>`", names)
		}
	}
	p, ok := c.Servers[name]
	if !ok {
		return "", nil, selectionErrorf("server %q not found; see mm-cli auth list", name)
	}
	if p == nil {
		return "", nil, fmt.Errorf("%w: server %q is empty", ErrInvalidConfig, name)
	}
	if err := CheckServerURL(p.URL); err != nil {
		return "", nil, fmt.Errorf("%w: server %q: %w", ErrInvalidConfig, name, err)
	}
	return name, p, nil
}

// Store is the on-disk location of the configuration.
type Store struct {
	ConfigDir string
	CacheDir  string
}

// DefaultStore uses $MM_CONFIG_DIR or the user configuration directory.
func DefaultStore() (Store, error) {
	if d := os.Getenv("MM_CONFIG_DIR"); d != "" {
		return Store{ConfigDir: d, CacheDir: filepath.Join(d, "cache")}, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return Store{}, err
	}
	c, err := os.UserCacheDir()
	if err != nil {
		return Store{}, err
	}
	return Store{ConfigDir: filepath.Join(d, "mm-cli"), CacheDir: filepath.Join(c, "mm-cli")}, nil
}

// CachePath is the address-resolution cache of a server.
func (s Store) CachePath(name string) string {
	return filepath.Join(s.CacheDir, name, "resolve.json")
}

// DeleteCache removes the cache directory of a server. The name is checked
// again here, and the directory must lie strictly inside CacheDir, so that a
// bad name never reaches os.RemoveAll.
func (s Store) DeleteCache(name string) error {
	if err := ValidateServerName(name); err != nil {
		return err
	}
	base := filepath.Clean(s.CacheDir)
	dir := filepath.Join(base, name)
	if filepath.Dir(dir) != base {
		return fmt.Errorf("cache directory %q is outside %q", dir, base)
	}
	return os.RemoveAll(dir)
}

func (s Store) configPath() string { return filepath.Join(s.ConfigDir, "config.yaml") }

func (s Store) sessionPath(name string) string {
	return filepath.Join(s.ConfigDir, "sessions", name+".token")
}

func (s Store) pendingPath(name string) string {
	return filepath.Join(s.ConfigDir, "sessions", name+".pending")
}

// checkedPath validates the server name before a session or pending file
// path is built from it.
func checkedPath(name string, path func(string) string) (string, error) {
	if err := ValidateServerName(name); err != nil {
		return "", err
	}
	return path(name), nil
}

// Load reads config.yaml; a missing file yields an empty config.
func (s Store) Load() (*Config, error) {
	c := &Config{Servers: map[string]*Server{}}
	data, err := os.ReadFile(s.configPath())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := checkLegacy(data); err != nil {
		return nil, fmt.Errorf("%s: %w: %w", s.configPath(), ErrInvalidConfig, err)
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w: %w", s.configPath(), ErrInvalidConfig, err)
	}
	if c.Servers == nil {
		c.Servers = map[string]*Server{}
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w: %w", s.configPath(), ErrInvalidConfig, err)
	}
	return c, nil
}

// Names returns the names of the saved servers, sorted.
func (c *Config) Names() []string {
	return slices.Sorted(maps.Keys(c.Servers))
}

// validate rejects server names that are unsafe as file names and empty
// server entries. Server URLs are checked when a server is selected, so that
// a server with a bad URL can still be listed and deleted.
func (c *Config) validate() error {
	var errs []string
	for _, n := range c.Names() {
		if err := ValidateServerName(n); err != nil {
			errs = append(errs, err.Error())
		} else if c.Servers[n] == nil {
			errs = append(errs, fmt.Sprintf("server %q is empty; give it a url or remove it", n))
		} else if msg := c.Servers[n].RateLimit.check(); msg != "" {
			errs = append(errs, fmt.Sprintf("server %q: %s", n, msg))
		} else if msg := checkUserAgent(c.Servers[n].UserAgent); msg != "" {
			errs = append(errs, fmt.Sprintf("server %q: %s", n, msg))
		}
	}
	if c.Current != "" {
		if err := ValidateServerName(c.Current); err != nil {
			errs = append(errs, "current: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// checkUserAgent rejects a user_agent that is not a valid header value:
// control characters would split the request, and non-ASCII is not portable.
func checkUserAgent(ua string) string {
	for _, r := range ua {
		if r < 0x20 || r > 0x7e {
			return fmt.Sprintf("user_agent %q: use printable ASCII only", ua)
		}
	}
	return ""
}

// checkLegacy refuses a config.yaml written by mm-cli 0.1, which kept the
// servers under "profiles" and the current one under "default". The entries
// themselves did not change, so renaming the two keys is enough.
func checkLegacy(data []byte) error {
	var top map[string]any
	if yaml.Unmarshal(data, &top) != nil {
		return nil // the full parse reports it
	}
	_, profiles := top["profiles"]
	_, def := top["default"]
	if profiles || def {
		return errors.New(`written by mm-cli 0.1: rename the key "profiles" to "servers" and "default" to "current", or delete the file and log in again`)
	}
	return nil
}

// check reports an out-of-range rate_limit; a nil limit and zero values
// (meaning "use the default") are fine.
func (r *RateLimit) check() string {
	if r == nil {
		return ""
	}
	switch {
	case r.PerSecond < 0:
		return fmt.Sprintf("rate_limit.per_second is negative (%v); use 0..%d", r.PerSecond, MaxPerSecond)
	case r.PerSecond > MaxPerSecond:
		return fmt.Sprintf("rate_limit.per_second is %v; at most %d is allowed", r.PerSecond, MaxPerSecond)
	case r.Burst < 0:
		return fmt.Sprintf("rate_limit.burst is negative (%d); use 0..%d", r.Burst, MaxBurst)
	case r.Burst > MaxBurst:
		return fmt.Sprintf("rate_limit.burst is %d; at most %d is allowed", r.Burst, MaxBurst)
	}
	return ""
}

// Save writes config.yaml atomically.
func (s Store) Save(c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return WriteFileAtomic(s.configPath(), data, 0o600)
}

// SaveSession stores the session token with mode 0600.
func (s Store) SaveSession(name, token string) error {
	p, err := checkedPath(name, s.sessionPath)
	if err != nil {
		return err
	}
	return WriteFileAtomic(p, []byte(token+"\n"), 0o600)
}

// LoadSession reads the session token and refuses files readable by others.
func (s Store) LoadSession(name string) (string, error) {
	p, err := checkedPath(name, s.sessionPath)
	if err != nil {
		return "", err
	}
	data, err := readPrivate(p)
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

// DeleteSession removes the session file; a missing file is not an error.
func (s Store) DeleteSession(name string) error {
	p, err := checkedPath(name, s.sessionPath)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Pending is a login started in one process and finished in another.
type Pending struct {
	URL         string `json:"url"`
	Method      string `json:"method"`
	ClientToken string `json:"client_token"`
	CreatedAt   int64  `json:"created_at"`
	// Team is the --team of auth login, for the process that finishes the
	// login from the browser's link.
	Team string `json:"team,omitempty"`
}

// SavePending stores a login in progress.
func (s Store) SavePending(name string, p Pending) error {
	path, err := checkedPath(name, s.pendingPath)
	if err != nil {
		return err
	}
	// The client token is stored on purpose, in a 0600 file: the login is
	// finished by a second process.
	data, err := json.Marshal(p) //nolint:gosec // see above
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data, 0o600)
}

// LoadPending reads a login in progress.
func (s Store) LoadPending(name string) (Pending, error) {
	var p Pending
	path, err := checkedPath(name, s.pendingPath)
	if err != nil {
		return p, err
	}
	data, err := readPrivate(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, ErrNoPending
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("parse %s: %w", path, err)
	}
	return p, nil
}

// FindPending returns the server name and the login in progress whose client
// token is clientToken; ErrNoPending when there is none.
func (s Store) FindPending(clientToken string) (string, Pending, error) {
	files, err := filepath.Glob(s.pendingPath("*"))
	if err != nil {
		return "", Pending{}, err
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".pending")
		if ValidateServerName(name) != nil {
			continue
		}
		p, err := s.LoadPending(name)
		if err != nil {
			continue // an unreadable file is not this login
		}
		if clientToken != "" && subtle.ConstantTimeCompare([]byte(p.ClientToken), []byte(clientToken)) == 1 {
			return name, p, nil
		}
	}
	return "", Pending{}, ErrNoPending
}

// DeletePending removes a login in progress.
func (s Store) DeletePending(name string) error {
	p, err := checkedPath(name, s.pendingPath)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// DeletePendingIf removes the login in progress only when it is the attempt
// with this client token, so that finishing one login never removes a login
// another process started for the same server in the meantime.
func (s Store) DeletePendingIf(name, clientToken string) error {
	p, err := s.LoadPending(name)
	if errors.Is(err, ErrNoPending) {
		return nil
	}
	if err == nil && p.ClientToken != clientToken {
		return nil
	}
	// An unreadable file is stale state of this server either way.
	return s.DeletePending(name)
}

func readPrivate(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrInsecureSession)
	}
	return os.ReadFile(path)
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// WriteFileAtomic writes through a temporary file and a rename; parent
// directories are created with mode 0700.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
