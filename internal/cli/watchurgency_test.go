package cli

import (
	"strings"
	"testing"

	"github.com/VitalyOstanin/mm-cli/internal/fakemm"
)

// useFakeBus makes watch --notify on "linux" talk to a fake bus.
func useFakeBus(t *testing.T) *fakeNotifyBus {
	t.Helper()
	bus := newFakeNotifyBus()
	old := dialNotifyBus
	dialNotifyBus = func() (notifyBus, error) { return bus, nil }
	t.Cleanup(func() { dialNotifyBus = old })
	return bus
}

func TestWatchUrgencyReachesBusForEvents(t *testing.T) {
	for _, c := range []struct {
		args []string
		want urgency
	}{
		{[]string{"--urgency", "low"}, urgencyLow},
		{[]string{"--urgency", "normal"}, urgencyNormal},
		{[]string{"--urgency", "critical"}, urgencyCritical},
		{nil, 1}, // the default is normal, sent explicitly
	} {
		t.Run(strings.Join(c.args, "_"), func(t *testing.T) {
			bus := useFakeBus(t)
			r := reactServer(t, func(town string) [][]byte {
				return [][]byte{postedBy(t, town, bobID, "@bob", fakemm.ID("p1"), "hello")}
			})
			r.app.GOOS = "linux"
			out, _, stop := startWatch(t, r.app, append([]string{"--notify"}, c.args...)...)
			waitFor(t, "the message", func() bool { return strings.Contains(out.String(), "hello") })
			stop()
			bus.mu.Lock()
			defer bus.mu.Unlock()
			if len(bus.shown) != 1 || bus.shown[0].urgency != c.want {
				t.Errorf("shown = %+v, want one notification with urgency %d", bus.shown, c.want)
			}
		})
	}
}

func TestWatchUrgencyReachesBusForSignInNotice(t *testing.T) {
	app, _ := newTestApp(t)
	app.GOOS = "linux"
	if err := app.Store.DeleteSession("test"); err != nil {
		t.Fatal(err)
	}
	bus := useFakeBus(t)
	r := run(app, "watch", "--notify", "--urgency", "critical")
	mustCode(t, r, 3)
	if len(bus.shown) != 1 || bus.shown[0].urgency != urgencyCritical {
		t.Errorf("shown = %+v, want the sign-in notice with urgency 2", bus.shown)
	}
}

func TestWatchUrgencyUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--notify", "--urgency", "urgent"},
		{"--urgency", "critical"},
		{"--urgency", "bogus"},
	} {
		app, s := newTestApp(t)
		app.GOOS = "linux"
		useFakeBus(t)
		r := run(app, append([]string{"watch"}, args...)...)
		mustCode(t, r, 2)
		if s.Total() != 0 {
			t.Errorf("%v: %d requests before the check", args, s.Total())
		}
		if strings.Contains(strings.Join(args, " "), "critical") && !strings.Contains(r.err, "--urgency needs --notify") {
			t.Errorf("%v: stderr %q", args, r.err)
		}
	}
}
