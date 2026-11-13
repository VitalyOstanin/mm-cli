package cli

import "testing"

func TestUrgencyHintIsByte(t *testing.T) {
	for _, c := range []struct {
		level urgency
		want  byte
	}{{urgencyLow, 0}, {urgencyNormal, 1}, {urgencyCritical, 2}} {
		v := urgencyHint(c.level)
		if got := v.Signature().String(); got != "y" {
			t.Errorf("level %d: signature = %q, want y", c.level, got)
		}
		if b, ok := v.Value().(byte); !ok || b != c.want {
			t.Errorf("level %d: value = %#v, want byte %d", c.level, v.Value(), c.want)
		}
	}
}
