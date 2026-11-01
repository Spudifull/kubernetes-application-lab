package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"
)

const (
	maxMemoryMiB  = 128
	maxMemoryHold = 60 * time.Second
)

func validateMemoryParams(mib int, hold time.Duration) error {
	if mib < 1 || mib > maxMemoryMiB {
		return fmt.Errorf("memory-mib must be between 1 and %d", maxMemoryMiB)
	}
	if hold < time.Second || hold > maxMemoryHold {
		return fmt.Errorf("memory-hold must be between 1s and %s", maxMemoryHold)
	}
	return nil
}

// allocateAndTouchMemory faults each page into the container's memory cgroup.
// The bound is validated before multiplication or allocation.
func allocateAndTouchMemory(ctx context.Context, mib int) ([]byte, error) {
	if mib < 1 || mib > maxMemoryMiB {
		return nil, fmt.Errorf("memory-mib must be between 1 and %d", maxMemoryMiB)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	buffer := make([]byte, mib*1024*1024)
	pageSize := os.Getpagesize()
	for offset := 0; offset < len(buffer); offset += pageSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		buffer[offset] = 0xa5
	}
	buffer[len(buffer)-1] = 0xa5
	return buffer, nil
}

func runMemory(ctx context.Context, runID string, mib int, hold time.Duration, wait func(context.Context, time.Duration) error) error {
	if err := validateMemoryParams(mib, hold); err != nil {
		return err
	}
	buffer, err := allocateAndTouchMemory(ctx, mib)
	if err != nil {
		return err
	}
	writeEvent("memory_pages_touched", map[string]any{"run_id": runID, "time": time.Now().UTC(), "memory_mib": mib})
	err = wait(ctx, hold)
	runtime.KeepAlive(buffer)
	return err
}

func waitForMemory(ctx context.Context, hold time.Duration) error {
	timer := time.NewTimer(hold)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
