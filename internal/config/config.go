// Package config stores profiles, session tokens and pending logins on disk.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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

// Profile describes one server account.
type Profile struct {
	URL        string     `yaml:"url"`
	Team       string     `yaml:"team,omitempty"`
	UserID     string     `yaml:"user_id,omitempty"`
	Username   string     `yaml:"username,omitempty"`
	AuthMethod string     `yaml:"auth_method,omitempty"`
	RateLimit  *RateLimit `yaml:"rate_limit,omitempty"`
}

// RateLimit overrides the default request rate of a profile.
type RateLimit struct {
	PerSecond float64 `yaml:"per_second"`
	Burst     int     `yaml:"burst"`
}

// Upper bounds for a profile's rate_limit, a guard against a typo turning the
// CLI into a load generator: 10x the default rate (1 request/s) and about 7x
// the default burst (3); the defaults live in internal/client.
const (
	MaxPerSecond = 10
	MaxBurst     = 20
)

// Config is the content of config.yaml.
type Config struct {
	Default  string              `yaml:"default,omitempty"`
	Profiles map[string]*Profile `yaml:"profiles,omitempty"`
}

var (
	ErrNoProfile       = errors.New("no profile configured; run `mm-cli auth login <url>`")
	ErrNoSession       = errors.New("not logged in; run `mm-cli auth login`")
	ErrInsecureSession = errors.New("session file is accessible by other users; run `chmod 600` on it")
	ErrNoPending       = errors.New("no login in progress; run `mm-cli auth login <url>` first")
	// ErrInvalidConfig marks a config.yaml that cannot be used as written.
	ErrInvalidConfig = errors.New("invalid configuration")
	// ErrProfileSelection marks a profile choice that the command line has
	// to fix: a missing profile, or several profiles and no default.
	ErrProfileSelection = errors.New("cannot select a profile")
)

// selectionError keeps its own message and matches ErrProfileSelection.
type selectionError struct{ msg string }

func (e *selectionError) Error() string        { return e.msg }
func (e *selectionError) Is(target error) bool { return target == ErrProfileSelection }

func selectionErrorf(format string, args ...any) error {
	return &selectionError{msg: fmt.Sprintf(format, args...)}
}

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidateProfileName rejects names that are unsafe as file names.
func ValidateProfileName(name string) error {
	if !profileNameRe.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use 1-64 letters, digits, '-' or '_'", name)
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

// Select picks a profile: explicit flag, then $MM_PROFILE, then the default,
// then the only one.
func (c *Config) Select(explicit, env string) (string, *Profile, error) {
	name := explicit
	if name == "" {
		name = env
	}
	if name == "" {
		name = c.Default
	}
	if name == "" {
		switch len(c.Profiles) {
		case 0:
			return "", nil, ErrNoProfile
		case 1:
			for n := range c.Profiles {
				name = n
			}
		default:
			names := make([]string, 0, len(c.Profiles))
			for n := range c.Profiles {
				names = append(names, n)
			}
			slices.Sort(names)
			return "", nil, selectionErrorf("several profiles (%v) and no default; pass --profile", names)
		}
	}
	p, ok := c.Profiles[name]
	if !ok {
		return "", nil, selectionErrorf("profile %q not found", name)
	}
	if p == nil {
		return "", nil, fmt.Errorf("%w: profile %q is empty", ErrInvalidConfig, name)
	}
	if err := CheckServerURL(p.URL); err != nil {
		return "", nil, fmt.Errorf("%w: profile %q: %w", ErrInvalidConfig, name, err)
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

// CachePath is the address-resolution cache of a profile.
func (s Store) CachePath(name string) string {
	return filepath.Join(s.CacheDir, name, "resolve.json")
}

// DeleteCache removes the cache directory of a profile. The name is checked
// again here, and the directory must lie strictly inside CacheDir, so that a
// bad name never reaches os.RemoveAll.
func (s Store) DeleteCache(name string) error {
	if err := ValidateProfileName(name); err != nil {
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

// checkedPath validates the profile name before a session or pending file
// path is built from it.
func checkedPath(name string, path func(string) string) (string, error) {
	if err := ValidateProfileName(name); err != nil {
		return "", err
	}
	return path(name), nil
}

// Load reads config.yaml; a missing file yields an empty config.
func (s Store) Load() (*Config, error) {
	c := &Config{Profiles: map[string]*Profile{}}
	data, err := os.ReadFile(s.configPath())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w: %w", s.configPath(), ErrInvalidConfig, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]*Profile{}
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w: %w", s.configPath(), ErrInvalidConfig, err)
	}
	return c, nil
}

// validate rejects profile names that are unsafe as file names and empty
// profile entries. Profile URLs are checked when a profile is selected, so
// that a profile with a bad URL can still be listed and removed.
func (c *Config) validate() error {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	slices.Sort(names)
	var errs []string
	for _, n := range names {
		if err := ValidateProfileName(n); err != nil {
			errs = append(errs, err.Error())
		} else if c.Profiles[n] == nil {
			errs = append(errs, fmt.Sprintf("profile %q is empty; give it a url or remove it", n))
		} else if msg := c.Profiles[n].RateLimit.check(); msg != "" {
			errs = append(errs, fmt.Sprintf("profile %q: %s", n, msg))
		}
	}
	if c.Default != "" {
		if err := ValidateProfileName(c.Default); err != nil {
			errs = append(errs, "default: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
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
// another process started for the same profile in the meantime.
func (s Store) DeletePendingIf(name, clientToken string) error {
	p, err := s.LoadPending(name)
	if errors.Is(err, ErrNoPending) {
		return nil
	}
	if err == nil && p.ClientToken != clientToken {
		return nil
	}
	// An unreadable file is stale state of this profile either way.
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
