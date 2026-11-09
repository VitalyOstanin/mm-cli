package render

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestTimeHelpers(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	if got := LocalTime("2026-03-04T12:30:00Z", berlin); got != "2026-03-04 13:30" {
		t.Errorf("LocalTime: %q", got)
	}
	if got := LocalTime("soon", berlin); got != "soon" {
		t.Errorf("LocalTime of a bad value: %q", got)
	}
	ms := time.Date(2026, 3, 4, 12, 30, 0, 0, time.UTC).UnixMilli()
	for in, want := range map[int64]string{ms: "2026-03-04T12:30:00Z", 0: "", -1: ""} {
		if got := RFC3339(in); got != want {
			t.Errorf("RFC3339(%d) = %q, want %q", in, got, want)
		}
	}
}
