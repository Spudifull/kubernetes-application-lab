package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const scanConfig = `{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck","scanner_version":"v1.1.4","go_version":"go1.26.10","scan_level":"symbol","scan_mode":"source"}}`
const scanSBOM = `{"SBOM":{"go_version":"go1.26.10","modules":[{"path":"example/app"}],"roots":["example/app"]}}`
const calledFinding = `{"finding":{"osv":"GO-2026-6505","trace":[{"module":"otel","package":"otel/sdk","function":"init"},{"module":"example/app","package":"example/app","function":"main"}]}}`

func TestGovulnStreamPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, stream            string
		bad                     bool
		calls, imports, modules int
	}{
		{"clean", scanConfig + scanSBOM, false, 0, 0, 0},
		{"module only", scanConfig + `{"finding":{"osv":"GO-MODULE","trace":[{"module":"dependency"}]}}` + scanSBOM, false, 0, 0, 1},
		{"import only", scanConfig + scanSBOM + `{"finding":{"osv":"GO-PACKAGE","trace":[{"module":"dependency","package":"pkg"}]}}`, false, 0, 1, 0},
		{"call and duplicate init", scanConfig + scanSBOM + calledFinding + calledFinding, false, 1, 0, 0},
		{"empty", "", true, 0, 0, 0},
		{"config only", scanConfig, true, 0, 0, 0},
		{"bad scan level", strings.Replace(scanConfig, "symbol", "module", 1) + scanSBOM, true, 0, 0, 0},
		{"unknown protocol", strings.Replace(scanConfig, "v1.0.0", "v2.0.0", 1) + scanSBOM, true, 0, 0, 0},
		{"truncated", scanConfig + scanSBOM + `{"finding":`, true, 0, 0, 0},
		{"trailing garbage", scanConfig + scanSBOM + "not json", true, 0, 0, 0},
		{"empty trace", scanConfig + scanSBOM + `{"finding":{"osv":"GO-BAD","trace":[]}}`, true, 0, 0, 0},
		{"missing ID", scanConfig + scanSBOM + `{"finding":{"trace":[{"module":"pkg"}]}}`, true, 0, 0, 0},
		{"duplicate config", scanConfig + scanSBOM + scanConfig, true, 0, 0, 0},
		{"unknown message", scanConfig + scanSBOM + `{"error":{"message":"scan failed"}}`, true, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGovulncheck(strings.NewReader(tc.stream))
			if (err != nil) != tc.bad {
				t.Fatalf("summary=%+v error=%v", got, err)
			}
			if !tc.bad && (len(got.Called) != tc.calls || len(got.Imported) != tc.imports || len(got.Required) != tc.modules) {
				t.Fatalf("summary=%+v", got)
			}
		})
	}
}

func TestGovulnGateFailsDespiteScannerJSONSuccess(t *testing.T) {
	file := t.TempDir() + "/scan.json"
	if err := os.WriteFile(file, []byte(scanConfig+scanSBOM+calledFinding), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCLI(t.Context(), []string{"govulncheck", "--file", file}, &fakeRunner{}, &out); err == nil {
		t.Fatal("reachable finding accepted after scanner returned JSON")
	}
	var summary govulnSummary
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil || len(summary.Called) != 1 {
		t.Fatalf("missing diagnostic summary: %s, %v", out.String(), err)
	}
	if err := os.WriteFile(file, []byte(scanConfig+scanSBOM), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (lab{out: &out}).govulncheck(file); err != nil {
		t.Fatal(err)
	}
}
