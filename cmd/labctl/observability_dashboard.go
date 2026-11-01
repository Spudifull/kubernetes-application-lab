package main

import (
	"encoding/json"
	"errors"
	"os"
)

// Dashboard JSON has one canonical source in Git, not a second copy in values.
// Native Grafana chart provisioning survives replacement of its temporary DB.
func (e monitoringEvidence) dashboardValues() error {
	dashboards := map[string]map[string]string{}
	for _, name := range []string{"application", "dependencies"} {
		b, err := os.ReadFile("cluster/observability/dashboards/" + name + ".json")
		if err != nil {
			return err
		}
		if !json.Valid(b) {
			return errors.New(name + " dashboard is not valid JSON")
		}
		dashboards[name] = map[string]string{"json": string(b)}
	}
	values := map[string]any{"grafana": map[string]any{"dashboards": map[string]any{"lab": dashboards}}}
	b, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return e.save("dashboard-values.json", b)
}
