package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VitalyOstanin/mm-cli/internal/config"
	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

var (
	meID  = fakemm.ID("alice")
	bobID = fakemm.ID("bob")
	now   = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
)

// newEmptyApp returns an App with an empty configuration directory.
func newEmptyApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	return &App{
		In:          strings.NewReader(""),
		Out:         &bytes.Buffer{},
		Err:         &bytes.Buffer{},
		Store:       config.Store{ConfigDir: filepath.Join(dir, "config"), CacheDir: filepath.Join(dir, "cache")},
		Env:         func(string) string { return "" },
		Now:         func() time.Time { return now },
		Location:    time.UTC,
		OpenBrowser: func(string) error { t.Error("unexpected browser launch"); return nil },
		IsTerminal:  func() bool { return false },
		Version:     "test",
		DefaultRate: config.RateLimit{PerSecond: 1000, Burst: 100},
	}
}

// newTestApp returns an App logged in to a fake server as alice in myteam.
func newTestApp(t *testing.T) (*App, *fakemm.Server) {
	t.Helper()
	s := fakemm.New(t)
	app := newEmptyApp(t)
	cfg := &config.Config{Default: "test", Profiles: map[string]*config.Profile{
		"test": {URL: s.URL, Team: "myteam", UserID: meID, Username: "alice"},
	}}
	if err := app.Store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.Store.SaveSession("test", "tok"); err != nil {
		t.Fatal(err)
	}
	return app, s
}

type result struct {
	code     int
	out, err string
}

func run(app *App, args ...string) result {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app.Out, app.Err = out, errb
	code := Execute(context.Background(), app, args)
	return result{code, out.String(), errb.String()}
}

func runStdin(app *App, stdin string, args ...string) result {
	app.In = strings.NewReader(stdin)
	return run(app, args...)
}

func mustCode(t *testing.T, r result, code int) {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit code %d, want %d\nstdout: %s\nstderr: %s", r.code, code, r.out, r.err)
	}
}

var _ io.Reader = (*strings.Reader)(nil)
