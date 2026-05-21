package scheduler

import (
	"errors"
	"testing"

	"llm-go-proxy/internal/ollama"
)

func TestJobKindPriority(t *testing.T) {
	tests := []struct {
		kind     JobKind
		expected int
	}{
		{KindChat, 100},
		{KindTool, 90},
		{KindVision, 90},
		{KindImageGeneration, 60},
		{KindAudio, 50},
		{KindDocument, 30},
	}

	for _, tc := range tests {
		got := tc.kind.Priority()
		if got != tc.expected {
			t.Errorf("%v.Priority() = %d, want %d", tc.kind, got, tc.expected)
		}
	}
}

func TestNewJobID_IsUnique(t *testing.T) {
	const count = 100
	ids := make(map[string]bool)
	for i := 0; i < count; i++ {
		id, err := NewJobID()
		if err != nil {
			t.Fatalf("NewJobID() returned error: %v", err)
		}
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestNewJobID_IsHexString(t *testing.T) {
	id, err := NewJobID()
	if err != nil {
		t.Fatalf("NewJobID() returned error: %v", err)
	}
	if len(id) != 32 {
		t.Errorf("expected ID length 32, got %d: %s", len(id), id)
	}
	for _, r := range id {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			t.Errorf("non-hex character in ID: %c", r)
			break
		}
	}
}

func TestJobResultFields(t *testing.T) {
	// JobResult can hold a Response
	resp := &ollama.ChatResponse{
		Model: "test-model",
		Done:  true,
	}
	r1 := JobResult{Response: resp, Err: nil}
	if r1.Response == nil {
		t.Error("expected Response to be non-nil")
	}
	if r1.Err != nil {
		t.Error("expected Err to be nil")
	}
	if r1.Response.Model != "test-model" {
		t.Errorf("unexpected model: %s", r1.Response.Model)
	}

	// JobResult can hold an error
	someErr := errors.New("test error")
	r2 := JobResult{Response: nil, Err: someErr}
	if r2.Response != nil {
		t.Error("expected Response to be nil")
	}
	if r2.Err == nil {
		t.Error("expected Err to be non-nil")
	}
	if r2.Err.Error() != "test error" {
		t.Errorf("unexpected error message: %s", r2.Err.Error())
	}
}
