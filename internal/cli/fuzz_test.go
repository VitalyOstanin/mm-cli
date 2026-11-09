package cli

import (
	"testing"
	"time"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

func FuzzParseSince(f *testing.F) {
	for _, s := range []string{
		"90m", "2h", "3d", "0d", "-3d", "2026-01-02T15:04:05Z", "2026-01-02T15:04:05+03:00",
		"213504d", "106751d", "106752d", "99999999999999999999d", "1.5h", "", "d", "soon",
	} {
		f.Add(s)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, s string) {
		got, err := parseSince(s, now)
		if err != nil {
			if clierr.ExitCode(err) != clierr.CodeUsage {
				t.Fatalf("parseSince(%q) error %v is not a usage error", s, err)
			}
			return
		}
		if _, perr := time.Parse(time.RFC3339, s); perr == nil {
			return // an absolute time may lie in the future
		}
		if !got.Before(now) {
			t.Fatalf("parseSince(%q) = %v, not before now", s, got)
		}
		// Monotonic: the same value with one more day goes further back.
		if len(s) > 1 && s[len(s)-1] == 'd' {
			if bigger, err := parseSince("1"+s, now); err == nil && bigger.After(got) {
				t.Fatalf("parseSince(%q) = %v is earlier than parseSince(%q) = %v", s, got, "1"+s, bigger)
			}
		}
	})
}

// timeSeeds are inputs of the time flags: presets, both time forms, and
// near misses.
var timeSeeds = []string{
	"tomorrow", "monday", "next-monday", "Monday", " tomorrow ", "today", "this-week",
	"30m", "1h", "4h", "90m", "-1h", "0s", "2562047h", "2562048h",
	"2026-06-02 10:00", "2026-06-01 11:59", "2026-13-01 10:00", "2026-06-02T10:00:00Z",
	"2026-06-02T10:00:00+03:00", "2026-06-01T11:00:00Z", "", "soon", "9999-12-31 23:59",
}

// fuzzNow returns the fixed base time moved by m minutes, so that the
// presets meet every weekday and day boundary.
func fuzzNow(m int16) time.Time {
	return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(m) * time.Minute)
}

var fuzzZones = func() [2]*time.Location {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		panic(err)
	}
	return [2]*time.Location{time.UTC, berlin}
}()

// checkTimeResult holds for every time flag: errors are usage errors, a
// result lies in the future, and a value that needs no zone does not depend
// on it.
func checkTimeResult(t *testing.T, flag, s string, needsZone bool, parse func(*time.Location) (time.Time, error), now time.Time) {
	t.Helper()
	got, err := parse(fuzzZones[0])
	if err != nil {
		if clierr.ExitCode(err) != clierr.CodeUsage {
			t.Fatalf("%s %q: error %v is not a usage error", flag, s, err)
		}
		return
	}
	if !got.After(now) {
		t.Fatalf("%s %q = %v, not after now %v", flag, s, got, now)
	}
	if !needsZone {
		if other, err := parse(fuzzZones[1]); err != nil || !other.Equal(got) {
			t.Fatalf("%s %q depends on the zone: %v, %v (%v)", flag, s, got, other, err)
		}
	}
}

func FuzzParseWhen(f *testing.F) {
	for _, s := range timeSeeds {
		f.Add(s, int16(0))
	}
	f.Fuzz(func(t *testing.T, s string, m int16) {
		now := fuzzNow(m)
		parse := func(loc *time.Location) (time.Time, error) { return parseWhen(s, now, loc) }
		checkTimeResult(t, "<when>", s, needsZone(s), parse, now)
		// The syntax check, run before any request, accepts all that parses.
		if _, err := parse(time.UTC); err == nil && whenSyntax(s) != nil {
			t.Fatalf("whenSyntax rejects %q, which parses", s)
		}
	})
}

func FuzzParseUntil(f *testing.F) {
	for _, s := range timeSeeds {
		f.Add(s, int16(0))
	}
	f.Fuzz(func(t *testing.T, s string, m int16) {
		now := fuzzNow(m)
		parse := func(loc *time.Location) (time.Time, error) { return parseUntil(s, now, loc) }
		checkTimeResult(t, "--until", s, untilNeedsZone(s), parse, now)
		if _, err := parse(time.UTC); err == nil && untilSyntax(s) != nil {
			t.Fatalf("untilSyntax rejects %q, which parses", s)
		}
	})
}

func FuzzCustomExpiry(f *testing.F) {
	for _, s := range timeSeeds {
		f.Add(s, int16(0))
	}
	durations := map[string]bool{"date_and_time": true}
	for _, d := range clearAfterPresets {
		durations[d] = true
	}
	f.Fuzz(func(t *testing.T, s string, m int16) {
		now := fuzzNow(m)
		parse := func(loc *time.Location) (time.Time, error) {
			d, at, err := customExpiry(s, now, loc)
			if err == nil && !durations[d] {
				t.Fatalf("--clear-after %q: unknown duration %q", s, d)
			}
			return at, err
		}
		checkTimeResult(t, "--clear-after", s, clearAfterNeedsZone(s), parse, now)
	})
}
