package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendChat_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if req.Model != "llama3:8b" {
			t.Errorf("expected Model 'llama3:8b', got %q", req.Model)
		}
		if len(req.Messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "user" {
			t.Errorf("expected Role 'user', got %q", req.Messages[0].Role)
		}
		if req.Messages[0].Content != "Hello!" {
			t.Errorf("expected Content 'Hello!', got %q", req.Messages[0].Content)
		}

		createdAt := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		resp := ChatResponse{
			Model:     "llama3:8b",
			CreatedAt: createdAt,
			Message: ChatMessage{
				Role:    "assistant",
				Content: "Hi there! How can I help you?",
			},
			Done:             true,
			TotalDuration:    123456789000,
			LoadDuration:     98765432100,
			PromptEvalCount:  15,
			EvalCount:        42,
			DoneReason:       "stop",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := server.Client()
	req := &ChatRequest{
		Model: "llama3:8b",
		Messages: []ChatMessage{
			{Role: "user", Content: "Hello!"},
		},
		Stream: false,
	}

	result, err := SendChat(context.Background(), client, server.URL, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.Model != "llama3:8b" {
		t.Errorf("expected Model 'llama3:8b', got %q", result.Model)
	}
	if result.Message.Role != "assistant" {
		t.Errorf("expected Message.Role 'assistant', got %q", result.Message.Role)
	}
	if result.Message.Content != "Hi there! How can I help you?" {
		t.Errorf("expected Message.Content 'Hi there! How can I help you?', got %q", result.Message.Content)
	}
	if !result.Done {
		t.Error("expected Done to be true")
	}
	if result.TotalDuration != 123456789000 {
		t.Errorf("expected TotalDuration 123456789000, got %d", result.TotalDuration)
	}
	if result.LoadDuration != 98765432100 {
		t.Errorf("expected LoadDuration 98765432100, got %d", result.LoadDuration)
	}
	if result.PromptEvalCount != 15 {
		t.Errorf("expected PromptEvalCount 15, got %d", result.PromptEvalCount)
	}
	if result.EvalCount != 42 {
		t.Errorf("expected EvalCount 42, got %d", result.EvalCount)
	}
	if result.DoneReason != "stop" {
		t.Errorf("expected DoneReason 'stop', got %q", result.DoneReason)
	}
}

func TestSendChat_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := server.Client()
	_, err := SendChat(context.Background(), client, server.URL, &ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain '500', got %q", err.Error())
	}
}

func TestSendChat_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{invalid json}`))
	}))
	defer server.Close()

	client := server.Client()
	_, err := SendChat(context.Background(), client, server.URL, &ChatRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSendChat_TrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		resp := ChatResponse{
			Model: "test-model",
			Message: ChatMessage{
				Role:    "assistant",
				Content: "OK",
			},
			Done: true,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := server.Client()
	result, err := SendChat(context.Background(), client, server.URL+"/", &ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Model != "test-model" {
		t.Errorf("expected Model 'test-model', got %q", result.Model)
	}
	if !result.Done {
		t.Error("expected Done to be true")
	}
}
