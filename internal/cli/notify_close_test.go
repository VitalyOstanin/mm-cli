package cli

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestDesktopNotifierCloseReturnsWhenBusOnlyStopsForwarder(t *testing.T) {
	bus := newFakeNotifyBus()
	d := &desktopNotifier{goos: "linux", bus: bus, links: map[uint32]string{}}
	d.wg.Add(1)
	go d.listen(bus.signals())

	closed := make(chan struct{})
	go func() { d.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return: forwarder left the signals channel open")
	}
}

func TestForwardNotifySignalsDeliversAndCloses(t *testing.T) {
	raw := make(chan *dbus.Signal, 4)
	out := make(chan notifySignal, 4)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() { forwardNotifySignals(raw, out, done); close(finished) }()

	raw <- &dbus.Signal{Name: notifyDest + ".ActionInvoked", Body: []any{uint32(7), "default"}}
	raw <- &dbus.Signal{Name: notifyDest + ".NotificationClosed", Body: []any{uint32(8), uint32(1)}}
	raw <- &dbus.Signal{Name: notifyDest + ".Other", Body: []any{uint32(9)}}
	if got := <-out; got != (notifySignal{id: 7, action: true}) {
		t.Fatalf("first signal = %+v", got)
	}
	if got := <-out; got != (notifySignal{id: 8}) {
		t.Fatalf("second signal = %+v", got)
	}
	close(done)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("forwarder did not stop")
	}
	if _, ok := <-out; ok {
		t.Fatal("out must be closed after the forwarder stops")
	}
}
