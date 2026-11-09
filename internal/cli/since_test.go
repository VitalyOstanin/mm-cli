package cli

import (
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/clierr"
)

// Counts large enough to overflow time.Duration must fail, not wrap around
// to a recent time.
func TestParseSinceOverflow(t *testing.T) {
	for _, in := range []string{"106752d", "213504d", "9223372036854775807d", "99999999999999999999d", "9999999999h"} {
		if _, err := parseSince(in, now); clierr.ExitCode(err) != clierr.CodeUsage {
			t.Errorf("parseSince(%q) err = %v", in, err)
		}
	}
	if _, err := parseSince("106751d", now); err != nil {
		t.Errorf("parseSince(106751d) = %v", err)
	}
}
