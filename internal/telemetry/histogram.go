// Package telemetry contains bounded in-process metrics for the laboratory.
// It performs no network I/O on application hot paths.
package telemetry

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

var bounds = [...]time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second, 10 * time.Second, 30 * time.Second}

// Distribution is a value snapshot. Its owner must synchronize observation and
// copying; it contains no mutex and may be copied under the owner's lock.
type Distribution struct {
	Count   uint64
	Sum     float64
	Buckets [len(bounds)]uint64
}

func (d *Distribution) Observe(elapsed time.Duration) {
	if elapsed < 0 {
		elapsed = 0
	}
	d.Count++
	d.Sum += elapsed.Seconds()
	for i, bound := range bounds {
		if elapsed <= bound {
			d.Buckets[i]++
		}
	}
}

// Write emits cumulative classic buckets and the mandatory +Inf/count pair.
// name and labels are trusted, fixed instrumentation identifiers, not user input.
func (d Distribution) Write(w io.Writer, name, labels string) {
	_, _ = fmt.Fprintf(w, "# HELP %s Duration in seconds with fixed cumulative buckets.\n# TYPE %s histogram\n", name, name)
	d.WriteSamples(w, name, labels)
}

// WriteSamples allows multiple bounded label sets under one HELP/TYPE header.
func (d Distribution) WriteSamples(w io.Writer, name, labels string) {
	base := strings.TrimSuffix(labels, "}")
	if base == "" {
		base = "{"
	} else {
		base += ","
	}
	for i, bound := range bounds {
		_, _ = fmt.Fprintf(w, "%s_bucket%sle=%q} %d\n", name, base, strconv.FormatFloat(bound.Seconds(), 'f', -1, 64), d.Buckets[i])
	}
	_, _ = fmt.Fprintf(w, "%s_bucket%sle=\"+Inf\"} %d\n%s_sum%s %.9f\n%s_count%s %d\n", name, base, d.Count, name, labels, d.Sum, name, labels, d.Count)
}
