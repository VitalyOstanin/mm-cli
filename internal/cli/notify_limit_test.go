package cli

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDesktopNotifierCapsConcurrentProcesses(t *testing.T) {
	release := make(chan struct{})
	var cur, peak, started atomic.Int32
	var once sync.Once
	startedCap := make(chan struct{})
	d := &desktopNotifier{goos: "windows", links: map[uint32]string{}}
	d.run = func(ctx context.Context, name string, args ...string) (string, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		if started.Add(1) == notifyMaxProcs {
			once.Do(func() { close(startedCap) })
		}
		<-release
		cur.Add(-1)
		return "", nil
	}
	for range 50 {
		d.Notify(context.Background(), Notification{Title: "t", Body: "b"})
	}
	<-startedCap
	close(release)
	d.Close()
	if got := peak.Load(); got > notifyMaxProcs {
		t.Fatalf("peak concurrent runs = %d, want <= %d", got, notifyMaxProcs)
	}
	if got := started.Load(); got != notifyMaxProcs {
		t.Fatalf("started = %d, want %d (the rest dropped)", got, notifyMaxProcs)
	}
}

// Notify and NotifyNow share one group counter: terminal-notifier replaces a
// notification that has the same group, so each call needs its own.
func TestDesktopNotifierGroupsAreDistinct(t *testing.T) {
	var mu sync.Mutex
	var groups []string
	d := &desktopNotifier{goos: "darwin", terminal: true, links: map[uint32]string{}}
	d.run = func(ctx context.Context, name string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		for i, a := range args {
			if a == "-group" && i+1 < len(args) {
				groups = append(groups, args[i+1])
			}
		}
		return "", nil
	}
	d.Notify(context.Background(), Notification{Title: "t", Body: "b"})
	d.Close()
	d.NotifyNow(context.Background(), Notification{Title: "t", Body: "b"})
	mu.Lock()
	defer mu.Unlock()
	if len(groups) != 2 || groups[0] != "mm-cli-1" || groups[1] != "mm-cli-2" {
		t.Fatalf("groups = %v, want [mm-cli-1 mm-cli-2]", groups)
	}
}
