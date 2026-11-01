package scheduled

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// HoldDuration is an explicit laboratory delay before any SQL work.
func HoldDuration(seconds string) (time.Duration, error) {
	if seconds == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(seconds)
	if err != nil || n < 0 || n > 90 {
		return 0, errors.New("LAB_CRON_HOLD_SECONDS must be an integer from 0 to 90")
	}
	return time.Duration(n) * time.Second, nil
}

func Hold(ctx context.Context, delay time.Duration) error {
	if delay == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
