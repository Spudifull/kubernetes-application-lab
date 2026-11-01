// Package jobs owns the bounded job contract and transactional outbox state.
package jobs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

const Topic = "lab.jobs.v1"

var (
	ErrInvalid   = errors.New("invalid job request")
	ErrConflict  = errors.New("idempotency key has different content")
	ErrLeaseLost = errors.New("outbox claim expired or replaced")
	keyPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	idPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type Payload struct {
	DurationMS int `json:"duration_ms"`
}

type Request struct {
	IdempotencyKey string  `json:"idempotency_key"`
	JobType        string  `json:"job_type"`
	Payload        Payload `json:"payload"`
}

func (r Request) Validate() error {
	if !keyPattern.MatchString(r.IdempotencyKey) || r.JobType != "lab_effect" || r.Payload.DurationMS < 1 || r.Payload.DurationMS > 8000 {
		return ErrInvalid
	}
	return nil
}

// Hash uses the validated typed content, not JSON field order or whitespace.
// The key itself is not content: it selects the row whose hash is compared.
func (r Request) Hash() [32]byte {
	b, _ := json.Marshal(struct {
		Type    string  `json:"job_type"`
		Payload Payload `json:"payload"`
	}{r.JobType, r.Payload}) // Fixed concrete fields cannot fail to marshal.
	return sha256.Sum256(b)
}

type Event struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       string    `json:"event_id"`
	JobID         string    `json:"job_id"`
	JobType       string    `json:"job_type"`
	CreatedAt     time.Time `json:"created_at"`
	Payload       Payload   `json:"payload"`
}

func ValidID(id string) bool { return idPattern.MatchString(id) }

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

type Job struct {
	ID          string          `json:"job_id"`
	JobType     string          `json:"job_type"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	ErrorCode   *string         `json:"error_code,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}
