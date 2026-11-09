package client

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSleepCtxCanceledReturnsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := SleepCtx(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("SleepCtx did not return promptly after cancel")
	}
}

func TestSleepCtxElapsedReturnsNil(t *testing.T) {
	if err := SleepCtx(context.Background(), 10*time.Millisecond); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if err := SleepCtx(context.Background(), 0); err != nil {
		t.Fatalf("zero duration: err = %v, want nil", err)
	}
}
