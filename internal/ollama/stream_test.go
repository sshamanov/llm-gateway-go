package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendChatStream_Basic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if !req.Stream {
			t.Error("expected Stream to be true")
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":"Hello"},"done":false}`+"\n")
		fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":" world"},"done":false}`+"\n")
		fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":""},"done":true,"total_duration":500000000,"eval_count":5}`+"\n")
	}))
	defer server.Close()

	client := server.Client()
	ctx := context.Background()
	req := &ChatRequest{
		Model:    "llama3",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}

	ch, err := SendChatStream(ctx, client, server.URL, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}

	var chunks []StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	// First chunk: partial content
	if chunks[0].Err != nil {
		t.Fatalf("unexpected error in chunk 0: %v", chunks[0].Err)
	}
	if chunks[0].Response == nil {
		t.Fatal("expected non-nil Response in chunk 0")
	}
	if chunks[0].Response.Message.Content != "Hello" {
		t.Errorf("expected content 'Hello', got %q", chunks[0].Response.Message.Content)
	}
	if chunks[0].Response.Done {
		t.Error("expected done=false in chunk 0")
	}

	// Second chunk: more content
	if chunks[1].Err != nil {
		t.Fatalf("unexpected error in chunk 1: %v", chunks[1].Err)
	}
	if chunks[1].Response.Message.Content != " world" {
		t.Errorf("expected content ' world', got %q", chunks[1].Response.Message.Content)
	}
	if chunks[1].Response.Done {
		t.Error("expected done=false in chunk 1")
	}

	// Third chunk: done
	if chunks[2].Err != nil {
		t.Fatalf("unexpected error in chunk 2: %v", chunks[2].Err)
	}
	if !chunks[2].Response.Done {
		t.Error("expected done=true in chunk 2")
	}
	if chunks[2].Response.TotalDuration != 500000000 {
		t.Errorf("expected TotalDuration 500000000, got %d", chunks[2].Response.TotalDuration)
	}
	if chunks[2].Response.EvalCount != 5 {
		t.Errorf("expected EvalCount 5, got %d", chunks[2].Response.EvalCount)
	}
}

func TestSendChatStream_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)

		// Send first chunk
		fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":"Hello"},"done":false}`+"\n")

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Wait for context cancellation
		<-r.Context().Done()
	}))
	defer server.Close()

	client := server.Client()
	req := &ChatRequest{Model: "llama3"}

	ch, err := SendChatStream(ctx, client, server.URL, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read first chunk
	chunk, ok := <-ch
	if !ok {
		t.Fatal("expected at least one chunk")
	}
	if chunk.Err != nil {
		t.Fatalf("unexpected error: %v", chunk.Err)
	}
	if chunk.Response == nil {
		t.Fatal("expected non-nil Response")
	}
	if chunk.Response.Message.Content != "Hello" {
		t.Errorf("expected content 'Hello', got %q", chunk.Response.Message.Content)
	}

	// Cancel context
	cancel()

	// Drain the channel. Should close without panic.
	for range ch {
	}
}

func TestSendChatStream_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":"internal error"}`)
	}))
	defer server.Close()

	client := server.Client()
	ctx := context.Background()
	req := &ChatRequest{Model: "test"}

	ch, err := SendChatStream(ctx, client, server.URL, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if ch != nil {
		t.Error("expected nil channel on HTTP error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain '500', got %q", err.Error())
	}
}

func TestSendChatStream_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintf(w, `not valid json`+"\n")
	}))
	defer server.Close()

	client := server.Client()
	ctx := context.Background()
	req := &ChatRequest{Model: "test"}

	ch, err := SendChatStream(ctx, client, server.URL, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunk, ok := <-ch
	if !ok {
		t.Fatal("expected a chunk with error")
	}
	if chunk.Response != nil {
		t.Error("expected nil Response on parse error")
	}
	if chunk.Err == nil {
		t.Fatal("expected error on invalid JSON")
	}
	if !strings.Contains(chunk.Err.Error(), "decode line") {
		t.Errorf("expected error to contain 'decode line', got %q", chunk.Err.Error())
	}

	// Channel should close after the error chunk
	_, ok = <-ch
	if ok {
		t.Error("expected channel to be closed after error")
	}
}

func TestSendChatStream_EmptyLines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintf(w, "\n")
		fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":"Hello"},"done":false}`+"\n")
		fmt.Fprintf(w, "\n")
		fmt.Fprintf(w, "\n")
		fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":""},"done":true}`+"\n")
		fmt.Fprintf(w, "\n")
	}))
	defer server.Close()

	client := server.Client()
	ctx := context.Background()
	req := &ChatRequest{Model: "test"}

	ch, err := SendChatStream(ctx, client, server.URL, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks []StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	if chunks[0].Err != nil {
		t.Fatalf("unexpected error in chunk 0: %v", chunks[0].Err)
	}
	if chunks[0].Response.Message.Content != "Hello" {
		t.Errorf("expected content 'Hello', got %q", chunks[0].Response.Message.Content)
	}

	if chunks[1].Err != nil {
		t.Fatalf("unexpected error in chunk 1: %v", chunks[1].Err)
	}
	if !chunks[1].Response.Done {
		t.Error("expected done=true")
	}
}

func TestSendChatStream_StreamForcedTrue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if !req.Stream {
			t.Error("expected Stream to be true after forcing")
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintf(w, `{"model":"llama3","message":{"role":"assistant","content":"OK"},"done":true}`+"\n")
	}))
	defer server.Close()

	client := server.Client()
	ctx := context.Background()
	req := &ChatRequest{Model: "test", Stream: false}

	ch, err := SendChatStream(ctx, client, server.URL, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunk, ok := <-ch
	if !ok {
		t.Fatal("expected a chunk")
	}
	if chunk.Err != nil {
		t.Fatalf("unexpected error: %v", chunk.Err)
	}
	if !chunk.Response.Done {
		t.Error("expected done=true")
	}

	// Drain remaining
	for range ch {
	}
}
