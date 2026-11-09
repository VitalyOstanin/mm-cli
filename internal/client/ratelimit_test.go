package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

// run is one process of the tool: its own transport, clock and stderr,
// sharing the state file with the other runs.
type run struct {
	tr     *Transport
	rec    *recorder
	stderr bytes.Buffer
}

func newRun(path string, at time.Time, perSecond float64, burst int) *run {
	r := &run{rec: &recorder{}}
	r.tr = NewTransport(nil, perSecond, burst)
	r.tr.Sleep = r.rec.sleep
	r.tr.Now = func() time.Time { return at }
	r.tr.Rand = func() float64 { return 0 }
	r.tr.Notify = &r.stderr
	r.tr.UseState(path)
	return r
}

func get(tr *Transport, url string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := tr.RoundTrip(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	return resp, err
}

func TestBanSharedBetweenRuns(t *testing.T) {
	srv, hits := script(t, []int{429, 200}, map[int]http.Header{0: {"Retry-After": {"30"}}})
	path := filepath.Join(t.TempDir(), "myserver", StateFile)

	r1 := newRun(path, testNow, 1000, 100)
	if resp, err := get(r1.tr, srv.URL); err != nil || resp.StatusCode != 200 {
		t.Fatalf("run 1: %v %v", resp, err)
	}
	if len(*hits) != 2 || len(r1.rec.sleeps) != 1 || r1.rec.sleeps[0] != 30*time.Second {
		t.Fatalf("run 1: hits %d, sleeps %v", len(*hits), r1.rec.sleeps)
	}

	// Run 2 starts 10 s later: it must wait the remaining 20 s before its
	// first request.
	r2 := newRun(path, testNow.Add(10*time.Second), 1000, 100)
	hitsAtSleep := -1
	r2.tr.Sleep = func(ctx context.Context, d time.Duration) error {
		hitsAtSleep = len(*hits)
		return r2.rec.sleep(ctx, d)
	}
	if _, err := get(r2.tr, srv.URL); err != nil {
		t.Fatal(err)
	}
	if len(r2.rec.sleeps) != 1 || r2.rec.sleeps[0] != 20*time.Second || hitsAtSleep != 2 || len(*hits) != 3 {
		t.Fatalf("run 2: sleeps %v, hits at sleep %d, hits %d", r2.rec.sleeps, hitsAtSleep, len(*hits))
	}
	if !strings.Contains(r2.stderr.String(), "mm-cli: rate limited by the server, waiting 20s") {
		t.Fatalf("run 2 stderr %q", r2.stderr.String())
	}

	// Run 3 starts after the ban: no wait.
	r3 := newRun(path, testNow.Add(time.Minute), 1000, 100)
	if _, err := get(r3.tr, srv.URL); err != nil || len(r3.rec.sleeps) != 0 {
		t.Fatalf("run 3: %v, sleeps %v", err, r3.rec.sleeps)
	}
}

func TestLongBanEndsRunsWithoutRequests(t *testing.T) {
	srv, hits := script(t, []int{429, 200}, map[int]http.Header{0: {"Retry-After": {"600"}}})
	path := filepath.Join(t.TempDir(), StateFile)

	r1 := newRun(path, testNow, 1000, 100)
	_, err := get(r1.tr, srv.URL)
	if clierr.ExitCode(err) != clierr.CodeRateLimited || len(*hits) != 1 || len(r1.rec.sleeps) != 0 {
		t.Fatalf("run 1: err %v, hits %d, sleeps %v", err, len(*hits), r1.rec.sleeps)
	}
	if s, ok := loadState(path); !ok || !s.NotBefore.Equal(testNow.Add(600*time.Second)) {
		t.Fatalf("state %+v %v", s, ok)
	}

	r2 := newRun(path, testNow.Add(10*time.Second), 1000, 100)
	_, err = get(r2.tr, srv.URL)
	if clierr.ExitCode(err) != clierr.CodeRateLimited || len(*hits) != 1 || len(r2.rec.sleeps) != 0 {
		t.Fatalf("run 2: err %v, hits %d, sleeps %v", err, len(*hits), r2.rec.sleeps)
	}
	if !strings.Contains(err.Error(), "until 2026-01-01T00:10:00Z") {
		t.Fatalf("run 2 message %q", err)
	}
}

func TestRateRecoversBetweenRuns(t *testing.T) {
	srv, _ := script(t, []int{429, 200}, map[int]http.Header{0: {"Retry-After": {"1"}}})
	path := filepath.Join(t.TempDir(), StateFile)
	if _, err := get(newRun(path, testNow, 1, 3).tr, srv.URL); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		after time.Duration
		limit rate.Limit
		burst int
	}{
		{time.Second, 0.5, 1},
		{recoverInterval, 0.75, 1},
		{10 * time.Minute, 1, 3},
	} {
		l, b := newRun(path, testNow.Add(c.after), 1, 3).tr.lim.limit()
		if l != c.limit || b != c.burst {
			t.Errorf("after %v: %v %d, want %v %d", c.after, l, b, c.limit, c.burst)
		}
	}
}

func TestFullRateRemovesState(t *testing.T) {
	srv, _ := script(t, []int{200}, nil)
	path := filepath.Join(t.TempDir(), StateFile)
	if err := saveState(path, rateState{Limit: 900, LimitAt: testNow}, testNow, 1000); err != nil {
		t.Fatal(err)
	}
	r := newRun(path, testNow, 1000, 100)
	for range recoverAfter - 1 {
		if _, err := get(r.tr, srv.URL); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("removed too early: %v", err)
	}
	if _, err := get(r.tr, srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state not removed: %v", err)
	}
}

func TestClearStateKeepsNewerCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFile)
	_ = saveState(path, rateState{Limit: 0.5, LimitAt: testNow.Add(time.Minute)}, testNow, 1)
	if err := clearState(path, testNow, testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadState(path); !ok {
		t.Fatal("a newer cut by another run was removed")
	}
}

func TestStateMergeKeepsLaterBanAndSlowerRate(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFile)
	_ = saveState(path, rateState{NotBefore: testNow.Add(100 * time.Second), Limit: 0.5, LimitAt: testNow}, testNow, 1)
	_ = saveState(path, rateState{NotBefore: testNow.Add(10 * time.Second), Limit: 0.8, LimitAt: testNow}, testNow, 1)
	s, _ := loadState(path)
	if !s.NotBefore.Equal(testNow.Add(100*time.Second)) || s.Limit != 0.5 {
		t.Fatalf("after a faster, earlier write: %+v", s)
	}
	_ = saveState(path, rateState{NotBefore: testNow.Add(5 * time.Second), Limit: 0.2, LimitAt: testNow}, testNow, 1)
	s, _ = loadState(path)
	if !s.NotBefore.Equal(testNow.Add(100*time.Second)) || s.Limit != 0.2 {
		t.Fatalf("after a slower write: %+v", s)
	}
}

func TestDamagedStateIgnored(t *testing.T) {
	srv, _ := script(t, []int{200}, nil)
	for name, data := range map[string]string{
		"garbage":    "not json at all",
		"array":      `[1, 2, 3]`,
		"bad time":   `{"not_before": "soon"}`,
		"bad limit":  `{"limit": -5, "limit_at": "2026-01-01T00:00:00Z"}`,
		"no time":    `{"limit": 0.1}`,
		"binary":     "\x00\xff\xfe" + strings.Repeat("x", 10000),
		"truncated":  `{"not_before": "2026-01-01T00:10:00Z"`,
		"far future": `{"not_before": "9999-01-01T00:00:00Z"}`,
	} {
		path := filepath.Join(t.TempDir(), StateFile)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		r := newRun(path, testNow, 1000, 100)
		_, err := get(r.tr, srv.URL)
		if name == "far future" {
			// Honoured, but capped: no lock for centuries.
			if clierr.ExitCode(err) != clierr.CodeRateLimited || !strings.Contains(err.Error(), "2026-01-01T01:00:00Z") {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err != nil || len(r.rec.sleeps) != 0 {
			t.Errorf("%s: err %v, sleeps %v", name, err, r.rec.sleeps)
		}
		if l, b := r.tr.lim.limit(); l != 1000 || b != 100 {
			t.Errorf("%s: limit %v %d", name, l, b)
		}
	}
}

func TestLongRetryAfterNotRetried(t *testing.T) {
	srv, bodies := script(t, []int{429, 200}, map[int]http.Header{0: {"Retry-After": {"300"}}})
	rec := &recorder{}
	_, err := get(newTestTransport(rec), srv.URL)
	if clierr.ExitCode(err) != clierr.CodeRateLimited || len(*bodies) != 1 || len(rec.sleeps) != 0 {
		t.Fatalf("err %v, hits %d, sleeps %v", err, len(*bodies), rec.sleeps)
	}
}

type errBase struct {
	err   error
	calls int
}

func (b *errBase) RoundTrip(*http.Request) (*http.Response, error) {
	b.calls++
	return nil, b.err
}

func TestPermanentNetworkErrorsNotRetried(t *testing.T) {
	for name, e := range map[string]error{
		"tls": &tls.CertificateVerificationError{Err: errors.New("unknown authority")},
		"dns": &net.DNSError{Err: "no such host", Name: "chat.example.com", IsNotFound: true},
	} {
		rec := &recorder{}
		b := &errBase{err: e}
		tr := newTestTransport(rec)
		tr.Base = b
		if _, err := get(tr, "https://chat.example.com/api/v4/users/me"); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if b.calls != 1 || len(rec.sleeps) != 0 {
			t.Errorf("%s: calls %d, sleeps %v", name, b.calls, rec.sleeps)
		}
	}
	// A temporary DNS failure is still retried.
	rec := &recorder{}
	b := &errBase{err: &net.DNSError{Err: "timeout", Name: "chat.example.com", IsTimeout: true}}
	tr := newTestTransport(rec)
	tr.Base = b
	_, _ = get(tr, "https://chat.example.com/api/v4/users/me")
	if b.calls != 1+maxFailureRetries {
		t.Fatalf("temporary: calls %d", b.calls)
	}
}

func TestBackoffExponentialWithJitter(t *testing.T) {
	srv, _ := script(t, []int{503, 503, 200}, nil)
	rec := &recorder{}
	tr := newTestTransport(rec)
	tr.Rand = func() float64 { return 0.5 }
	if _, err := get(tr, srv.URL); err != nil {
		t.Fatal(err)
	}
	if len(rec.sleeps) != 2 || rec.sleeps[0] != 1250*time.Millisecond || rec.sleeps[1] != 2500*time.Millisecond {
		t.Fatalf("sleeps %v", rec.sleeps)
	}
	tr.Rand = func() float64 { return 0 }
	for n, want := range map[int]time.Duration{1: time.Second, 3: 4 * time.Second, 6: maxBackoff, 100: maxBackoff} {
		if got := tr.backoff(n); got != want {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
}

type hangBase struct{ calls int }

func (b *hangBase) RoundTrip(r *http.Request) (*http.Response, error) {
	b.calls++
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestAttemptTimeout(t *testing.T) {
	rec := &recorder{}
	b := &hangBase{}
	tr := newTestTransport(rec)
	tr.Base = b
	tr.AttemptTimeout = 5 * time.Millisecond
	_, err := get(tr, "https://chat.example.com/api/v4/users/me")
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() || b.calls != 1+maxFailureRetries {
		t.Fatalf("err %v, calls %d", err, b.calls)
	}

	// The timeout covers the headers only: the body stays readable.
	srv, _ := script(t, []int{200}, nil)
	tr = newTestTransport(rec)
	tr.AttemptTimeout = time.Minute
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "{}" {
		t.Fatalf("body %q, %v", body, err)
	}
}

func TestNewHasNoOverallTimeout(t *testing.T) {
	c := New(Options{URL: "https://chat.example.com"})
	tr, ok := c.HTTPClient.Transport.(*Transport)
	if c.HTTPClient.Timeout != 0 || !ok || tr.AttemptTimeout != DefaultAttemptTimeout {
		t.Fatalf("timeout %v, transport %T", c.HTTPClient.Timeout, c.HTTPClient.Transport)
	}
}
