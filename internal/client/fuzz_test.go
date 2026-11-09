package client

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func FuzzRetryAfter(f *testing.F) {
	for _, s := range []string{
		"", "0", "1", "999", "3600", "3601", "10000000000", "99999999999999999999",
		"-5", "-99999999999999999999", "soon", "Wed, 21 Oct 2026 07:28:00 GMT", " 7 ",
	} {
		f.Add(s)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, v string) {
		d := retryAfter(v, now)
		if d < 0 || d > maxRetryAfter {
			t.Fatalf("retryAfter(%q) = %v, outside [0, %v]", v, d, maxRetryAfter)
		}
		// Monotonic in whole seconds.
		if s, err := strconv.ParseInt(v, 10, 64); err == nil && s >= 0 && s < 1<<62 {
			if next := retryAfter(strconv.FormatInt(s+1, 10), now); next < d {
				t.Fatalf("retryAfter(%d) = %v > retryAfter(%d) = %v", s, d, s+1, next)
			}
			if s <= int64(maxRetryAfter/time.Second) && d != time.Duration(s)*time.Second {
				t.Fatalf("retryAfter(%d) = %v", s, d)
			}
		}
	})
}

func FuzzObserve(f *testing.F) {
	for _, s := range [][2]string{
		{"0", "30"}, {"1", "5"}, {"2", "5"}, {"0", "1900000000"}, {"0", "99999999999999999"},
		{"0", "0"}, {"-1", "10"}, {"x", "y"}, {"", "10"}, {"0", "-5"}, {"0", "999999999"},
	} {
		f.Add(s[0], s[1])
	}
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, rem, reset string) {
		a := newAdaptive(1, 3)
		h := http.Header{}
		h.Set("X-Ratelimit-Remaining", rem)
		h.Set("X-Ratelimit-Reset", reset)
		a.observe(h, now)
		d, until := a.pause(now)
		if d < 0 || d > maxRetryAfter {
			t.Fatalf("observe(%q, %q): pause %v outside [0, %v]", rem, reset, d, maxRetryAfter)
		}
		if d > 0 && !until.Equal(now.Add(d)) {
			t.Fatalf("observe(%q, %q): pause %v and deadline %v disagree", rem, reset, d, until)
		}
		// A repeated observation never moves the ban earlier.
		a.observe(h, now)
		if d2, _ := a.pause(now); d2 < d {
			t.Fatalf("observe(%q, %q): ban shrank from %v to %v", rem, reset, d, d2)
		}
	})
}
