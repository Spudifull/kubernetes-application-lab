package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestValidateMemoryParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mib     int
		hold    time.Duration
		wantErr bool
	}{
		{name: "minimum", mib: 1, hold: time.Second},
		{name: "maximum", mib: maxMemoryMiB, hold: maxMemoryHold},
		{name: "zero memory", mib: 0, hold: time.Second, wantErr: true},
		{name: "too much memory", mib: maxMemoryMiB + 1, hold: time.Second, wantErr: true},
		{name: "negative memory", mib: -1, hold: time.Second, wantErr: true},
		{name: "short hold", mib: 1, hold: time.Millisecond, wantErr: true},
		{name: "long hold", mib: 1, hold: maxMemoryHold + time.Second, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMemoryParams(test.mib, test.hold); (err != nil) != test.wantErr {
				t.Fatalf("validateMemoryParams(%d, %s) = %v, wantErr=%t", test.mib, test.hold, err, test.wantErr)
			}
		})
	}
}

func TestAllocateAndTouchMemory(t *testing.T) {
	t.Parallel()
	buffer, err := allocateAndTouchMemory(context.Background(), 1)
	if err != nil {
		t.Fatalf("allocate 1 MiB: %v", err)
	}
	if len(buffer) != 1024*1024 {
		t.Fatalf("allocated %d bytes, want 1 MiB", len(buffer))
	}
	for offset := 0; offset < len(buffer); offset += os.Getpagesize() {
		if buffer[offset] != 0xa5 {
			t.Fatalf("page at offset %d was not touched", offset)
		}
	}
	if buffer[len(buffer)-1] != 0xa5 {
		t.Fatal("last byte was not touched")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := allocateAndTouchMemory(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled allocation = %v, want context.Canceled", err)
	}
	if _, err := allocateAndTouchMemory(context.Background(), maxMemoryMiB+1); err == nil {
		t.Fatal("oversized allocation was accepted")
	}
}

func TestRunMemoryUsesBoundedHold(t *testing.T) {
	t.Parallel()
	called := false
	err := runMemory(context.Background(), "test-run", 1, time.Second, func(ctx context.Context, hold time.Duration) error {
		called = true
		if hold != time.Second || ctx.Err() != nil {
			t.Errorf("hold=%s context error=%v", hold, ctx.Err())
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("runMemory error=%v wait called=%t", err, called)
	}
}
