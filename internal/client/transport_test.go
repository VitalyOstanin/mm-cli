package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

type recorder struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (r *recorder) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sleeps = append(r.sleeps, d)
	return nil
}

// script serves the given statuses in order; the last one repeats.
func script(t *testing.T, statuses []int, headers map[int]http.Header) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		i := n
		n++
		bodies = append(bodies, string(b))
		mu.Unlock()
		if i >= len(statuses) {
			i = len(statuses) - 1
		}
		for k, v := range headers[i] {
			w.Header()[k] = v
		}
		w.WriteHeader(statuses[i])
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newTestTransport has a fixed clock, no jitter and records its sleeps.
func newTestTransport(rec *recorder) *Transport {
	tr := NewTransport(nil, 1000, 100)
	tr.Sleep = rec.sleep
	tr.Now = func() time.Time { return testNow }
	tr.Rand = func() float64 { return 0 }
	return tr
}

func do(t *testing.T, tr *Transport, method, url, body string) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func TestRetryAfter429(t *testing.T) {
	srv, bodies := script(t, []int{429, 200}, map[int]http.Header{0: {"Retry-After": {"2"}}})
	rec := &recorder{}
	resp := do(t, newTestTransport(rec), http.MethodGet, srv.URL, "")
	if resp.StatusCode != 200 || len(*bodies) != 2 {
		t.Fatalf("status %d, hits %d", resp.StatusCode, len(*bodies))
	}
	if len(rec.sleeps) != 1 || rec.sleeps[0] != 2*time.Second {
		t.Fatalf("sleeps %v", rec.sleeps)
	}
}

func TestThrottleGivesUp(t *testing.T) {
	srv, bodies := script(t, []int{429}, nil)
	rec := &recorder{}
	resp := do(t, newTestTransport(rec), http.MethodGet, srv.URL, "")
	if resp.StatusCode != 429 || len(*bodies) != 1+maxThrottleRetries {
		t.Fatalf("status %d, hits %d", resp.StatusCode, len(*bodies))
	}
	if len(rec.sleeps) != maxThrottleRetries {
		t.Fatalf("sleeps %v", rec.sleeps)
	}
}

func TestPost429ReplaysBody(t *testing.T) {
	srv, bodies := script(t, []int{429, 201}, nil)
	resp := do(t, newTestTransport(&recorder{}), http.MethodPost, srv.URL, `{"message":"hi"}`)
	if resp.StatusCode != 201 || len(*bodies) != 2 || (*bodies)[0] != (*bodies)[1] || (*bodies)[1] != `{"message":"hi"}` {
		t.Fatalf("status %d, bodies %q", resp.StatusCode, *bodies)
	}
}

func TestPost503NotRetried(t *testing.T) {
	srv, bodies := script(t, []int{503, 201}, nil)
	resp := do(t, newTestTransport(&recorder{}), http.MethodPost, srv.URL, `x`)
	if resp.StatusCode != 503 || len(*bodies) != 1 {
		t.Fatalf("status %d, hits %d", resp.StatusCode, len(*bodies))
	}
}

func TestGet503Retried(t *testing.T) {
	srv, bodies := script(t, []int{503, 503, 200}, nil)
	rec := &recorder{}
	resp := do(t, newTestTransport(rec), http.MethodGet, srv.URL, "")
	if resp.StatusCode != 200 || len(*bodies) != 3 {
		t.Fatalf("status %d, hits %d", resp.StatusCode, len(*bodies))
	}
	if len(rec.sleeps) != 2 || rec.sleeps[0] != time.Second || rec.sleeps[1] != 2*time.Second {
		t.Fatalf("sleeps %v", rec.sleeps)
	}
}

func TestGetNetworkErrorRetried(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	rec := &recorder{}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if _, err := newTestTransport(rec).RoundTrip(req); err == nil {
		t.Fatal("expected an error")
	}
	if len(rec.sleeps) != maxFailureRetries {
		t.Fatalf("sleeps %v", rec.sleeps)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := map[string]time.Duration{
		"":                     defaultRetryAfter,
		"7":                    7 * time.Second,
		"999":                  999 * time.Second,
		"10000000000":          maxRetryAfter,
		"99999999999999999999": maxRetryAfter,
		"-5":                   0,
		now.Add(10 * time.Second).Format(http.TimeFormat): 10 * time.Second,
		now.Add(-time.Minute).Format(http.TimeFormat):     0,
		"soon": defaultRetryAfter,
	}
	for in, want := range tests {
		if got := retryAfter(in, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAdaptive(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := newAdaptive(1, 3)
	a.onThrottled(now, now)
	if l, b := a.limit(); l != 0.5 || b != 1 {
		t.Fatalf("after one 429: %v %d", l, b)
	}
	for range 10 {
		a.onThrottled(now, now)
	}
	if l, _ := a.limit(); l != minLimit {
		t.Fatalf("floor: %v", l)
	}
	for range recoverAfter - 1 {
		a.onSuccess(now)
	}
	if l, _ := a.limit(); l != minLimit {
		t.Fatalf("recovered too early: %v", l)
	}
	a.onSuccess(now)
	if l, _ := a.limit(); l != minLimit*growFactor {
		t.Fatalf("after %d successes: %v", recoverAfter, l)
	}
	for range 40 * recoverAfter {
		a.onSuccess(now)
	}
	if l, b := a.limit(); l != rate.Limit(1) || b != 3 {
		t.Fatalf("cap: %v %d", l, b)
	}
}

func TestRateLimitHeaders(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := newAdaptive(1, 3)
	a.observe(http.Header{"X-Ratelimit-Remaining": {"5"}, "X-Ratelimit-Reset": {"7"}}, now)
	if d, _ := a.pause(now); d != 0 {
		t.Fatalf("remaining 5 must not pause: %v", d)
	}
	a.observe(http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"7"}}, now)
	if d, _ := a.pause(now); d != 7*time.Second {
		t.Fatalf("pause %v", d)
	}
	b := newAdaptive(1, 3)
	b.observe(http.Header{"X-Ratelimit-Remaining": {"1"}, "X-Ratelimit-Reset": {"600"}}, now)
	if d, _ := b.pause(now); d != 600*time.Second {
		t.Fatalf("a long Reset must not be shortened: %v", d)
	}
}

func TestTransportHonoursReset(t *testing.T) {
	srv, _ := script(t, []int{200}, map[int]http.Header{0: {"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"7"}}})
	rec := &recorder{}
	tr := newTestTransport(rec)
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr.Now = func() time.Time { return fixed }
	do(t, tr, http.MethodGet, srv.URL, "")
	do(t, tr, http.MethodGet, srv.URL, "")
	if len(rec.sleeps) == 0 || rec.sleeps[0] != 7*time.Second {
		t.Fatalf("sleeps %v", rec.sleeps)
	}
}
