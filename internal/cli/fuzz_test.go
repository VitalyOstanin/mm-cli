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
