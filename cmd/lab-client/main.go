// lab-client runs bounded in-cluster HTTP and resource experiments.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type summary struct {
	RunID                string    `json:"run_id"`
	StartTime            time.Time `json:"start_time"`
	EndTime              time.Time `json:"end_time"`
	TotalRequests        int       `json:"total_requests"`
	SuccessfulRequests   int       `json:"successful_requests"`
	HTTP5xx              int       `json:"http_5xx"`
	OtherNon2xx          int       `json:"other_non_2xx"`
	ConnectionErrors     int       `json:"connection_errors"`
	Timeouts             int       `json:"timeouts"`
	DurationMS           int64     `json:"duration_ms"`
	ResponseLatencyUSSum int64     `json:"response_latency_us_sum"`
	ResponseLatencyUSMax int64     `json:"response_latency_us_max"`
}

func main() {
	mode := flag.String("mode", "load", "load, cpu-load, work, probe or memory")
	target := flag.String("url", "http://lab-api.kubernetes-application-lab.svc/", "HTTP target")
	duration := flag.Duration("duration", 180*time.Second, "bounded load duration")
	interval := flag.Duration("interval", 100*time.Millisecond, "time between load requests")
	requestTimeout := flag.Duration("request-timeout", 2*time.Second, "per-request timeout")
	explicitRunID := flag.String("run-id", "", "optional experiment correlation ID")
	memoryMiB := flag.Int("memory-mib", 96, "memory MiB to touch in memory mode (1..128)")
	memoryHold := flag.Duration("memory-hold", 15*time.Second, "maximum hold time in memory mode (1..60s)")
	flag.Parse()

	if *mode != "load" && *mode != "cpu-load" && *mode != "work" && *mode != "probe" && *mode != "memory" {
		fatal("mode must be load, cpu-load, work, probe or memory")
	}
	if *duration < time.Second || *duration > 5*time.Minute || *interval < 50*time.Millisecond || *interval > time.Second || *requestTimeout < time.Second || *requestTimeout > 15*time.Second {
		fatal("duration, interval or request timeout is outside the laboratory bound")
	}

	hostname, err := os.Hostname()
	if err != nil {
		fatal("cannot obtain hostname: " + err.Error())
	}
	runID := fmt.Sprintf("%s-%d", hostname, time.Now().UTC().UnixNano())
	if *explicitRunID != "" {
		runID = *explicitRunID
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *mode == "memory" {
		if err := validateMemoryParams(*memoryMiB, *memoryHold); err != nil {
			fatal(err.Error())
		}
		writeEvent("memory_allocation_started", map[string]any{"run_id": runID, "time": time.Now().UTC(), "memory_mib": *memoryMiB, "hold": memoryHold.String()})
		if err := runMemory(ctx, runID, *memoryMiB, *memoryHold, waitForMemory); err != nil {
			fatal("memory experiment: " + err.Error())
		}
		writeEvent("memory_experiment_completed", map[string]any{"run_id": runID, "time": time.Now().UTC()})
		return
	}
	u, err := url.Parse(*target)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil {
		fatal("target must be an HTTP URL with a host and no credentials")
	}
	client := &http.Client{
		Timeout: *requestTimeout,
		Transport: &http.Transport{
			DisableKeepAlives: true, // New TCP connections exercise Service endpoint selection.
			DialContext:       (&net.Dialer{Timeout: time.Second}).DialContext,
		},
	}
	defer client.CloseIdleConnections()

	if *mode == "work" {
		if err := runWork(ctx, client, runID, u.String()); err != nil {
			fatal(err.Error())
		}
		return
	}
	if *mode == "probe" {
		if err := runShutdownProbe(ctx, client, runID, u, *duration); err != nil {
			fatal(err.Error())
		}
		return
	}
	if err := runLoad(ctx, client, runID, u.String(), *duration, *interval, *mode == "cpu-load"); err != nil {
		fatal(err.Error())
	}
}

func runShutdownProbe(parent context.Context, client *http.Client, runID string, target *url.URL, duration time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	readyURL := target.ResolveReference(&url.URL{Path: "/readyz"}).String()
	workURL := target.ResolveReference(&url.URL{Path: "/work", RawQuery: "duration_ms=1"}).String()
	sawReady := false
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	writeEvent("probe_started", map[string]any{"run_id": runID, "time": time.Now().UTC(), "target": target.Host})
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("shutdown probe did not observe readiness 503: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("shutdown probe did not observe readiness 503: %w", ctx.Err())
		case <-ticker.C:
			status, pod, err := requestStatus(ctx, client, readyURL)
			if err != nil {
				writeEvent("probe_error", map[string]any{"run_id": runID, "time": time.Now().UTC(), "error": err.Error()})
				continue
			}
			if status == http.StatusOK && !sawReady {
				sawReady = true
				writeEvent("probe_ready_seen", map[string]any{"run_id": runID, "time": time.Now().UTC(), "pod": pod})
			}
			if status != http.StatusServiceUnavailable {
				continue
			}
			workStatus, workPod, workErr := requestStatus(ctx, client, workURL)
			writeEvent("probe_result", map[string]any{"run_id": runID, "time": time.Now().UTC(), "ready_status": status, "ready_pod": pod, "new_work_status": workStatus, "new_work_pod": workPod, "work_error": fmt.Sprint(workErr)})
			if !sawReady || workErr != nil || workStatus != http.StatusServiceUnavailable {
				return fmt.Errorf("shutdown admission mismatch: ready before=%t, new work status=%d, error=%v", sawReady, workStatus, workErr)
			}
			return nil
		}
	}
}

func requestStatus(ctx context.Context, client *http.Client, target string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	pod := response.Header.Get("X-Lab-Pod")
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return response.StatusCode, pod, fmt.Errorf("read=%v close=%v", readErr, closeErr)
	}
	return response.StatusCode, pod, nil
}

func runLoad(parent context.Context, client *http.Client, runID, target string, duration, interval time.Duration, recordResponses bool) error {
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	result := summary{RunID: runID, StartTime: time.Now().UTC()}
	writeEvent("load_started", map[string]any{"run_id": runID, "start_time": result.StartTime, "url": target})
	requests := time.NewTicker(interval)
	defer requests.Stop()
	progress := time.NewTicker(5 * time.Second)
	defer progress.Stop()
	for {
		if ctx.Err() != nil {
			result.EndTime = time.Now().UTC()
			result.DurationMS = result.EndTime.Sub(result.StartTime).Milliseconds()
			writeEvent("load_summary", result)
			if parent.Err() != nil {
				return fmt.Errorf("load interrupted: %w", parent.Err())
			}
			if result.HTTP5xx+result.OtherNon2xx+result.ConnectionErrors+result.Timeouts > 0 {
				return fmt.Errorf("load observed %d failed requests", result.TotalRequests-result.SuccessfulRequests)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			continue
		case <-requests.C:
			result.TotalRequests++
			started := time.Now()
			request, err := http.NewRequestWithContext(parent, http.MethodGet, target, nil)
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}
			request.Header.Set("X-Lab-Run-ID", runID)
			response, err := client.Do(request)
			if err != nil {
				if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
					result.Timeouts++
					writeEvent("request_error", map[string]any{"run_id": runID, "time": time.Now().UTC(), "kind": "timeout", "error": err.Error()})
				} else if ctx.Err() == nil {
					result.ConnectionErrors++
					writeEvent("request_error", map[string]any{"run_id": runID, "time": time.Now().UTC(), "kind": "connection", "error": err.Error()})
				} else {
					result.ConnectionErrors++
					writeEvent("request_error", map[string]any{"run_id": runID, "time": time.Now().UTC(), "kind": "interrupted", "error": err.Error()})
				}
				continue
			}
			_, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			closeErr := response.Body.Close()
			if copyErr != nil || closeErr != nil {
				result.ConnectionErrors++
				writeEvent("request_error", map[string]any{"run_id": runID, "time": time.Now().UTC(), "kind": "body", "status": response.StatusCode, "pod": response.Header.Get("X-Lab-Pod"), "copy_error": fmt.Sprint(copyErr), "close_error": fmt.Sprint(closeErr)})
				continue
			}
			elapsed := time.Since(started)
			if elapsed.Microseconds() > result.ResponseLatencyUSMax {
				result.ResponseLatencyUSMax = elapsed.Microseconds()
			}
			result.ResponseLatencyUSSum += elapsed.Microseconds()
			if recordResponses {
				writeEvent("request_result", map[string]any{"run_id": runID, "time": time.Now().UTC(), "status": response.StatusCode, "pod": response.Header.Get("X-Lab-Pod"), "latency_us": elapsed.Microseconds()})
			}
			switch {
			case response.StatusCode >= 200 && response.StatusCode < 300:
				result.SuccessfulRequests++
			case response.StatusCode >= 500:
				result.HTTP5xx++
			default:
				result.OtherNon2xx++
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				writeEvent("request_error", httpFailureEvent(runID, response, elapsed))
			}
		case <-progress.C:
			writeEvent("load_progress", map[string]any{"run_id": runID, "time": time.Now().UTC(), "total_requests": result.TotalRequests, "successful_requests": result.SuccessfulRequests})
		}
	}
}

func httpFailureEvent(runID string, response *http.Response, latency time.Duration) map[string]any {
	return map[string]any{
		"run_id":     runID,
		"time":       time.Now().UTC(),
		"kind":       "http",
		"status":     response.StatusCode,
		"pod":        response.Header.Get("X-Lab-Pod"),
		"latency_ms": latency.Milliseconds(),
	}
}

func runWork(ctx context.Context, client *http.Client, runID, target string) error {
	started := time.Now().UTC()
	writeEvent("work_request_sent", map[string]any{"run_id": runID, "time": started, "url": target})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("create work request: %w", err)
	}
	request.Header.Set("X-Lab-Run-ID", runID)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("work request: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read work response: read=%v close=%v", readErr, closeErr)
	}
	writeEvent("work_response", map[string]any{"run_id": runID, "start_time": started, "end_time": time.Now().UTC(), "status": response.StatusCode, "pod": response.Header.Get("X-Lab-Pod"), "body": string(body)})
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("work returned HTTP %d", response.StatusCode)
	}
	return nil
}

func writeEvent(event string, value any) {
	data, err := json.Marshal(struct {
		Event string `json:"event"`
		Value any    `json:"value"`
	}{Event: event, Value: value})
	if err != nil {
		fatal("encode event: " + err.Error())
	}
	fmt.Println(string(data))
}

func fatal(message string) {
	_, err := fmt.Fprintln(os.Stderr, message)
	if err != nil {
		return
	}
	os.Exit(1)
}
