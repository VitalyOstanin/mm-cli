package client

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	minLimit     = rate.Limit(0.1) // one request in 10 seconds
	recoverAfter = 20
	growFactor   = 1.5
)

// adaptive slows the limiter down after each 429 and recovers gradually.
// It also honours X-RateLimit-Remaining and X-RateLimit-Reset.
type adaptive struct {
	mu        sync.Mutex
	bucket    *rate.Limiter
	max       rate.Limit
	maxBurst  int
	successes int
	notBefore time.Time
	// cutAt is when the rate was last cut, here or by the run whose state
	// was restored.
	cutAt time.Time
}

func newAdaptive(perSecond float64, burst int) *adaptive {
	return &adaptive{
		bucket:   rate.NewLimiter(rate.Limit(perSecond), burst),
		max:      rate.Limit(perSecond),
		maxBurst: burst,
	}
}

// onThrottled halves the rate after a 429 and bans requests until the
// given time. It returns the state to share with later runs.
func (a *adaptive) onThrottled(now, until time.Time) rateState {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.successes = 0
	l := a.bucket.Limit() / 2
	if l < minLimit {
		l = minLimit
	}
	if l > a.max {
		l = a.max
	}
	a.bucket.SetLimitAt(now, l)
	a.bucket.SetBurstAt(now, 1)
	a.cutAt = now
	if until.After(a.notBefore) {
		a.notBefore = until
	}
	return rateState{NotBefore: a.notBefore, Limit: float64(l), LimitAt: now}
}

// onSuccess counts a successful response and grows the rate after
// recoverAfter of them. It reports whether the rate has just come back to
// full.
func (a *adaptive) onSuccess(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bucket.Limit() >= a.max {
		return false
	}
	a.successes++
	if a.successes < recoverAfter {
		return false
	}
	a.successes = 0
	l := a.bucket.Limit() * growFactor
	full := l >= a.max
	if full {
		l = a.max
		a.bucket.SetBurstAt(now, a.maxBurst)
	}
	a.bucket.SetLimitAt(now, l)
	return full
}

// restore starts from the state left by earlier runs.
func (a *adaptive) restore(s rateState, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if limit := now.Add(maxRetryAfter); s.NotBefore.After(limit) {
		s.NotBefore = limit
	}
	if s.NotBefore.After(a.notBefore) {
		a.notBefore = s.NotBefore
	}
	if l, full := effectiveLimit(s, now, a.max); !full {
		a.bucket.SetLimitAt(now, l)
		a.bucket.SetBurstAt(now, 1)
		a.cutAt = s.LimitAt
	}
}

// snapshot is the state to share with later runs.
func (a *adaptive) snapshot() rateState {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := rateState{NotBefore: a.notBefore}
	if l := a.bucket.Limit(); l < a.max {
		s.Limit, s.LimitAt = float64(l), a.cutAt
	}
	return s
}

func (a *adaptive) cutTime() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cutAt
}

// observe reads the server's rate-limit headers. Mattermost sends Reset as
// seconds until the window resets; a value that looks like a Unix time is
// treated as one. The wait is not shortened here: the transport refuses to
// wait longer than MaxWait instead of sending early.
func (a *adaptive) observe(h http.Header, now time.Time) {
	rem := strings.TrimSpace(h.Get("X-Ratelimit-Remaining"))
	reset := strings.TrimSpace(h.Get("X-Ratelimit-Reset"))
	if rem == "" || reset == "" {
		return
	}
	n, err1 := strconv.Atoi(rem)
	r, err2 := strconv.ParseInt(reset, 10, 64)
	if err1 != nil || err2 != nil || n > 1 || r <= 0 {
		return
	}
	var at time.Time
	if r < 1_000_000_000 {
		at = now.Add(time.Duration(r) * time.Second)
	} else {
		at = time.Unix(r, 0)
	}
	if limit := now.Add(maxRetryAfter); at.After(limit) {
		at = limit
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if at.After(a.notBefore) {
		a.notBefore = at
	}
}

// pause is how long to wait before the next request, and until when.
func (a *adaptive) pause(now time.Time) (time.Duration, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.notBefore.Sub(now); d > 0 {
		return d, a.notBefore
	}
	return 0, time.Time{}
}

func (a *adaptive) limit() (rate.Limit, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bucket.Limit(), a.bucket.Burst()
}
