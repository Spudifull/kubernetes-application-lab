package jobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrEvent = errors.New("invalid or inconsistent job event")

// DecodeEvent validates the bounded v1 transport contract. Unknown fields and
// trailing JSON are rejected; as in the HTTP API, repeated fields use Go's last value.
func DecodeEvent(key, value []byte) (Event, error) {
	var e Event
	if len(value) == 0 || len(value) > 4096 || len(key) != 36 {
		return e, ErrEvent
	}
	d := json.NewDecoder(bytes.NewReader(value))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil {
		return Event{}, ErrEvent
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return Event{}, ErrEvent
	}
	_, offset := e.CreatedAt.Zone()
	if e.SchemaVersion != 1 || !ValidID(e.JobID) || !ValidID(e.EventID) || string(key) != e.JobID || e.CreatedAt.IsZero() || offset != 0 || e.JobType != "lab_effect" || e.Payload.DurationMS < 1 || e.Payload.DurationMS > 8000 {
		return Event{}, ErrEvent
	}
	return e, nil
}

type Effect struct {
	Outcome    string `json:"outcome"`
	DurationMS int    `json:"duration_ms"`
}
