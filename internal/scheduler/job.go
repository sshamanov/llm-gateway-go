package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/ollama"
)

// JobKind classifies a job for priority and routing purposes.
type JobKind int

const (
	KindChat            JobKind = 100
	KindTool            JobKind = 90
	KindVision          JobKind = 90
	KindImageGeneration JobKind = 60
	KindAudio           JobKind = 50
	KindDocument        JobKind = 30
)

// Priority returns the numeric priority associated with the job kind.
func (k JobKind) Priority() int {
	return int(k)
}

// String returns a human-readable name for the job kind.
func (k JobKind) String() string {
	switch k {
	case KindChat:
		return "chat"
	case KindTool:
		return "tool_vision"
	case KindImageGeneration:
		return "image_generation"
	case KindAudio:
		return "audio"
	case KindDocument:
		return "document"
	default:
		return "unknown"
	}
}

// JobState represents the lifecycle state of a job.
type JobState int

const (
	StatePending JobState = iota
	StateRunning
	StateCompleted
	StateFailed
)

// String returns a human-readable name for the job state.
func (s JobState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateRunning:
		return "running"
	case StateCompleted:
		return "completed"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Job represents a single request to be processed by a backend.
type Job struct {
	ID             string
	Kind           JobKind
	Priority       int
	State          JobState
	CreatedAt      time.Time
	RequestedModel string
	Candidates     []string
	AliasConfig    *config.AliasConfig
	Messages       []ollama.ChatMessage
	KeepAlive      string
	Think          *bool
	Options        *ollama.ChatOptions
	Tools          json.RawMessage

	// Streaming job fields.
	Streaming  bool                     // true if this is a streaming job
	StreamCh   chan *ollama.StreamChunk // delivers streaming chunks to handler (must be created by submitter)
	JobCtx     context.Context          // derived from HTTP request context; cancellation aborts backend request

	ResultChan     chan JobResult
	BackendID      string
	ConcreteModel  string
	Attempts       int
}

// JobResult carries the outcome of a completed job.
type JobResult struct {
	Response *ollama.ChatResponse
	Err      error
}

// Duration returns the time elapsed since the job was created.
func (j *Job) Duration() time.Duration {
	return time.Since(j.CreatedAt)
}

// NewJobID generates a random 16-byte hex string for use as a job ID.
func NewJobID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
