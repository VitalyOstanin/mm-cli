package client

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"time"

	"golang.org/x/time/rate"

	"github.com/VitalyOstanin/mm-cli/internal/config"
)

// StateFile is the name of the rate-limit state file in a profile's cache
// directory.
const StateFile = "ratelimit.json"

const (
	// recoverInterval is how often a slowed-down rate grows by growFactor
	// between runs: from the floor of one request in 10 seconds back to 1
	// request per second takes about three minutes.
	recoverInterval = 30 * time.Second
	// maxStateBytes is how much of the state file is read.
	maxStateBytes = 4 << 10
)

// rateState is what one run tells the next ones about the server: no request
// before NotBefore, and the rate was cut to Limit at LimitAt. It is written
// only when it changes (a 429, or a refusal to wait) and removed when the rate
// is back to full.
type rateState struct {
	NotBefore time.Time `json:"not_before,omitzero"`
	Limit     float64   `json:"limit,omitempty"`
	LimitAt   time.Time `json:"limit_at,omitzero"`
}

// loadState reads the state file. A missing, unreadable, damaged or foreign
// file yields false: the state is only a hint and never stops the tool.
func loadState(path string) (rateState, bool) {
	f, err := os.Open(path)
	if err != nil {
		return rateState{}, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxStateBytes))
	if err != nil {
		return rateState{}, false
	}
	var s rateState
	if err := json.Unmarshal(data, &s); err != nil {
		return rateState{}, false
	}
	if math.IsNaN(s.Limit) || math.IsInf(s.Limit, 0) || s.Limit <= 0 || s.LimitAt.IsZero() {
		s.Limit, s.LimitAt = 0, time.Time{}
	}
	return s, true
}

// effectiveLimit is the rate a state allows at now: the saved rate grown by
// growFactor for every recoverInterval since it was cut. full reports that
// the rate is back to max.
func effectiveLimit(s rateState, now time.Time, max rate.Limit) (l rate.Limit, full bool) {
	if s.Limit <= 0 || rate.Limit(s.Limit) >= max {
		return max, true
	}
	l = rate.Limit(s.Limit)
	if l < minLimit {
		l = minLimit
	}
	if now.After(s.LimitAt) {
		for steps := now.Sub(s.LimitAt) / recoverInterval; steps > 0 && l < max; steps-- {
			l *= growFactor
		}
	}
	if l >= max {
		return max, true
	}
	return l, false
}

// merge combines two states read from or meant for the same file, as when
// two runs write at once: the later ban and the slower rate win.
func merge(a, b rateState, now time.Time, max rate.Limit) rateState {
	out := a
	if b.NotBefore.After(out.NotBefore) {
		out.NotBefore = b.NotBefore
	}
	la, _ := effectiveLimit(a, now, max)
	lb, _ := effectiveLimit(b, now, max)
	if lb < la {
		out.Limit, out.LimitAt = b.Limit, b.LimitAt
	}
	return out
}

// saveState merges s with the file and writes the result atomically. There
// is no lock: concurrent writers may each lose the other's update, which
// costs at most one more 429.
func saveState(path string, s rateState, now time.Time, max rate.Limit) error {
	if cur, ok := loadState(path); ok {
		s = merge(s, cur, now, max)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

// clearState removes the file once this run is back to full rate, unless
// another run has written a newer cut or a ban that still holds.
func clearState(path string, since, now time.Time) error {
	cur, ok := loadState(path)
	if ok && (cur.LimitAt.After(since) || cur.NotBefore.After(now)) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
