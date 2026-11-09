package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// TestUserAgent: requests carry mm-cli/<version>, or the server's
// user_agent from config.yaml; a login to a saved server keeps it.
func TestUserAgent(t *testing.T) {
	app, s := newTestApp(t)
	var got []string
	s.Handle(http.MethodGet, "/api/v4/users/me", func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.UserAgent())
		fakemm.WriteJSON(w, 200, &model.User{Id: meID, Username: "alice"})
	})
	s.JSON(http.MethodGet, "/api/v4/users/"+meID+"/teams", 200, []*model.Team{{Id: teamID, Name: "myteam"}})
	mustCode(t, run(app, "profile", "get"), 0)

	cfg, _ := app.Store.Load()
	cfg.Servers["test"].UserAgent = "Mozilla/5.0 (X11; Linux x86_64) example"
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	mustCode(t, run(app, "profile", "get"), 0)
	mustCode(t, runStdin(app, "pat\n", "auth", "login", s.URL, "--token-stdin", "--name", "test"), 0)

	want := []string{"mm-cli/test", "Mozilla/5.0 (X11; Linux x86_64) example", "Mozilla/5.0 (X11; Linux x86_64) example"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("User-Agent %q, want %q", got, want)
	}
	if cfg, _ = app.Store.Load(); cfg.Servers["test"].UserAgent != want[1] {
		t.Fatalf("login dropped user_agent: %+v", cfg.Servers["test"])
	}
}

// TestUserAgentInvalid: a user_agent that would split the request is a
// config error.
func TestUserAgentInvalid(t *testing.T) {
	app, s := newTestApp(t)
	writeRawConfig(t, app, "current: test\nservers:\n  test: {url: \""+s.URL+"\", user_agent: \"a\\r\\nX-Evil: 1\"}\n")
	r := run(app, "profile", "get")
	mustCode(t, r, clierr.CodeUsage)
	if !strings.Contains(r.err, "user_agent") {
		t.Fatalf("stderr: %s", r.err)
	}
}
