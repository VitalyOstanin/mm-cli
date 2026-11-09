// Package client builds a Mattermost API client whose transport limits the
// request rate and honours 429 responses, so the tool never floods a server.
package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

const (
	DefaultPerSecond = 1.0
	DefaultBurst     = 3

	// MaxWait is the longest the tool waits in-process for the server's
	// permission to send. A longer ban is saved for later runs and the
	// command fails with exit code 6 instead of sending early.
	MaxWait = 2 * time.Minute
	// DefaultAttemptTimeout bounds the wait for the response headers of
	// one attempt; retries and waits for 429 do not count against it.
	DefaultAttemptTimeout = 2 * time.Minute

	maxThrottleRetries = 3
	maxFailureRetries  = 2
	defaultRetryAfter  = 5 * time.Second
	// maxRetryAfter caps a ban announced by the server, so that a broken
	// header cannot lock a server for years.
	maxRetryAfter  = time.Hour
	failureBackoff = time.Second
	maxBackoff     = 30 * time.Second

	// drainLimit is how much of a response body is read before it is closed,
	// so that the connection can be reused.
	drainLimit = 64 << 10
	// maxRedirects is the standard limit of redirects.
	maxRedirects = 10
)

// Transport is an http.RoundTripper with a rate limiter and bounded retries.
type Transport struct {
	Base  http.RoundTripper
	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time
	// Rand returns a number in [0, 1) for the jitter of retry pauses.
	Rand func() float64
	// Notify, when set, receives a line before each wait for the server.
	Notify io.Writer
	// AttemptTimeout bounds the wait for response headers of one attempt;
	// zero means no bound.
	AttemptTimeout time.Duration
	// StatePath is the rate-limit state shared by runs for one server;
	// empty keeps the state in this process only.
	StatePath string
	lim       *adaptive
}

// NewTransport returns a transport limited to perSecond requests with the
// given burst.
func NewTransport(base http.RoundTripper, perSecond float64, burst int) *Transport {
	if perSecond <= 0 {
		perSecond = DefaultPerSecond
	}
	if burst <= 0 {
		burst = DefaultBurst
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{
		Base:           base,
		Sleep:          SleepCtx,
		Now:            time.Now,
		Rand:           rand.Float64,
		AttemptTimeout: DefaultAttemptTimeout,
		lim:            newAdaptive(perSecond, burst),
	}
}

// UseState shares the limiter state with other runs through the file at
// path and starts from what it holds. The file is read once here.
func (t *Transport) UseState(path string) {
	t.StatePath = path
	if s, ok := loadState(path); ok {
		t.lim.restore(s, t.Now())
	}
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	throttles, failures := 0, 0
	for {
		if err := t.Acquire(ctx); err != nil {
			return nil, err
		}
		attempt := req
		if throttles+failures > 0 {
			var err error
			if attempt, err = rewind(req); err != nil {
				return nil, err
			}
		}
		resp, err := t.send(ctx, attempt)
		if err != nil {
			if ctx.Err() != nil || !idempotent(req) || permanent(err) || failures >= maxFailureRetries {
				return nil, err
			}
			failures++
			if err := t.Sleep(ctx, t.backoff(failures)); err != nil {
				return nil, err
			}
			continue
		}
		t.lim.observe(resp.Header, t.Now())
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			now := t.Now()
			st := t.lim.onThrottled(now, now.Add(retryAfter(resp.Header.Get("Retry-After"), now)))
			t.save(st, now)
			if throttles >= maxThrottleRetries || !replayable(req) {
				return resp, nil
			}
			throttles++
			drain(resp)
			// The wait happens at the top of the loop, where a ban longer
			// than MaxWait ends the command instead.
		case (resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable ||
			resp.StatusCode == http.StatusGatewayTimeout) &&
			idempotent(req) && failures < maxFailureRetries:
			failures++
			drain(resp)
			if err := t.Sleep(ctx, t.backoff(failures)); err != nil {
				return nil, err
			}
		default:
			if resp.StatusCode < http.StatusInternalServerError {
				now := t.Now()
				if t.lim.onSuccess(now) && t.StatePath != "" {
					_ = clearState(t.StatePath, t.lim.cutTime(), now)
				}
			}
			return resp, nil
		}
	}
}

// Acquire waits for a ban announced by the server and then for the limiter,
// as before each request of RoundTrip. It is for requests made outside the
// transport, such as a WebSocket handshake. A ban longer than MaxWait is not
// waited for: it ends with exit code 6.
func (t *Transport) Acquire(ctx context.Context) error {
	now := t.Now()
	if d, until := t.lim.pause(now); d > 0 {
		if d > MaxWait {
			t.save(t.lim.snapshot(), now)
			return clierr.New(clierr.CodeRateLimited,
				"rate limited by the server until %s (%s from now); try again then",
				until.Format(time.RFC3339), d.Round(time.Second))
		}
		if t.Notify != nil {
			_, _ = fmt.Fprintf(t.Notify, "mm-cli: rate limited by the server, waiting %ds\n", int((d+time.Second-1)/time.Second))
		}
		if err := t.Sleep(ctx, d); err != nil {
			return err
		}
	}
	return t.lim.bucket.Wait(ctx)
}

// Throttled records a 429 answer to a request made outside the transport:
// the rate is cut and requests are banned for Retry-After, here and in
// later runs. It returns when the ban ends.
func (t *Transport) Throttled(h http.Header) time.Time {
	now := t.Now()
	st := t.lim.onThrottled(now, now.Add(retryAfter(h.Get("Retry-After"), now)))
	t.save(st, now)
	return st.NotBefore
}

// BannedUntil is when the ban announced by the server ends; zero when there
// is none.
func (t *Transport) BannedUntil() time.Time {
	_, until := t.lim.pause(t.Now())
	return until
}

// save shares the state with later runs; a failed write only loses the hint.
func (t *Transport) save(s rateState, now time.Time) {
	if t.StatePath == "" {
		return
	}
	_ = saveState(t.StatePath, s, now, t.lim.max)
}

// backoff is the pause before retry n (from 1): 1 s, 2 s, 4 s ... up to
// maxBackoff, plus up to half of it at random. It is never shorter than the
// base, so retries are not made faster by the jitter.
func (t *Transport) backoff(n int) time.Duration {
	d := maxBackoff
	if n < 6 {
		d = min(failureBackoff<<(n-1), maxBackoff)
	}
	j := 0.0
	if t.Rand != nil {
		j = t.Rand()
	}
	if j < 0 || j >= 1 {
		j = 0
	}
	return d + time.Duration(j*float64(d)/2)
}

// send runs one attempt. AttemptTimeout bounds the wait for the response
// headers only, so a long download is not cut off; the body is released when
// it is closed.
func (t *Transport) send(ctx context.Context, req *http.Request) (*http.Response, error) {
	if t.AttemptTimeout <= 0 {
		return t.Base.RoundTrip(req)
	}
	actx, cancel := context.WithCancelCause(ctx)
	timer := time.AfterFunc(t.AttemptTimeout, func() { cancel(errAttemptTimeout) })
	resp, err := t.Base.RoundTrip(req.WithContext(actx))
	timer.Stop()
	if err != nil {
		timedOut := ctx.Err() == nil && errors.Is(context.Cause(actx), errAttemptTimeout)
		cancel(nil)
		if timedOut {
			return nil, errAttemptTimeout
		}
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: func() { cancel(nil) }}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel func()
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// attemptTimeout is a net.Error, so it is reported as a network error.
type attemptTimeout struct{}

func (attemptTimeout) Error() string   { return "no response from the server in time" }
func (attemptTimeout) Timeout() bool   { return true }
func (attemptTimeout) Temporary() bool { return true }

var errAttemptTimeout net.Error = attemptTimeout{}

// permanent reports errors that a retry cannot fix: a certificate that does
// not verify, TLS spoken to a plain-HTTP port, a host name that does not
// exist.
func permanent(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return true
	}
	var cv *tls.CertificateVerificationError
	var rh tls.RecordHeaderError
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	return errors.As(err, &cv) || errors.As(err, &rh) || errors.As(err, &ua) ||
		errors.As(err, &he) || errors.As(err, &ci)
}

func idempotent(r *http.Request) bool {
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

func replayable(r *http.Request) bool {
	return r.Body == nil || r.Body == http.NoBody || r.GetBody != nil
}

func rewind(r *http.Request) (*http.Request, error) {
	c := r.Clone(r.Context())
	if r.Body != nil && r.Body != http.NoBody {
		b, err := r.GetBody()
		if err != nil {
			return nil, err
		}
		c.Body = b
	}
	return c, nil
}

// retryAfter is the wait a 429 asks for: Retry-After in seconds or as an
// HTTP date, 5 seconds when absent, at most maxRetryAfter. It is not cut to
// MaxWait: a longer wait is refused by the caller, not shortened.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	d := defaultRetryAfter
	if v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil {
			// Compare before multiplying: a large value would overflow.
			if s > int64(maxRetryAfter/time.Second) {
				d = maxRetryAfter
			} else {
				d = time.Duration(s) * time.Second
			}
		} else if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(v, "-") {
			d = maxRetryAfter
		} else if at, err := http.ParseTime(v); err == nil {
			d = at.Sub(now)
		}
	}
	if d < 0 {
		d = 0
	}
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, drainLimit))
	_ = resp.Body.Close()
}

// SleepCtx waits for d or until ctx is done. It returns ctx.Err() on
// cancellation and nil when d elapses; the timer is always stopped.
func SleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}

// errRedirectDowngrade stops a redirect from https to plain http.
var errRedirectDowngrade = errors.New("refusing to follow a redirect from https to http")

// checkRedirect keeps the standard limit of redirects, refuses to leave
// https and drops credentials when a redirect leaves the origin of the first
// request. The standard library keeps Authorization for the same host name
// on another port or scheme and for subdomains; the session token must only
// reach the server it was issued by.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	first := via[0].URL
	if strings.EqualFold(first.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
		return errRedirectDowngrade
	}
	if origin(req.URL) != origin(first) {
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
	}
	return nil
}

// origin is the scheme, lower-case host name and port (default ports filled
// in) of u.
func origin(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// Options configure New.
type Options struct {
	URL       string
	Token     string
	PerSecond float64
	Burst     int
	UserAgent string
	// StatePath shares the rate-limit state between runs; empty keeps it in
	// this process.
	StatePath string
	// Notify receives a line before each wait for the server (stderr).
	Notify io.Writer
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// New returns a Client4 that goes through the limiting transport. There is
// no overall http.Client timeout: it would also cut waits the server asked
// for and long downloads; each attempt is bounded by DefaultAttemptTimeout.
func New(o Options) *model.Client4 {
	c := model.NewAPIv4Client(o.URL)
	tr := NewTransport(nil, o.PerSecond, o.Burst)
	if o.Now != nil {
		tr.Now = o.Now
	}
	tr.Notify = o.Notify
	if o.StatePath != "" {
		tr.UseState(o.StatePath)
	}
	c.HTTPClient = &http.Client{Transport: tr, CheckRedirect: checkRedirect}
	if o.UserAgent != "" {
		c.HTTPHeader["User-Agent"] = o.UserAgent
	}
	if o.Token != "" {
		c.SetToken(o.Token)
	}
	return c
}
