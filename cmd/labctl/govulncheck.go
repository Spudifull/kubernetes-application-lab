package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// The pinned scanner emits a JSON stream, including module/package findings
// that are not calls. Policy matches its symbol-level text exit semantics.
type govulnSummary struct {
	Scanner  string   `json:"scanner_version"`
	Go       string   `json:"go_version"`
	DB       string   `json:"db_last_modified"`
	Called   []string `json:"called"`
	Imported []string `json:"imported"`
	Required []string `json:"required"`
}

func parseGovulncheck(reader io.Reader) (govulnSummary, error) {
	summary := govulnSummary{Called: []string{}, Imported: []string{}, Required: []string{}}
	decoder := json.NewDecoder(reader)
	configSeen, sbomSeen := false, false
	called, imported, required := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for {
		var message map[string]json.RawMessage
		err := decoder.Decode(&message)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return summary, errors.New("malformed or truncated govulncheck JSON stream")
		}
		if len(message) != 1 {
			return summary, errors.New("expected one govulncheck message field")
		}
		if !configSeen && message["config"] == nil {
			return summary, errors.New("govulncheck configuration must be first")
		}
		for kind, data := range message {
			if string(data) == "null" {
				return summary, errors.New("null govulncheck message")
			}
			switch kind {
			case "config":
				var c struct {
					Protocol string `json:"protocol_version"`
					Name     string `json:"scanner_name"`
					Version  string `json:"scanner_version"`
					Go       string `json:"go_version"`
					DB       string `json:"db_last_modified"`
					Level    string `json:"scan_level"`
					Mode     string `json:"scan_mode"`
				}
				if err := json.Unmarshal(data, &c); err != nil {
					return summary, err
				}
				if configSeen || c.Protocol != "v1.0.0" || c.Name != "govulncheck" || c.Version == "" || c.Go == "" || c.Level != "symbol" || c.Mode != "source" {
					return summary, errors.New("expected one source/symbol govulncheck v1.0.0 protocol configuration")
				}
				configSeen = true
				summary.Scanner, summary.Go, summary.DB = c.Version, c.Go, c.DB
			case "SBOM":
				var sbom struct {
					Go      string `json:"go_version"`
					Modules []struct {
						Path string `json:"path"`
					} `json:"modules"`
					Roots []string `json:"roots"`
				}
				if err := json.Unmarshal(data, &sbom); err != nil {
					return summary, err
				}
				if sbomSeen || sbom.Go != summary.Go || len(sbom.Modules) == 0 || len(sbom.Roots) == 0 {
					return summary, errors.New("missing or inconsistent source scan SBOM")
				}
				sbomSeen = true
			case "finding":
				var finding struct {
					OSV   string `json:"osv"`
					Trace []struct {
						Module   string `json:"module"`
						Package  string `json:"package"`
						Function string `json:"function"`
					} `json:"trace"`
				}
				if err := json.Unmarshal(data, &finding); err != nil {
					return summary, err
				}
				if finding.OSV == "" || len(finding.Trace) == 0 || finding.Trace[0].Module == "" {
					return summary, errors.New("incomplete govulncheck finding")
				}
				first := finding.Trace[0]
				if first.Function != "" {
					if first.Package == "" {
						return summary, errors.New("symbol finding has no package")
					}
					called[finding.OSV] = true
				} else {
					if len(finding.Trace) != 1 {
						return summary, errors.New("non-symbol finding has an ambiguous call trace")
					}
					if first.Package != "" {
						imported[finding.OSV] = true
					} else {
						required[finding.OSV] = true
					}
				}
			case "progress", "osv":
				var object map[string]json.RawMessage
				if err := json.Unmarshal(data, &object); err != nil {
					return summary, err
				}
			default:
				return summary, fmt.Errorf("unsupported govulncheck message %q", kind)
			}
		}
	}
	if !configSeen || !sbomSeen {
		return summary, errors.New("incomplete govulncheck report: config and SBOM required")
	}
	for id := range called {
		summary.Called = append(summary.Called, id)
	}
	for id := range imported {
		if !called[id] {
			summary.Imported = append(summary.Imported, id)
		}
	}
	for id := range required {
		if !called[id] && !imported[id] {
			summary.Required = append(summary.Required, id)
		}
	}
	sort.Strings(summary.Called)
	sort.Strings(summary.Imported)
	sort.Strings(summary.Required)
	return summary, nil
}

func (l lab) govulncheck(file string) error {
	if file == "" {
		return errors.New("govulncheck requires --file")
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 64<<20 {
		return errors.New("govulncheck report must be a regular file up to 64 MiB")
	}
	summary, err := parseGovulncheck(io.LimitReader(f, (64<<20)+1))
	if err != nil {
		return err
	}
	if err := json.NewEncoder(l.out).Encode(summary); err != nil {
		return err
	}
	if len(summary.Called) > 0 {
		return fmt.Errorf("called vulnerable symbols found: %s", strings.Join(summary.Called, ", "))
	}
	return nil
}
