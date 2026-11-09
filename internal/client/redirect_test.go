package client

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

const redirectToken = "secret-token"

// tokenSink records the Authorization header of every request it serves.
type tokenSink struct {
	mu    sync.Mutex
	hits  int
	auths []string
}

func (s *tokenSink) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.hits++
	s.auths = append(s.auths, r.Header.Get("Authorization"))
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"OK"}`))
}

func (s *tokenSink) snapshot() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits, append([]string(nil), s.auths...)
}

func redirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, http.StatusFound)
	}
}

// pingThrough sends an authenticated request to first with the client that
// New builds; base, when set, replaces the network transport (for TLS).
func pingThrough(t *testing.T, first string, base http.RoundTripper) error {
	t.Helper()
	c := New(Options{URL: first, Token: redirectToken, PerSecond: 1000, Burst: 100})
	if base != nil {
		c.HTTPClient.Transport.(*Transport).Base = base
	}
	_, _, err := c.GetPing(context.Background())
	return err
}

func TestRedirectFromHTTPSToHTTPIsRefused(t *testing.T) {
	sink := &tokenSink{}
	second := httptest.NewServer(http.HandlerFunc(sink.handler))
	defer second.Close()
	first := httptest.NewTLSServer(redirectTo(second.URL))
	defer first.Close()

	err := pingThrough(t, first.URL, first.Client().Transport)
	if err == nil {
		t.Fatal("redirect from https to http was followed")
	}
	if hits, _ := sink.snapshot(); hits != 0 {
		t.Fatalf("plain-http server got %d requests, want 0", hits)
	}
}

func TestRedirectToOtherPortDropsToken(t *testing.T) {
	sink := &tokenSink{}
	second := httptest.NewServer(http.HandlerFunc(sink.handler))
	defer second.Close()
	// Same host name, different port: the standard library keeps the
	// header because it compares host names only.
	first := httptest.NewServer(redirectTo(second.URL))
	defer first.Close()

	_ = pingThrough(t, first.URL, nil)
	hits, auths := sink.snapshot()
	if hits == 0 {
		t.Fatal("redirect was not followed")
	}
	for _, a := range auths {
		if a != "" {
			t.Fatalf("other origin got Authorization %q", a)
		}
	}
}

func TestRedirectToOtherHostDropsToken(t *testing.T) {
	sink := &tokenSink{}
	second := httptest.NewServer(http.HandlerFunc(sink.handler))
	defer second.Close()
	// 127.0.0.1 and localhost are different host names for the client.
	_, port, err := net.SplitHostPort(second.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	other := "http://localhost:" + port
	first := httptest.NewServer(redirectTo(other))
	defer first.Close()

	_ = pingThrough(t, first.URL, nil)
	hits, auths := sink.snapshot()
	if hits == 0 {
		t.Fatal("redirect was not followed")
	}
	for _, a := range auths {
		if a != "" {
			t.Fatalf("other host got Authorization %q", a)
		}
	}
}

func TestRedirectOnSameOriginKeepsToken(t *testing.T) {
	sink := &tokenSink{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/system/ping", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved/ping", http.StatusFound)
	})
	mux.HandleFunc("/moved/ping", sink.handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := pingThrough(t, srv.URL, nil); err != nil {
		t.Fatal(err)
	}
	_, auths := sink.snapshot()
	if len(auths) != 1 || auths[0] != model.HeaderBearer+" "+redirectToken {
		t.Fatalf("Authorization after same-origin redirect = %q", auths)
	}
}

func TestRedirectLimitIsKept(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()

	if err := pingThrough(t, srv.URL, nil); err == nil {
		t.Fatal("endless redirect loop did not stop")
	}
}
