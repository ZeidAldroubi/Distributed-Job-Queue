package shared

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	StatusQueued     = "queued"
	StatusProcessing = "processing"
	StatusDone       = "done"
	StatusFailed     = "failed"
	StatusDead       = "dead"

	TypeResizeImage = "resize_image"

	PendingQueue    = "queue:pending"
	ProcessingQueue = "queue:processing"
	DeadLetterQueue = "queue:dead_letter"
	EventsChannel   = "events"
	CompletedZSet   = "metrics:completed"
	WorkersRegistry = "workers:registry"
)

type Job struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	CreatedAt   time.Time       `json:"created_at"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	WorkerID    string          `json:"worker_id,omitempty"`
}

type ResizePayload struct {
	ImageURL string `json:"image_url,omitempty"`
	ImageB64 string `json:"image_base64,omitempty"`
}

type Event struct {
	Type     string    `json:"type"`
	JobID    string    `json:"job_id,omitempty"`
	WorkerID string    `json:"worker_id,omitempty"`
	Status   string    `json:"status,omitempty"`
	Message  string    `json:"message,omitempty"`
	Time     time.Time `json:"time"`
}

func JobKey(id string) string {
	return fmt.Sprintf("job:%s", id)
}

func WorkerKey(id string) string {
	return fmt.Sprintf("worker:%s", id)
}
