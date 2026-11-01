package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	maxCPUUnits          = 100
	iterationsPerCPUUnit = 1_000_000
	maxConcurrentCPUWork = 2
	maxCPUWallTime       = 8 * time.Second
)

// cpuStats is a snapshot of the container's own cgroup quota counters.
// A missing quota is represented by nil rather than an invented limit.
type cpuStats struct {
	CgroupVersion int     `json:"cgroup_version"`
	UsageUsec     *uint64 `json:"usage_usec"`
	Periods       uint64  `json:"nr_periods"`
	Throttled     uint64  `json:"nr_throttled"`
	ThrottledUsec uint64  `json:"throttled_usec"`
	QuotaUsec     *int64  `json:"quota_us"`
	PeriodUsec    uint64  `json:"period_us"`
}

func (a *App) handleCPU(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()["units"]
	if len(values) != 1 {
		http.Error(w, "units must appear exactly once", http.StatusBadRequest)
		return
	}
	units, err := strconv.Atoi(values[0])
	if err != nil || units < 1 || units > maxCPUUnits {
		http.Error(w, "units must be between 1 and 100", http.StatusBadRequest)
		return
	}
	if len(r.Header.Get("X-Lab-Run-ID")) > 64 {
		http.Error(w, "X-Lab-Run-ID exceeds 64 bytes", http.StatusBadRequest)
		return
	}
	select {
	case a.cpuSlots <- struct{}{}:
		defer func() { <-a.cpuSlots }()
	default:
		http.Error(w, "CPU experiment is at capacity", http.StatusTooManyRequests)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), maxCPUWallTime)
	defer cancel()
	started := time.Now()
	runID := r.Header.Get("X-Lab-Run-ID")
	a.logger.Info("cpu_work_started", "pod", a.podName, "run_id", runID, "units", units)
	compute := a.cpuCompute
	if compute == nil {
		compute = computeCPU
	}
	checksum, err := compute(ctx, units)
	if err != nil {
		a.logger.Info("cpu_work_stopped", "pod", a.podName, "run_id", runID, "error", err)
		if r.Context().Err() == nil {
			http.Error(w, "CPU experiment exceeded its time limit", http.StatusGatewayTimeout)
		}
		return
	}
	elapsed := time.Since(started)
	a.logger.Info("cpu_work_completed", "pod", a.podName, "run_id", runID, "units", units, "elapsed_ms", elapsed.Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Units     int    `json:"units"`
		Checksum  uint64 `json:"checksum"`
		ElapsedMS int64  `json:"elapsed_ms"`
	}{Units: units, Checksum: checksum, ElapsedMS: elapsed.Milliseconds()})
}

// computeCPU performs fixed integer work, checking cancellation frequently.
// The returned checksum prevents the compiler from discarding the loop.
func computeCPU(ctx context.Context, units int) (uint64, error) {
	var checksum uint64 = 14695981039346656037
	for unit := 0; unit < units; unit++ {
		for i := 0; i < iterationsPerCPUUnit; i++ {
			if i%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
			}
			checksum = (checksum ^ uint64(i+unit)) * 1099511628211
		}
	}
	return checksum, ctx.Err()
}

func (a *App) handleCPUStat(w http.ResponseWriter, _ *http.Request) {
	read := a.readCPUStats
	if read == nil {
		read = readContainerCPUStats
	}
	stats, err := read()
	if err != nil {
		a.logger.Error("cpu_stat_unavailable", "pod", a.podName, "error", err)
		http.Error(w, "container CPU statistics unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

func readContainerCPUStats() (cpuStats, error) {
	data, err := readSmallFile("/sys/fs/cgroup/cpu.stat")
	if err == nil {
		quota, quotaErr := readSmallFile("/sys/fs/cgroup/cpu.max")
		if quotaErr != nil {
			return cpuStats{}, fmt.Errorf("read cgroup v2 quota: %w", quotaErr)
		}
		return parseCPUStatV2(string(data), string(quota))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return cpuStats{}, fmt.Errorf("read cgroup v2 counters: %w", err)
	}
	data, err = readSmallFile("/sys/fs/cgroup/cpu/cpu.stat")
	if err != nil {
		return cpuStats{}, fmt.Errorf("read cgroup v1 counters: %w", err)
	}
	quota, err := readSmallFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	if err != nil {
		return cpuStats{}, fmt.Errorf("read cgroup v1 quota: %w", err)
	}
	period, err := readSmallFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if err != nil {
		return cpuStats{}, fmt.Errorf("read cgroup v1 period: %w", err)
	}
	return parseCPUStatV1(string(data), string(quota), string(period))
}

func readSmallFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return nil, err
	}
	if len(data) > 4096 {
		return nil, fmt.Errorf("%s exceeds 4096 bytes", path)
	}
	return data, nil
}

func parseCPUStatV2(data, quota string) (cpuStats, error) {
	fields := strings.Fields(quota)
	if len(fields) != 2 {
		return cpuStats{}, fmt.Errorf("invalid cgroup v2 cpu.max")
	}
	period, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || period == 0 {
		return cpuStats{}, fmt.Errorf("invalid cgroup v2 period")
	}
	stats := cpuStats{CgroupVersion: 2, PeriodUsec: period}
	if fields[0] != "max" {
		value, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || value <= 0 {
			return cpuStats{}, fmt.Errorf("invalid cgroup v2 quota")
		}
		stats.QuotaUsec = &value
	}
	values, err := parseCounterLines(data)
	if err != nil {
		return cpuStats{}, err
	}
	usage, err := requiredCounter(values, "usage_usec")
	if err != nil {
		return cpuStats{}, err
	}
	stats.UsageUsec = &usage
	if stats.Periods, err = requiredCounter(values, "nr_periods"); err != nil {
		return cpuStats{}, err
	}
	if stats.Throttled, err = requiredCounter(values, "nr_throttled"); err != nil {
		return cpuStats{}, err
	}
	if stats.ThrottledUsec, err = requiredCounter(values, "throttled_usec"); err != nil {
		return cpuStats{}, err
	}
	return stats, nil
}

func parseCPUStatV1(data, quota, period string) (cpuStats, error) {
	quotaValue, err := strconv.ParseInt(strings.TrimSpace(quota), 10, 64)
	if err != nil || quotaValue == 0 || quotaValue < -1 {
		return cpuStats{}, fmt.Errorf("invalid cgroup v1 quota")
	}
	periodValue, err := strconv.ParseUint(strings.TrimSpace(period), 10, 64)
	if err != nil || periodValue == 0 {
		return cpuStats{}, fmt.Errorf("invalid cgroup v1 period")
	}
	stats := cpuStats{CgroupVersion: 1, PeriodUsec: periodValue}
	if quotaValue > 0 {
		stats.QuotaUsec = &quotaValue
	}
	values, err := parseCounterLines(data)
	if err != nil {
		return cpuStats{}, err
	}
	if stats.Periods, err = requiredCounter(values, "nr_periods"); err != nil {
		return cpuStats{}, err
	}
	if stats.Throttled, err = requiredCounter(values, "nr_throttled"); err != nil {
		return cpuStats{}, err
	}
	nanoseconds, err := requiredCounter(values, "throttled_time")
	if err != nil {
		return cpuStats{}, err
	}
	stats.ThrottledUsec = nanoseconds / 1000
	return stats, nil
}

func parseCounterLines(data string) (map[string]uint64, error) {
	values := make(map[string]uint64)
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid cgroup counter line")
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid cgroup counter %s: %w", fields[0], err)
		}
		values[fields[0]] = value
	}
	return values, nil
}

func requiredCounter(values map[string]uint64, key string) (uint64, error) {
	value, ok := values[key]
	if !ok {
		return 0, fmt.Errorf("missing cgroup counter %s", key)
	}
	return value, nil
}
