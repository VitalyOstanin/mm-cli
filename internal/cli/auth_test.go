package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
	"github.com/VitalyOstanin/mm-cli/internal/resolve"
)

// loginServer serves the endpoints of the login flow. The desktop token "st"
// yields the session token "sess".
func loginServer(t *testing.T, ssoKeys map[string]string, teams ...string) *fakemm.Server {
	t.Helper()
	s := fakemm.New(t)
	s.JSON(http.MethodGet, "/api/v4/config/client", 200, ssoKeys)
	s.Handle(http.MethodPost, "/api/v4/users/login/desktop_token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["token"] != "st" {
			fakemm.AppError(w, http.StatusUnauthorized, "bad token")
			return
		}
		w.Header().Set("Token", "sess")
		fakemm.WriteJSON(w, 200, &model.User{Id: meID, Username: "alice"})
	})
	s.Handle(http.MethodGet, "/api/v4/users/me", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.EqualFold(auth, "Bearer sess") && !strings.EqualFold(auth, "Bearer pat") {
			fakemm.AppError(w, http.StatusUnauthorized, "no session")
			return
		}
		w.Header().Set("X-Version-Id", "10.11.0")
		fakemm.WriteJSON(w, 200, &model.User{Id: meID, Username: "alice"})
	})
	var ts []*model.Team
	for _, n := range teams {
		ts = append(ts, &model.Team{Id: fakemm.ID(n), Name: n})
	}
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, ts)
	return s
}

var desktopTokenRe = regexp.MustCompile(`desktop_token=([0-9a-f]{64})`)

func callbackFor(t *testing.T, s *fakemm.Server, loginURL string) string {
	t.Helper()
	m := desktopTokenRe.FindStringSubmatch(loginURL)
	if m == nil {
		t.Fatalf("no desktop_token in %q", loginURL)
	}
	return fmt.Sprintf("%s/login/desktop?client_token=%s&server_token=st", s.URL, url.QueryEscape(m[1]))
}

func TestLoginTwoSteps(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
	app := newEmptyApp(t)
	var opened string
	app.OpenBrowser = func(u string) error { opened = u; return nil }
	r := run(app, "auth", "login", s.URL, "--profile", "work")
	mustCode(t, r, 0)
	if !strings.HasPrefix(opened, s.URL+"/login/sso/saml?desktop_token=") || !strings.Contains(r.err, opened) || !strings.Contains(r.err, "--callback") {
		t.Fatalf("opened %q, stderr %s", opened, r.err)
	}
	if _, err := app.Store.LoadSession("work"); err == nil {
		t.Fatal("no session expected before --callback")
	}
	r = run(app, "auth", "login", "--profile", "work", "--callback", callbackFor(t, s, opened))
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "as @alice") || !strings.Contains(r.out, "team myteam") {
		t.Fatal(r.out)
	}
	tok, err := app.Store.LoadSession("work")
	if err != nil || tok != "sess" {
		t.Fatalf("session %q %v", tok, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(app.Store.ConfigDir + "/sessions/work.token")
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
	}
	cfg, _ := app.Store.Load()
	p := cfg.Profiles["work"]
	if cfg.Default != "work" || p.UserID != meID || p.Team != "myteam" || p.AuthMethod != "saml" {
		t.Fatalf("%+v %+v", cfg, p)
	}
	mustCode(t, run(app, "auth", "login", "--profile", "work", "--callback", callbackFor(t, s, opened)), clierr.CodeUsage)
}

func TestLoginOnTerminal(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "a", "b")
	app := newEmptyApp(t)
	app.IsTerminal = func() bool { return true }
	pr, pw := io.Pipe()
	app.OpenBrowser = func(u string) error {
		go func() { _, _ = fmt.Fprintln(pw, callbackFor(t, s, u)) }()
		return nil
	}
	app.In = pr
	r := run(app, "auth", "login", s.URL)
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "Several teams (a, b)") {
		t.Fatal(r.out)
	}
	cfg, _ := app.Store.Load()
	if len(cfg.Profiles) != 1 || cfg.Default == "" {
		t.Fatalf("%+v", cfg)
	}
}

func TestLoginNoBrowser(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
	app := newEmptyApp(t)
	r := run(app, "auth", "login", s.URL, "--no-browser", "--profile", "work")
	mustCode(t, r, 0)
	if !strings.Contains(r.err, "desktop_token=") {
		t.Fatal(r.err)
	}
}

func TestLoginWrongClientToken(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
	app := newEmptyApp(t)
	app.OpenBrowser = func(string) error { return nil }
	mustCode(t, run(app, "auth", "login", s.URL, "--profile", "work"), 0)
	cb := s.URL + "/login/desktop?client_token=" + strings.Repeat("0", 64) + "&server_token=st"
	mustCode(t, run(app, "auth", "login", "--profile", "work", "--callback", cb), clierr.CodeUsage)
	if _, err := app.Store.LoadSession("work"); err == nil {
		t.Fatal("session must not exist")
	}
	if s.Count(http.MethodPost, "/api/v4/users/login/desktop_token") != 0 {
		t.Fatal("the token must not reach the server")
	}
}

func TestLoginSeveralMethods(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true", "EnableSignUpWithGitLab": "true"}, "myteam")
	app := newEmptyApp(t)
	r := run(app, "auth", "login", s.URL)
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "saml, gitlab") {
		t.Fatal(r.err)
	}
	var opened string
	app.OpenBrowser = func(u string) error { opened = u; return nil }
	mustCode(t, run(app, "auth", "login", s.URL, "--sso", "gitlab"), 0)
	if !strings.Contains(opened, "/oauth/gitlab/login?desktop_token=") {
		t.Fatal(opened)
	}
	mustCode(t, run(app, "auth", "login", s.URL, "--sso", "kerberos"), clierr.CodeUsage)
}

func TestLoginTokenStdin(t *testing.T) {
	s := loginServer(t, nil, "myteam")
	app := newEmptyApp(t)
	mustCode(t, runStdin(app, "pat\n", "auth", "login", s.URL, "--token-stdin", "--profile", "work"), 0)
	if tok, _ := app.Store.LoadSession("work"); tok != "pat" {
		t.Fatal(tok)
	}
	mustCode(t, runStdin(app, "", "auth", "login", s.URL, "--token-stdin"), clierr.CodeUsage)
	mustCode(t, runStdin(app, "a b", "auth", "login", s.URL, "--token-stdin"), clierr.CodeUsage)
	mustCode(t, runStdin(app, "wrong", "auth", "login", s.URL, "--token-stdin", "--profile", "other"), clierr.CodeAuth)
}

func TestLoginRejectsPlainHTTP(t *testing.T) {
	app := newEmptyApp(t)
	mustCode(t, run(app, "auth", "login", "http://chat.example.com"), clierr.CodeUsage)
	mustCode(t, run(app, "auth", "login", "ftp://chat.example.com"), clierr.CodeUsage)
}

func TestLoginUnknownTeam(t *testing.T) {
	s := loginServer(t, nil, "myteam")
	app := newEmptyApp(t)
	r := runStdin(app, "pat", "auth", "login", s.URL, "--token-stdin", "--team", "other")
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "myteam") {
		t.Fatal(r.err)
	}
}

func TestCheckAndLogout(t *testing.T) {
	app, s := newTestApp(t)
	s.Handle(http.MethodGet, "/api/v4/users/me", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Version-Id", "10.11.0")
		fakemm.WriteJSON(w, 200, &model.User{Id: meID, Username: "alice"})
	})
	r := run(app, "auth", "check")
	mustCode(t, r, 0)
	if !strings.Contains(r.out, "@alice") || !strings.Contains(r.out, "10.11.0") {
		t.Fatal(r.out)
	}
	r = run(app, "auth", "check", "--json")
	mustCode(t, r, 0)
	var got loginResult
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got.User.Username != "alice" || got.ServerVersion != "10.11.0" {
		t.Fatalf("%q %v", r.out, err)
	}
	s.Handle(http.MethodPost, "/api/v4/users/logout", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.AppError(w, http.StatusUnauthorized, "expired")
	})
	mustCode(t, run(app, "auth", "logout"), 0)
	if _, err := app.Store.LoadSession("test"); err == nil {
		t.Fatal("session must be deleted")
	}
	mustCode(t, run(app, "auth", "check"), clierr.CodeAuth)
}

// seedProfile stores profile "work" with a cache, as left by an
// earlier login.
func seedProfile(t *testing.T, app *App, url, userID string) {
	t.Helper()
	cfg := &config.Config{Default: "work", Profiles: map[string]*config.Profile{
		"work": {URL: url, UserID: userID, Username: "someone"},
	}}
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	c := resolve.NewCache()
	c.PutUser(userID, "someone")
	if err := c.Save(app.Store.CachePath("work")); err != nil {
		t.Fatal(err)
	}
	if !cacheExists(app, "work") {
		t.Fatal("the cache was not written")
	}
}

func cacheExists(app *App, name string) bool {
	_, err := os.Stat(app.Store.CachePath(name))
	return err == nil
}

func TestLoginAsAnotherUserDropsCache(t *testing.T) {
	s := loginServer(t, nil, "myteam")
	app := newEmptyApp(t)
	seedProfile(t, app, s.URL, bobID)
	mustCode(t, runStdin(app, "pat\n", "auth", "login", s.URL, "--token-stdin", "--profile", "work"), 0)
	if cacheExists(app, "work") {
		t.Fatal("the cache of the previous user must be removed")
	}
}

func TestLoginToAnotherServerDropsCache(t *testing.T) {
	s := loginServer(t, nil, "myteam")
	app := newEmptyApp(t)
	seedProfile(t, app, "https://chat.example.com", meID)
	mustCode(t, runStdin(app, "pat\n", "auth", "login", s.URL, "--token-stdin", "--profile", "work"), 0)
	if cacheExists(app, "work") {
		t.Fatal("the cache of the previous server must be removed")
	}
}

func TestLoginAgainKeepsCache(t *testing.T) {
	s := loginServer(t, nil, "myteam")
	app := newEmptyApp(t)
	seedProfile(t, app, s.URL, meID)
	mustCode(t, runStdin(app, "pat\n", "auth", "login", s.URL, "--token-stdin", "--profile", "work"), 0)
	if !cacheExists(app, "work") {
		t.Fatal("logging in again as the same user must keep the cache")
	}
}

// waitDone fails the test when done is not closed within d.
func waitDone(t *testing.T, done <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v after the context was canceled", what, d)
	}
}

func TestReadLineCtxCanceled(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := readLineCtx(ctx, pr)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	line, err := readLineCtx(context.Background(), strings.NewReader("abc"))
	if err != nil || line != "abc" {
		t.Fatalf("%q %v", line, err)
	}
}

func TestLoginCanceledWhileWaitingForAddress(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
	app := newEmptyApp(t)
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return true }
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	app.In = pr
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.OpenBrowser = func(string) error { cancel(); return nil }
	done := make(chan struct{})
	var code int
	go func() { code = Execute(ctx, app, []string{"auth", "login", s.URL}); close(done) }()
	waitDone(t, done, 3*time.Second, "auth login")
	if code == 0 {
		t.Fatal("a canceled login must not succeed")
	}
	if _, err := app.Store.LoadSession(profileNameFromURL(s.URL)); err == nil {
		t.Fatal("no session expected")
	}
}

func TestConfirmCanceled(t *testing.T) {
	app, _ := newTestApp(t)
	app.Out, app.Err = &bytes.Buffer{}, &bytes.Buffer{}
	app.IsTerminal = func() bool { return true }
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	app.In = pr
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	done := make(chan struct{})
	var code int
	go func() { code = Execute(ctx, app, []string{"profile", "remove", "test"}); close(done) }()
	waitDone(t, done, 3*time.Second, "profile remove")
	if code == 0 {
		t.Fatal("a canceled confirmation must not succeed")
	}
	if cfg, _ := app.Store.Load(); cfg.Profiles["test"] == nil {
		t.Fatal("the profile must stay")
	}
}

func TestLoginPendingJSON(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
	app := newEmptyApp(t)
	r := run(app, "auth", "login", s.URL, "--no-browser", "--profile", "work", "--json")
	mustCode(t, r, 0)
	var got loginPending
	if err := json.Unmarshal([]byte(r.out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %q %v", r.out, err)
	}
	if got.Status != "pending" || got.Profile != "work" || got.Method != "saml" ||
		!strings.Contains(got.LoginURL, "desktop_token=") || got.ExpiresIn != 180 || !strings.Contains(got.Next, "--callback -") {
		t.Fatalf("%+v", got)
	}
	r = runStdin(app, callbackFor(t, s, got.LoginURL)+"\n", "auth", "login", "--profile", "work", "--callback", "-", "--json")
	mustCode(t, r, 0)
	var done loginResult
	if err := json.Unmarshal([]byte(r.out), &done); err != nil || done.Status != "logged_in" || done.User.Username != "alice" {
		t.Fatalf("%q %v", r.out, err)
	}
}

func TestLoginCallbackFromStdin(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
	app := newEmptyApp(t)
	var opened string
	app.OpenBrowser = func(u string) error { opened = u; return nil }
	r := run(app, "auth", "login", s.URL, "--profile", "work")
	mustCode(t, r, 0)
	if !strings.Contains(r.err, "--callback -") {
		t.Fatalf("the hint must recommend --callback -: %s", r.err)
	}
	mustCode(t, runStdin(app, callbackFor(t, s, opened)+"\n", "auth", "login", "--profile", "work", "--callback", "-"), 0)
	if tok, err := app.Store.LoadSession("work"); err != nil || tok != "sess" {
		t.Fatalf("session %q %v", tok, err)
	}
	mustCode(t, runStdin(app, "", "auth", "login", "--profile", "work", "--callback", "-"), clierr.CodeUsage)
}

func TestUnreadableSessionFileIsGeneralError(t *testing.T) {
	app, _ := newTestApp(t)
	p := filepath.Join(app.Store.ConfigDir, "sessions", "test.token")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	mustCode(t, run(app, "auth", "check"), clierr.CodeError)
	mustCode(t, run(app, "auth", "check", "--profile", "missing"), clierr.CodeUsage)
}

func TestCorruptPendingIsGeneralError(t *testing.T) {
	app := newEmptyApp(t)
	p := filepath.Join(app.Store.ConfigDir, "sessions", "work.pending")
	if err := config.WriteFileAtomic(p, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustCode(t, run(app, "auth", "login", "--profile", "work", "--callback", "https://chat.example.com/login/desktop"), clierr.CodeError)
}

// startPending starts a two-step login to profile work and returns the
// /login/desktop address that finishes it.
func startPending(t *testing.T, app *App, s *fakemm.Server) string {
	t.Helper()
	var opened string
	app.OpenBrowser = func(u string) error { opened = u; return nil }
	mustCode(t, run(app, "auth", "login", s.URL, "--profile", "work"), 0)
	return callbackFor(t, s, opened)
}

func TestLoginExchangeErrorsKeepTheirCode(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header string
		code   int
	}{
		{"unavailable", http.StatusServiceUnavailable, "", clierr.CodeError},
		{"rate limited", http.StatusTooManyRequests, "0", clierr.CodeRateLimited},
		{"rejected", http.StatusUnauthorized, "", clierr.CodeAuth},
		{"bad request", http.StatusBadRequest, "", clierr.CodeAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
			app := newEmptyApp(t)
			cb := startPending(t, app, s)
			s.Handle(http.MethodPost, "/api/v4/users/login/desktop_token", func(w http.ResponseWriter, _ *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				fakemm.AppError(w, tc.status, "failed")
			})
			mustCode(t, run(app, "auth", "login", "--profile", "work", "--callback", cb), tc.code)
		})
	}
}

func TestLoginKeepsConcurrentConfigChanges(t *testing.T) {
	s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
	app := newEmptyApp(t)
	app.IsTerminal = func() bool { return true }
	pr, pw := io.Pipe()
	app.In = pr
	app.OpenBrowser = func(u string) error {
		// Another process adds a profile and starts its own login to
		// the same profile while this one waits for the address.
		cfg, err := app.Store.Load()
		if err != nil {
			return err
		}
		cfg.Profiles["other"] = &config.Profile{URL: "https://chat.example.com"}
		if err := app.Store.Save(cfg); err != nil {
			return err
		}
		if err := app.Store.SavePending("work", config.Pending{URL: s.URL, Method: "saml", ClientToken: "theirs", CreatedAt: now.Unix()}); err != nil {
			return err
		}
		go func() { _, _ = fmt.Fprintln(pw, callbackFor(t, s, u)) }()
		return nil
	}
	mustCode(t, run(app, "auth", "login", s.URL, "--profile", "work"), 0)
	cfg, err := app.Store.Load()
	if err != nil || cfg.Profiles["other"] == nil || cfg.Profiles["work"] == nil {
		t.Fatalf("%+v %v", cfg, err)
	}
	pend, err := app.Store.LoadPending("work")
	if err != nil || pend.ClientToken != "theirs" {
		t.Fatalf("the login of the other process must stay: %+v %v", pend, err)
	}
}

func TestLogoutJSON(t *testing.T) {
	app, s := newTestApp(t)
	s.Handle(http.MethodPost, "/api/v4/users/logout", func(w http.ResponseWriter, _ *http.Request) {
		fakemm.WriteJSON(w, 200, map[string]string{"status": "OK"})
	})
	r := run(app, "auth", "logout", "--json")
	mustCode(t, r, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(r.out), &got); err != nil || got["action"] != "logged_out" || got["profile"] != "test" {
		t.Fatalf("%q %v", r.out, err)
	}
}

func TestLoginUsageErrors(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"token and callback", "", []string{"https://chat.example.com", "--token-stdin", "--callback", "-"}},
		{"unknown sso", "", []string{"https://chat.example.com", "--sso", "ldap"}},
		{"no url and no profiles", "", nil},
		{"unknown profile without url", "", []string{"--profile", "work"}},
		{"two tokens on stdin", "pat other\n", []string{"https://chat.example.com", "--token-stdin"}},
		{"empty stdin token", "\n", []string{"https://chat.example.com", "--token-stdin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newEmptyApp(t)
			r := runStdin(app, tc.stdin, append([]string{"auth", "login"}, tc.args...)...)
			mustCode(t, r, clierr.CodeUsage)
		})
	}
}

// banProfile writes a rate-limit state file for the profile that forbids
// requests for an hour after now.
func banProfile(t *testing.T, app *App, name string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(app.Store.CachePath(name)), "ratelimit.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`{"not_before":%q}`, now.Add(time.Hour).Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoginHonoursSavedRateLimitBan(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
		app := newEmptyApp(t)
		app.OpenBrowser = func(string) error { return nil }
		banProfile(t, app, "work")
		r := run(app, "auth", "login", s.URL, "--profile", "work")
		mustCode(t, r, clierr.CodeRateLimited)
		if n := s.Total(); n != 0 {
			t.Fatalf("%d requests sent during the ban", n)
		}
		if !strings.Contains(r.err, "rate limited by the server until") || strings.Contains(r.err, `Get "`) {
			t.Fatalf("stderr: %s", r.err)
		}
	})
	t.Run("callback", func(t *testing.T) {
		s := loginServer(t, map[string]string{"EnableSaml": "true"}, "myteam")
		app := newEmptyApp(t)
		cb := startPending(t, app, s)
		before := s.Total()
		banProfile(t, app, "work")
		r := run(app, "auth", "login", "--profile", "work", "--callback", cb)
		mustCode(t, r, clierr.CodeRateLimited)
		if n := s.Total() - before; n != 0 {
			t.Fatalf("%d requests sent during the ban", n)
		}
		if strings.Contains(r.err, `Post "`) {
			t.Fatalf("stderr: %s", r.err)
		}
	})
}
