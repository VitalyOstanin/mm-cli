package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/config/configtest"
)

func testStore(t *testing.T) config.Store {
	t.Helper()
	d := t.TempDir()
	return config.Store{ConfigDir: filepath.Join(d, "config"), CacheDir: filepath.Join(d, "cache"), Tokens: configtest.NewMemoryBackend()}
}

func TestSelect(t *testing.T) {
	two := &config.Config{Servers: map[string]*config.Server{"a": {URL: "https://a.example.com"}, "b": {URL: "https://b.example.com"}}}
	one := &config.Config{Servers: map[string]*config.Server{"a": {URL: "https://a.example.com"}}}
	withDefault := &config.Config{Current: "b", Servers: two.Servers}
	tests := []struct {
		name          string
		cfg           *config.Config
		explicit, env string
		want          string
		wantErr       string
	}{
		{"flag wins", withDefault, "a", "b", "a", ""},
		{"env before default", withDefault, "", "a", "a", ""},
		{"default", withDefault, "", "", "b", ""},
		{"only one", one, "", "", "a", ""},
		{"none", &config.Config{Servers: map[string]*config.Server{}}, "", "", "", "no server saved"},
		{"none, named", &config.Config{Servers: map[string]*config.Server{}}, "work", "", "", "no server saved"},
		{"none, stale current", &config.Config{Current: "work", Servers: map[string]*config.Server{}}, "", "", "", "no server saved"},
		{"several without current", two, "", "", "", "several servers"},
		{"unknown", two, "zzz", "", "", "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, p, err := tt.cfg.Select(tt.explicit, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || name != tt.want || p == nil {
				t.Fatalf("got %q %v %v, want %q", name, p, err, tt.want)
			}
		})
	}
	if _, _, err := (&config.Config{}).Select("", ""); !errors.Is(err, config.ErrNoServer) {
		t.Fatalf("empty config: %v", err)
	}
}

func TestSaveLoad(t *testing.T) {
	s := testStore(t)
	c, err := s.Load()
	if err != nil || len(c.Servers) != 0 {
		t.Fatalf("missing file: %v %v", c, err)
	}
	c.Current = "work"
	c.Servers["work"] = &config.Server{URL: "https://chat.example.com", Team: "myteam", UserID: "u1", Username: "alice",
		AuthMethod: "saml", RateLimit: &config.RateLimit{PerSecond: 0.5, Burst: 2}}
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := got.Servers["work"]
	if got.Current != "work" || p == nil || p.Team != "myteam" || p.RateLimit == nil || p.RateLimit.PerSecond != 0.5 || p.RateLimit.Burst != 2 {
		t.Fatalf("round trip: %+v %+v", got, p)
	}
}

func TestSession(t *testing.T) {
	s := testStore(t)
	s.Tokens = s.FileBackend()
	if _, err := s.LoadSession("work"); !errors.Is(err, config.ErrNoSession) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.SaveSession("work", "tok123"); err != nil {
		t.Fatal(err)
	}
	tok, err := s.LoadSession("work")
	if err != nil || tok != "tok123" {
		t.Fatalf("got %q %v", tok, err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(config.SessionPath(s, "work"))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
		dir, err := os.Stat(filepath.Dir(config.SessionPath(s, "work")))
		if err != nil {
			t.Fatal(err)
		}
		if dir.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v", dir.Mode().Perm())
		}
		if err := os.Chmod(config.SessionPath(s, "work"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.LoadSession("work"); !errors.Is(err, config.ErrInsecureSession) {
			t.Fatalf("insecure: %v", err)
		}
	}
	if err := s.DeleteSession("work"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("work"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestPending(t *testing.T) {
	s := testStore(t)
	if _, err := s.LoadPending("work"); !errors.Is(err, config.ErrNoPending) {
		t.Fatalf("missing: %v", err)
	}
	want := config.Pending{URL: "https://chat.example.com", Method: "saml", ClientToken: "ct", CreatedAt: 42}
	if err := s.SavePending("work", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadPending("work")
	if err != nil || got != want {
		t.Fatalf("got %+v %v", got, err)
	}
	if err := s.DeletePending("work"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateServerName(t *testing.T) {
	for _, ok := range []string{"work", "chat-example-com", "a_b", strings.Repeat("x", 64)} {
		if err := config.ValidateServerName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "a.b", strings.Repeat("x", 65)} {
		if err := config.ValidateServerName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCachePath(t *testing.T) {
	s := testStore(t)
	if got := s.CachePath("work"); got != filepath.Join(s.CacheDir, "work", "resolve.json") {
		t.Fatal(got)
	}
}

func TestWriteFileAtomicLeavesNoTemp(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "sub", "f")
	if err := config.WriteFileAtomic(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries: %v", entries)
	}
}

func writeConfig(t *testing.T, s config.Store, body string) {
	t.Helper()
	if err := config.WriteFileAtomic(config.ConfigPath(s), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsEmptyServer(t *testing.T) {
	for _, body := range []string{"servers:\n  work:\n", "servers: {work: }\n", "servers:\n  work: null\n"} {
		s := testStore(t)
		writeConfig(t, s, body)
		_, err := s.Load()
		if !errors.Is(err, config.ErrInvalidConfig) || !strings.Contains(err.Error(), `"work"`) {
			t.Errorf("%q: err = %v", body, err)
		}
	}
}

// A config.yaml of mm-cli 0.1 ("profiles", "default") is refused with a hint
// to rename the keys.
func TestLoadRejectsLegacyKeys(t *testing.T) {
	for _, body := range []string{
		"default: work\nprofiles:\n  work: {url: https://chat.example.com}\n",
		"profiles:\n  work: {url: https://chat.example.com}\n",
		"default: work\n",
	} {
		s := testStore(t)
		writeConfig(t, s, body)
		_, err := s.Load()
		if !errors.Is(err, config.ErrInvalidConfig) || !strings.Contains(err.Error(), `rename the key "profiles" to "servers"`) ||
			// auth login loads the config too: logging in again needs the
			// file gone first.
			!strings.Contains(err.Error(), "delete the file and log in again") {
			t.Errorf("%q: err = %v", body, err)
		}
	}
}

func TestLoadRejectsInvalidServerNames(t *testing.T) {
	for _, body := range []string{
		"servers:\n  \"..\": {url: https://chat.example.com}\n",
		"servers:\n  \"../x\": {url: https://chat.example.com}\n",
		"servers:\n  \"\": {url: https://chat.example.com}\n",
		"current: \"../x\"\nservers:\n  work: {url: https://chat.example.com}\n",
	} {
		s := testStore(t)
		writeConfig(t, s, body)
		if _, err := s.Load(); !errors.Is(err, config.ErrInvalidConfig) {
			t.Errorf("%q: err = %v", body, err)
		}
	}
}

func TestLoadRateLimitBounds(t *testing.T) {
	cases := []struct {
		name    string
		rate    string
		wantErr []string // empty: must load
	}{
		{"per_second above the limit", "per_second: 1000\n      burst: 3", []string{`"work"`, "per_second", "at most 10"}},
		{"burst above the limit", "per_second: 1\n      burst: 500", []string{`"work"`, "burst", "at most 20"}},
		{"negative per_second", "per_second: -1\n      burst: 3", []string{`"work"`, "per_second", "negative"}},
		{"negative burst", "per_second: 1\n      burst: -2", []string{`"work"`, "burst", "negative"}},
		{"at the limits", "per_second: 10\n      burst: 20", nil},
		{"defaults", "per_second: 1\n      burst: 3", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			writeConfig(t, s, "servers:\n  work:\n    url: https://chat.example.com\n    rate_limit:\n      "+tc.rate+"\n")
			_, err := s.Load()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, config.ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestLoadParseErrorIsInvalidConfig(t *testing.T) {
	s := testStore(t)
	writeConfig(t, s, "servers: [\n")
	if _, err := s.Load(); !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteCacheStaysInsideCacheDir(t *testing.T) {
	s := testStore(t)
	marker := filepath.Join(s.CacheDir, "keep")
	if err := config.WriteFileAtomic(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(filepath.Dir(s.CacheDir), "sibling")
	if err := config.WriteFileAtomic(filepath.Join(sibling, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"..", "", ".", "../sibling", "a/../.."} {
		if err := s.DeleteCache(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cache dir damaged: %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling removed: %v", err)
	}
}

func TestSessionFilesRejectInvalidNames(t *testing.T) {
	s := testStore(t)
	if err := s.SaveSession("../x", "tok"); err == nil {
		t.Error("SaveSession accepted ../x")
	}
	if _, err := s.LoadSession("../x"); err == nil || errors.Is(err, config.ErrNoSession) {
		t.Errorf("LoadSession: %v", err)
	}
	if err := s.DeleteSession(".."); err == nil {
		t.Error("DeleteSession accepted ..")
	}
	if err := s.SavePending("../x", config.Pending{}); err == nil {
		t.Error("SavePending accepted ../x")
	}
	if _, err := s.LoadPending("../x"); err == nil || errors.Is(err, config.ErrNoPending) {
		t.Errorf("LoadPending: %v", err)
	}
	if err := s.DeletePending(".."); err == nil {
		t.Error("DeletePending accepted ..")
	}
	if _, err := os.Stat(filepath.Join(s.ConfigDir, "x.token")); !os.IsNotExist(err) {
		t.Fatalf("file written outside sessions/: %v", err)
	}
}

func TestSelectChecksURL(t *testing.T) {
	for _, u := range []string{"http://chat.example.com", "ftp://chat.example.com", "", "https://alice:pw@chat.example.com"} {
		c := &config.Config{Servers: map[string]*config.Server{"work": {URL: u}}}
		if _, _, err := c.Select("", ""); !errors.Is(err, config.ErrInvalidConfig) {
			t.Errorf("%q: err = %v", u, err)
		}
	}
	for _, u := range []string{"https://chat.example.com", "https://chat.example.com/mm", "http://localhost:8065", "http://127.0.0.1:8065", "http://[::1]:8065"} {
		c := &config.Config{Servers: map[string]*config.Server{"work": {URL: u}}}
		if _, _, err := c.Select("", ""); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
}

func TestCheckServerURL(t *testing.T) {
	for _, u := range []string{"https://chat.example.com", "http://localhost", "http://127.0.0.1:8065", "http://[::1]:8065"} {
		if err := config.CheckServerURL(u); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
	for _, u := range []string{"http://chat.example.com", "chat.example.com", "https://", "https://chat.example.com/?a=1", "https://chat.example.com/#f", "https://alice@chat.example.com", "ws://chat.example.com"} {
		if err := config.CheckServerURL(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestSelectErrorsAreServerSelection(t *testing.T) {
	c := &config.Config{Servers: map[string]*config.Server{
		"a": {URL: "https://a.example.com"},
		"b": {URL: "https://b.example.com"},
	}}
	if _, _, err := c.Select("missing", ""); !errors.Is(err, config.ErrServerSelection) || err.Error() != `server "missing" not found; see mm-cli auth list` {
		t.Fatalf("not found: %v", err)
	}
	if _, _, err := c.Select("", ""); !errors.Is(err, config.ErrServerSelection) || !strings.Contains(err.Error(), "several servers") {
		t.Fatalf("several: %v", err)
	}
}

func TestDeletePendingIf(t *testing.T) {
	s := testStore(t)
	p := config.Pending{URL: "https://chat.example.com", Method: "saml", ClientToken: "theirs", CreatedAt: 42}
	if err := s.SavePending("work", p); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePendingIf("work", "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadPending("work"); err != nil {
		t.Fatalf("another attempt's pending login must stay: %v", err)
	}
	if err := s.DeletePendingIf("work", "theirs"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadPending("work"); !errors.Is(err, config.ErrNoPending) {
		t.Fatalf("own pending login must be removed: %v", err)
	}
	if err := s.DeletePendingIf("work", "theirs"); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}

func TestFindPending(t *testing.T) {
	s := testStore(t)
	for name, ct := range map[string]string{"work": "dev-a", "home": "dev-b"} {
		if err := s.SavePending(name, config.Pending{URL: "https://chat.example.com", ClientToken: ct, Team: "myteam"}); err != nil {
			t.Fatal(err)
		}
	}
	name, p, err := s.FindPending("dev-b")
	if err != nil || name != "home" || p.Team != "myteam" {
		t.Fatalf("%q %+v %v", name, p, err)
	}
	for _, ct := range []string{"dev-c", ""} {
		if _, _, err := s.FindPending(ct); !errors.Is(err, config.ErrNoPending) {
			t.Errorf("%q: %v", ct, err)
		}
	}
}
