package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAlertProofRequiresRealOrderedCycleAndStableReceiver(t *testing.T) {
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	base := func() alertSnapshot {
		var s alertSnapshot
		pending := 0.0
		s.RecoveryValue = &pending
		s.Rule.State = "inactive"
		s.Receiver.StartedAt = start.Add(-time.Hour)
		s.ActiveFingerprints = map[string]bool{}
		return s
	}
	newProof := func() alertProof { return alertProof{Since: start, ReceiverStarted: base().Receiver.StartedAt} }
	row := alertNotification{ReceivedAt: start.Add(time.Minute), Status: "firing", Labels: map[string]string{"alertname": "OutboxBacklogGrowing", "namespace": mainNS}, StartsAt: start, Fingerprint: "0123456789abcdef"}
	p := newProof()
	s := base()
	s.Rule.State = "pending"
	pending := 1.0
	s.RecoveryValue = &pending
	if ok, err := p.observe(s); err != nil || ok {
		t.Fatal("pending alone completed a cycle", err)
	}
	s.Rule.State = "firing"
	s.ActiveFingerprints[row.Fingerprint] = true
	s.Receiver.Notifications = []alertNotification{row}
	if ok, err := p.observe(s); err != nil || ok || !p.Manager {
		t.Fatal("firing/receipt not observed", err)
	}
	s.Rule.State = "inactive"
	zero := 0.0
	s.RecoveryValue = &zero
	resolved := row
	resolved.Status, resolved.EndsAt = "resolved", start.Add(2*time.Minute)
	resolved.ReceivedAt = start.Add(3 * time.Minute)
	s.Receiver.Notifications = append(s.Receiver.Notifications, resolved)
	if ok, err := p.observe(s); err != nil || !ok {
		t.Fatal("complete cycle rejected", err)
	}
	for _, change := range []func(*alertSnapshot){
		func(s *alertSnapshot) { s.Receiver.StartedAt = start },
		func(s *alertSnapshot) { s.Receiver.Evicted++ },
	} {
		bad := base()
		change(&bad)
		p := newProof()
		if _, err := p.observe(bad); err == nil {
			t.Fatal("loss of evidence ignored")
		}
	}
	for _, change := range []func(*alertSnapshot){
		func(s *alertSnapshot) { s.RecoveryValue = nil },
		func(s *alertSnapshot) { s.Receiver.Notifications[1].Fingerprint = "fedcba9876543210" },
		func(s *alertSnapshot) { s.Receiver.Notifications[1].StartsAt = start.Add(time.Second) },
		func(s *alertSnapshot) { s.Receiver.Notifications[1].ReceivedAt = start.Add(-time.Second) },
	} {
		s := base()
		s.Receiver.Notifications = []alertNotification{row, resolved}
		change(&s)
		p := newProof()
		p.Pending, p.Firing, p.Manager = true, true, true
		p.Fingerprint, p.StartsAt = row.Fingerprint, start
		if ok, _ := p.observe(s); ok {
			t.Fatal("unrelated/old resolved accepted")
		}
	}
	p = newProof()
	s = base()
	s.Receiver.Notifications = []alertNotification{row, resolved}
	if ok, _ := p.observe(s); ok {
		t.Fatal("notifications without observed Pending/Firing accepted")
	}
}

func TestAlertingGuardsAndImageIdentity(t *testing.T) {
	for _, action := range []string{"alerts-render", "alerts-install", "alerts-status", "alerts-watch", "api-alert-stop", "api-alert-start", "api-alert-check"} {
		f := &fakeRunner{}
		l := lab{context: netContext, namespace: mainNS, runner: f}
		if l.observabilityAction(context.Background(), action, "") == nil || len(f.calls) != 0 {
			t.Fatalf("unguarded %s", action)
		}
	}
	for _, ref := range []string{"other:" + strings.Repeat("a", 64), receiverRepository + ":build", receiverRepository + ":" + strings.Repeat("g", 64)} {
		if _, err := receiverImageTag(ref); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	if _, err := receiverImageTag(receiverRepository + ":" + strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if alertingOwnership([]byte(`[{"Name":"lab-alerting","Namespace":"other","Chart":"lab-alerting-0.1.0","Status":"deployed"}]`)) == nil {
		t.Fatal("foreign release accepted")
	}
}

func TestNamedWorkerAlertCycles(t *testing.T) {
	for _, name := range []string{"KafkaConsumerLagHigh", "WorkerProcessingStalled", "APIUnavailable", "PostgreSQLUnavailable"} {
		t.Run(name, func(t *testing.T) {
			start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			p := alertProof{AlertName: name, Since: start, ReceiverStarted: start.Add(-time.Hour)}
			var s alertSnapshot
			s.Receiver.StartedAt = p.ReceiverStarted
			lag := 1.0
			s.RecoveryValue = &lag
			s.Rule.State = "pending"
			if done, err := p.observe(s); done || err != nil {
				t.Fatalf("pending: done=%v err=%v", done, err)
			}
			s.Rule.State = "firing"
			row := alertNotification{ReceivedAt: start.Add(time.Minute), Status: "firing", Labels: map[string]string{"alertname": "OutboxBacklogGrowing", "namespace": mainNS}, StartsAt: start, Fingerprint: "0123456789abcdef"}
			s.ActiveFingerprints = map[string]bool{row.Fingerprint: true}
			s.Receiver.Notifications = []alertNotification{row}
			if done, err := p.observe(s); done || err != nil || p.Manager {
				t.Fatal("another alert fulfilled the named proof", err)
			}
			row.Labels = map[string]string{"alertname": name, "namespace": mainNS}
			s.Receiver.Notifications = []alertNotification{row}
			if done, err := p.observe(s); done || err != nil || !p.Manager {
				t.Fatal("matching firing receipt rejected", err)
			}
			row.Status, row.EndsAt, row.ReceivedAt = "resolved", start.Add(2*time.Minute), start.Add(3*time.Minute)
			s.Receiver.Notifications = []alertNotification{row}
			s.Rule.State = "inactive"
			s.RecoveryValue = nil
			if done, err := p.observe(s); done || err != nil {
				t.Fatal("unknown lag proved recovery", err)
			}
			s.RecoveryValue = &lag
			if done, err := p.observe(s); done || err != nil {
				t.Fatal("positive lag proved recovery", err)
			}
			zero := 0.0
			s.RecoveryValue = &zero
			if done, err := p.observe(s); !done || err != nil {
				t.Fatal("known zero and matching resolved rejected", err)
			}
		})
	}
}

func TestWatchedAlertSelectionAndRecoveryQuery(t *testing.T) {
	if name, err := watchedAlertName(""); err != nil || name != "OutboxBacklogGrowing" {
		t.Fatal(name, err)
	}
	for _, name := range []string{"KafkaConsumerLagHigh", "WorkerProcessingStalled"} {
		query, err := alertRecoveryQuery(name)
		if err != nil || !strings.Contains(query, ">= 0) == 3") || !strings.Contains(query, `job="lab-kafka-exporter"`) || !strings.Contains(query, "== 1") || strings.Contains(query, "> 0") {
			t.Fatalf("recovery must preserve a known zero and reject incomplete/negative lag: %s %v", query, err)
		}
	}
	for _, name := range []string{"other"} {
		if _, err := alertRecoveryQuery(name); err == nil {
			t.Fatalf("unsupported recovery proof accepted: %s", name)
		}
	}
}

func TestDLQProofRequiresContinuousWorkerCounter(t *testing.T) {
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	p := alertProof{AlertName: "DLQMessagesDetected", SignalIdentity: "10.0.0.1:8080", Since: start, ReceiverStarted: start.Add(-time.Hour)}
	var s alertSnapshot
	s.SignalIdentity = p.SignalIdentity
	s.Receiver.StartedAt = p.ReceiverStarted
	one := 1.0
	s.RecoveryValue = &one
	s.Rule.State = "pending"
	if done, err := p.observe(s); done || err != nil {
		t.Fatal(done, err)
	}
	s.Rule.State = "firing"
	row := alertNotification{ReceivedAt: start.Add(time.Minute), Status: "firing", Labels: map[string]string{"alertname": p.AlertName, "namespace": mainNS}, StartsAt: start, Fingerprint: "0123456789abcdef"}
	s.ActiveFingerprints = map[string]bool{row.Fingerprint: true}
	s.Receiver.Notifications = []alertNotification{row}
	if done, err := p.observe(s); done || err != nil || !p.Manager {
		t.Fatal(done, err)
	}
	for _, identity := range []string{"", "10.0.0.2:8080"} {
		bad := s
		bad.SignalIdentity = identity
		if _, err := p.observe(bad); err == nil {
			t.Fatal("replacement worker accepted")
		}
	}
	bad := s
	bad.RecoveryValue = nil
	if _, err := p.observe(bad); err == nil {
		t.Fatal("lost/reset counter accepted")
	}
	row.Status = "resolved"
	row.EndsAt = start.Add(6 * time.Minute)
	row.ReceivedAt = start.Add(7 * time.Minute)
	s.Receiver.Notifications = []alertNotification{row}
	s.Rule.State = "inactive"
	if done, err := p.observe(s); done || err != nil {
		t.Fatal("positive increment proved window expiry", err)
	}
	zero := 0.0
	s.RecoveryValue = &zero
	if done, err := p.observe(s); !done || err != nil {
		t.Fatal("matching resolved and same known zero rejected", err)
	}
	query, err := alertRecoveryQuery(p.AlertName)
	if err != nil || !strings.Contains(query, "[5m]") || !strings.Contains(query, "resets(") || !strings.Contains(query, "up{") || strings.Contains(query, "sum(") {
		t.Fatal(query, err)
	}
}
