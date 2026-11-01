package scheduled

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHoldBoundsAndCancellation(t *testing.T) {
	for _, value := range []string{"-1", "91", "75s", "9999999999999999999999999"} {
		if _, err := HoldDuration(value); err == nil {
			t.Fatalf("accepted hold %q", value)
		}
	}
	if delay, err := HoldDuration("75"); err != nil || delay != 75*time.Second {
		t.Fatal("bounded hold rejected")
	}
	if delay, err := HoldDuration(""); err != nil || delay != 0 {
		t.Fatal("default must not delay SQL")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, delay := range []time.Duration{0, 75 * time.Second} {
		if err := Hold(ctx, delay); !errors.Is(err, context.Canceled) {
			t.Fatal("hold did not propagate cancellation", err)
		}
	}
}
