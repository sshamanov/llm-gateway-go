package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/ollama"
	"llm-go-proxy/internal/scheduler"
)

// ---------------------------------------------------------------------------
// Handler integration tests
// ---------------------------------------------------------------------------

// newTestScheduler creates a Scheduler from config and registry for use in
// openai handler tests. The scheduler is NOT started — the caller must call
// Start() and Stop().
func newTestScheduler(t *testing.T, cfg *config.Config, reg *backend.Registry, backendURLs map[string]string) *scheduler.Scheduler {
	t.Helper()
	hosts := scheduler.NewHostLeaseManager(cfg.Hosts)
	backends := scheduler.NewBackendLeaseManager(cfg.OllamaBackends)
	stats := scheduler.NewStatsTracker()
	scorer := scheduler.NewScorer(stats, hosts, backends, cfg.Scheduler)
	return scheduler.NewScheduler(
		scheduler.NewQueue(cfg.Scheduler.AgingPerSecond),
		scorer,
		stats,
		http.DefaultClient,
		backendURLs,
		nil,
		nil,
		reg.BackendSnapshots,
	)
}

func TestChatCompletions_Basic(t *testing.T) {
	// Fake Ollama server that responds to /api/tags, /api/ps, and /api/chat.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Hello! I am an AI assistant.",
				},
				Done:            true,
				DoneReason:      "stop",
				PromptEvalCount: 10,
				EvalCount:       20,
				TotalDuration:   1000000000,
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{
					{Name: "qwen:30b"},
					{Name: "qwen:14b"},
				},
			})
			return
		}
		// /api/ps
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
	defer ts.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "my-alias",
			PrimaryModel: "qwen:30b",
			BackupModels: []string{"qwen:14b"},
		}},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: ts.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	// Wait for initial poll to complete.
	time.Sleep(200 * time.Millisecond)

	sched := newTestScheduler(t, &cfg, reg, map[string]string{"test-backend": ts.URL})
	sched.Start()
	defer sched.Stop()

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"my-alias","messages":[{"role":"user","content":"Hello"}]}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Verify ID format: "chatcmpl-" (9) + 16 hex chars = 25 chars total.
	if !strings.HasPrefix(result.ID, "chatcmpl-") {
		t.Errorf("expected ID to start with 'chatcmpl-', got %q", result.ID)
	}
	if len(result.ID) != 25 {
		t.Errorf("expected ID length 25, got %d: %q", len(result.ID), result.ID)
	}

	if result.Object != "chat.completion" {
		t.Errorf("expected Object 'chat.completion', got %q", result.Object)
	}
	if result.Created <= 0 {
		t.Errorf("expected positive Created timestamp, got %d", result.Created)
	}
	// Model should be the requested model ID, not the resolved one.
	if result.Model != "my-alias" {
		t.Errorf("expected Model 'my-alias', got %q", result.Model)
	}
	if len(result.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(result.Choices))
	}
	if result.Choices[0].Index != 0 {
		t.Errorf("expected Index 0, got %d", result.Choices[0].Index)
	}
	if result.Choices[0].Message.Role != "assistant" {
		t.Errorf("expected Message.Role 'assistant', got %q", result.Choices[0].Message.Role)
	}
	if result.Choices[0].Message.Content != "Hello! I am an AI assistant." {
		t.Errorf("expected Message.Content 'Hello! I am an AI assistant.', got %q", result.Choices[0].Message.Content)
	}
	if result.Choices[0].FinishReason != "stop" {
		t.Errorf("expected FinishReason 'stop', got %q", result.Choices[0].FinishReason)
	}
	if result.Usage.PromptTokens != 10 {
		t.Errorf("expected Usage.PromptTokens 10, got %d", result.Usage.PromptTokens)
	}
	if result.Usage.CompletionTokens != 20 {
		t.Errorf("expected Usage.CompletionTokens 20, got %d", result.Usage.CompletionTokens)
	}
	if result.Usage.TotalTokens != 30 {
		t.Errorf("expected Usage.TotalTokens 30 (10+20), got %d", result.Usage.TotalTokens)
	}
}

func TestChatCompletions_NativeModel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "native-model",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Response from native model.",
				},
				Done:       true,
				DoneReason: "stop",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "native-model"}},
			})
			return
		}
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
	defer ts.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: true,
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: ts.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	sched := newTestScheduler(t, &cfg, reg, map[string]string{"test-backend": ts.URL})
	sched.Start()
	defer sched.Stop()

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"native-model","messages":[{"role":"user","content":"Hi"}]}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Model != "native-model" {
		t.Errorf("expected Model 'native-model', got %q", result.Model)
	}
	if len(result.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(result.Choices))
	}
	if result.Choices[0].Message.Content != "Response from native model." {
		t.Errorf("unexpected content: %q", result.Choices[0].Message.Content)
	}
}

func TestChatCompletions_ModelNotFound(t *testing.T) {
	// Registry with no matching model — no server needed since we won't poll.
	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "existing-alias",
			PrimaryModel: "some-model",
		}},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

	handler := ChatCompletionsHandler(logger, reg, nil)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"nonexistent-model","messages":[{"role":"user","content":"Hi"}]}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	var errResp chatError
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Type != "model_not_found" {
		t.Errorf("expected Type 'model_not_found', got %q", errResp.Error.Type)
	}
	if !strings.Contains(errResp.Error.Message, "nonexistent-model") {
		t.Errorf("expected message to mention 'nonexistent-model', got %q", errResp.Error.Message)
	}
}

func TestChatCompletions_QueueFull(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "test-alias",
			PrimaryModel: "some-model",
		}},
	}
	// Set queue max pending to 0 so any submit fails with ErrQueueFull.
	cfg.Scheduler.QueueMaxPending = 0

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

	// Build a scheduler with QueueMaxPending=0 — any submit will fail immediately.
	hosts := scheduler.NewHostLeaseManager(cfg.Hosts)
	backends := scheduler.NewBackendLeaseManager(cfg.OllamaBackends)
	stats := scheduler.NewStatsTracker()
	scorer := scheduler.NewScorer(stats, hosts, backends, cfg.Scheduler)
	sched := scheduler.NewScheduler(
		scheduler.NewQueue(cfg.Scheduler.AgingPerSecond),
		scorer,
		stats,
		http.DefaultClient,
		nil,
		nil,
		nil,
		func() []backend.BackendSnapshot { return nil },
	)

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"test-alias","messages":[{"role":"user","content":"Hi"}]}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}

	var errResp chatError
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Type != "server_error" {
		t.Errorf("expected Type 'server_error', got %q", errResp.Error.Type)
	}
	if errResp.Error.Code != "queue_full" {
		t.Errorf("expected Code 'queue_full', got %q", errResp.Error.Code)
	}
}

func TestChatCompletions_StreamRejected(t *testing.T) {
	// Streaming is now supported — verify it returns 200 with SSE response.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Hello! I am an AI assistant.",
				},
				Done:            true,
				DoneReason:      "stop",
				PromptEvalCount: 10,
				EvalCount:       20,
				TotalDuration:   1000000000,
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{
					{Name: "qwen:30b"},
					{Name: "qwen:14b"},
				},
			})
			return
		}
		// /api/ps
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
	defer ts.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "my-alias",
			PrimaryModel: "qwen:30b",
			BackupModels: []string{"qwen:14b"},
		}},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: ts.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	// Wait for initial poll to complete.
	time.Sleep(200 * time.Millisecond)

	sched := newTestScheduler(t, &cfg, reg, map[string]string{"test-backend": ts.URL})
	sched.Start()
	defer sched.Stop()

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"my-alias","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got %q", ct)
	}

	// Verify SSE response body.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(respBody), "data:") {
		t.Error("expected SSE data events in response body")
	}
	if !strings.Contains(string(respBody), "[DONE]") {
		t.Error("expected [DONE] marker in response body")
	}
	if !strings.Contains(string(respBody), "Hello! I am an AI assistant.") {
		t.Error("expected response content in response body")
	}
}

func TestChatCompletions_HTTPError(t *testing.T) {
	// Fake server: /api/tags and /api/ps succeed, but /api/chat returns 500.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "good-model"}},
			})
			return
		}
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
	defer ts.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: true,
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: ts.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	sched := newTestScheduler(t, &cfg, reg, map[string]string{"test-backend": ts.URL})
	sched.Start()
	defer sched.Stop()

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"good-model","messages":[{"role":"user","content":"Hi"}]}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}

	var errResp chatError
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Type != "server_error" {
		t.Errorf("expected Type 'server_error', got %q", errResp.Error.Type)
	}
}

func TestChatCompletions_Streaming_RetryBeforeFirstToken(t *testing.T) {
	var callCount1, callCount2 atomic.Int32

	// Backend 1: fails on /api/chat with 500.
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			callCount1.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "model-a"}},
			})
			return
		}
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
	defer server1.Close()

	// Backend 2: succeeds on /api/chat with streaming ndjson.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			callCount2.Add(1)
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintf(w, `{"model":"model-b","message":{"role":"assistant","content":"Hello"},"done":false}`+"\n")
			fmt.Fprintf(w, `{"model":"model-b","message":{"role":"assistant","content":" world"},"done":false}`+"\n")
			fmt.Fprintf(w, `{"model":"model-b","message":{"role":"assistant","content":""},"done":true,"total_duration":1000000000,"eval_count":5}`+"\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "model-b"}},
			})
			return
		}
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
	defer server2.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "test-alias",
			PrimaryModel: "model-a",
			BackupModels: []string{"model-b"},
		}},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "backend-1", URL: server1.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
		{ID: "backend-2", URL: server2.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}
	cfg.Scheduler.Retry.MaxAttempts = 2

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	sched := newTestScheduler(t, &cfg, reg, map[string]string{
		"backend-1": server1.URL,
		"backend-2": server2.URL,
	})
	sched.Start()
	defer sched.Stop()

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"test-alias","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got %q", ct)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(respBody), "data:") {
		t.Error("expected SSE data events in response body")
	}
	if !strings.Contains(string(respBody), "[DONE]") {
		t.Error("expected [DONE] marker in response body")
	}
	// Content is split across two SSE chunks: "Hello" and " world".
	if !strings.Contains(string(respBody), `"content":"Hello"`) {
		t.Error("expected 'Hello' content in SSE chunks")
	}
	if !strings.Contains(string(respBody), `"content":" world"`) {
		t.Error("expected ' world' content in SSE chunks")
	}

	if callCount1.Load() != 1 {
		t.Errorf("expected backend-1 (failing) to be called once, got %d", callCount1.Load())
	}
	if callCount2.Load() != 1 {
		t.Errorf("expected backend-2 (successful) to be called once, got %d", callCount2.Load())
	}
}

func TestChatCompletions_Streaming_RetryExhausted(t *testing.T) {
	var callCount atomic.Int32

	// Both backends return 500 on /api/chat.
	makeFailingServer := func(model string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
				callCount.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "/api/tags") {
				json.NewEncoder(w).Encode(ollama.TagsResponse{
					Models: []ollama.TagsModel{{Name: model}},
				})
				return
			}
			json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
		}))
	}

	server1 := makeFailingServer("model-a")
	defer server1.Close()
	server2 := makeFailingServer("model-b")
	defer server2.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "test-alias",
			PrimaryModel: "model-a",
			BackupModels: []string{"model-b"},
		}},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "backend-1", URL: server1.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
		{ID: "backend-2", URL: server2.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}
	cfg.Scheduler.Retry.MaxAttempts = 2

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	sched := newTestScheduler(t, &cfg, reg, map[string]string{
		"backend-1": server1.URL,
		"backend-2": server2.URL,
	})
	sched.Start()
	defer sched.Stop()

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"test-alias","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Headers are still 200 + SSE because they are written before the scheduler
	// attempt. Verify the stream closes gracefully with [DONE] but no content.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got %q", ct)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	// The stream should contain [DONE] (written unconditionally after loop).
	if !strings.Contains(string(respBody), "[DONE]") {
		t.Error("expected [DONE] marker in response body")
	}
	// No content chunks should have been received.
	if strings.Contains(string(respBody), `"content":"`) {
		t.Error("expected no content chunks in failed retry stream")
	}
	// Both backends should have been called.
	if callCount.Load() != 2 {
		t.Errorf("expected 2 backend calls (both failing), got %d", callCount.Load())
	}
}

func TestChatCompletions_Streaming_ClientDisconnect(t *testing.T) {
	var serverCancelled atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			w.Header().Set("Content-Type", "application/x-ndjson")
			// Send first chunk and flush.
			fmt.Fprintf(w, `{"model":"model-a","message":{"role":"assistant","content":"Hello"},"done":false}`+"\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Wait for context cancellation (client disconnect).
			<-r.Context().Done()
			serverCancelled.Store(true)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "model-a"}},
			})
			return
		}
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: true,
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: server.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	sched := newTestScheduler(t, &cfg, reg, map[string]string{"test-backend": server.URL})
	sched.Start()
	defer sched.Stop()

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"model-a","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	// Verify streaming started.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Read first SSE event to confirm streaming started.
	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() {
		t.Fatal("expected at least one SSE event")
	}
	firstLine := scanner.Text()
	if !strings.HasPrefix(firstLine, "data:") {
		t.Errorf("expected SSE data event, got %q", firstLine)
	}

	// Client disconnect: close the response body.
	resp.Body.Close()

	// Wait briefly for cancellation to propagate.
	time.Sleep(300 * time.Millisecond)

	if !serverCancelled.Load() {
		t.Error("expected backend request context to be cancelled on client disconnect")
	}
}

func TestChatCompletions_Streaming_ModelNotFound(t *testing.T) {
	// Registry with no matching model — same as non-streaming model-not-found
	// test, but with stream:true in the request.
	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "existing-alias",
			PrimaryModel: "some-model",
		}},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

	handler := ChatCompletionsHandler(logger, reg, nil)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"nonexistent-model","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Model resolution happens before the streaming branch, so this should
	// return a normal JSON error (404) — not SSE.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	var errResp chatError
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Type != "model_not_found" {
		t.Errorf("expected Type 'model_not_found', got %q", errResp.Error.Type)
	}
	if !strings.Contains(errResp.Error.Message, "nonexistent-model") {
		t.Errorf("expected message to mention 'nonexistent-model', got %q", errResp.Error.Message)
	}
}

func TestChatCompletions_Streaming_QueueFull(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "test-alias",
			PrimaryModel: "some-model",
		}},
	}
	// Set queue max pending to 0 so any submit fails with ErrQueueFull.
	cfg.Scheduler.QueueMaxPending = 0

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

	// Build a scheduler with QueueMaxPending=0.
	hosts := scheduler.NewHostLeaseManager(cfg.Hosts)
	backends := scheduler.NewBackendLeaseManager(cfg.OllamaBackends)
	stats := scheduler.NewStatsTracker()
	scorer := scheduler.NewScorer(stats, hosts, backends, cfg.Scheduler)
	sched := scheduler.NewScheduler(
		scheduler.NewQueue(cfg.Scheduler.AgingPerSecond),
		scorer,
		stats,
		http.DefaultClient,
		nil,
		nil,
		nil,
		func() []backend.BackendSnapshot { return nil },
	)

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"test-alias","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// SSE headers are written before the submit attempt, so status is 200.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (SSE headers already sent), got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got %q", ct)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	// The body should contain an SSE error event with queue_full code.
	if !strings.Contains(string(respBody), "data:") {
		t.Error("expected SSE data event in body")
	}
	if !strings.Contains(string(respBody), `"code":"queue_full"`) {
		t.Errorf("expected queue_full error in SSE body, got: %s", string(respBody))
	}
}

func TestChatCompletions_NonStreaming_StillWorks(t *testing.T) {
	// Verify that non-streaming requests still work alongside streaming support.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Non-streaming response.",
				},
				Done:            true,
				DoneReason:      "stop",
				PromptEvalCount: 5,
				EvalCount:       10,
				TotalDuration:   500000000,
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{
					{Name: "qwen:30b"},
					{Name: "qwen:14b"},
				},
			})
			return
		}
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
	defer ts.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "my-alias",
			PrimaryModel: "qwen:30b",
			BackupModels: []string{"qwen:14b"},
		}},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: ts.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	sched := newTestScheduler(t, &cfg, reg, map[string]string{"test-backend": ts.URL})
	sched.Start()
	defer sched.Stop()

	handler := ChatCompletionsHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	// Explicitly set stream:false to verify the non-streaming path.
	body := `{"model":"my-alias","messages":[{"role":"user","content":"Hello"}],"stream":false}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}

	var result chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(result.ID, "chatcmpl-") {
		t.Errorf("expected ID to start with 'chatcmpl-', got %q", result.ID)
	}
	if result.Object != "chat.completion" {
		t.Errorf("expected Object 'chat.completion', got %q", result.Object)
	}
	if result.Model != "my-alias" {
		t.Errorf("expected Model 'my-alias', got %q", result.Model)
	}
	if len(result.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(result.Choices))
	}
	if result.Choices[0].Message.Content != "Non-streaming response." {
		t.Errorf("unexpected content: %q", result.Choices[0].Message.Content)
	}
	if result.Choices[0].FinishReason != "stop" {
		t.Errorf("expected FinishReason 'stop', got %q", result.Choices[0].FinishReason)
	}
	if result.Usage.PromptTokens != 5 {
		t.Errorf("expected PromptTokens 5, got %d", result.Usage.PromptTokens)
	}
	if result.Usage.CompletionTokens != 10 {
		t.Errorf("expected CompletionTokens 10, got %d", result.Usage.CompletionTokens)
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("expected TotalTokens 15, got %d", result.Usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// buildOllamaRequest unit tests
// ---------------------------------------------------------------------------

func TestBuildOllamaRequest_DefaultsOnly(t *testing.T) {
	thinkFalse := false
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     &thinkFalse,
	}

	req := &chatCompletionRequest{
		Model:    "test-model",
		Messages: []chatRequestMessage{{Role: "user", Content: json.RawMessage(`"Hello"`)}},
	}
	messages := []ollama.ChatMessage{{Role: "user", Content: "Hello"}}

	result := buildOllamaRequest("test-model", messages, defaults, nil, req, config.PolicyConfig{})

	if result.Model != "test-model" {
		t.Errorf("expected Model 'test-model', got %q", result.Model)
	}
	if result.Stream {
		t.Error("expected Stream to be false")
	}
	if result.KeepAlive != "5m" {
		t.Errorf("expected KeepAlive '5m', got %q", result.KeepAlive)
	}
	if result.Think == nil {
		t.Fatal("expected non-nil Think")
	}
	if *result.Think {
		t.Error("expected Think to be false")
	}
	if result.Options != nil {
		t.Errorf("expected nil Options (no global defaults), got %+v", result.Options)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("expected Role 'user', got %q", result.Messages[0].Role)
	}
	if result.Messages[0].Content != "Hello" {
		t.Errorf("expected Content 'Hello', got %q", result.Messages[0].Content)
	}
}

func TestBuildOllamaRequest_AliasThinkOverride(t *testing.T) {
	thinkFalse := false
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     &thinkFalse,
	}

	thinkTrue := true
	alias := &config.AliasConfig{
		Name:         "test-alias",
		PrimaryModel: "test-model",
		Overrides: config.AliasOverrides{
			Think: &thinkTrue,
		},
	}

	req := &chatCompletionRequest{Model: "test-model"}
	messages := []ollama.ChatMessage{{Role: "user", Content: "Hi"}}

	result := buildOllamaRequest("test-model", messages, defaults, alias, req, config.PolicyConfig{})

	if result.Think == nil {
		t.Fatal("expected non-nil Think")
	}
	if !*result.Think {
		t.Error("expected Think to be true (overridden by alias)")
	}
	// No global Options — Options should be nil (alias has no Options override).
	if result.Options != nil {
		t.Errorf("expected nil Options (no alias options), got %+v", result.Options)
	}
	if result.KeepAlive != "5m" {
		t.Errorf("expected KeepAlive '5m', got %q", result.KeepAlive)
	}
}

func TestBuildOllamaRequest_AliasOptionsMerge(t *testing.T) {
	thinkFalse := false
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     &thinkFalse,
	}

	alias := &config.AliasConfig{
		Name:         "test-alias",
		PrimaryModel: "test-model",
		Overrides: config.AliasOverrides{
			Options: &config.OllamaOptions{
				Temperature: 0.1,
			},
		},
	}

	req := &chatCompletionRequest{Model: "test-model"}
	messages := []ollama.ChatMessage{{Role: "user", Content: "Hi"}}

	result := buildOllamaRequest("test-model", messages, defaults, alias, req, config.PolicyConfig{})

	// Alias options should be applied (only Temperature set, TopP is 0 so not applied).
	if result.Options == nil {
		t.Fatal("expected non-nil Options from alias override")
	}
	if result.Options.Temperature != 0.1 {
		t.Errorf("expected Temperature 0.1 (from alias override), got %f", result.Options.Temperature)
	}
	// NumThread and NumCtx are not set by alias (only backend or global).
	if result.Options.NumThread != 0 {
		t.Errorf("expected NumThread 0 (not set by alias), got %d", result.Options.NumThread)
	}
	if result.Options.NumCtx != 0 {
		t.Errorf("expected NumCtx 0 (not set by alias), got %d", result.Options.NumCtx)
	}
	if result.Options.TopP != 0 {
		t.Errorf("expected TopP 0 (not set by alias), got %f", result.Options.TopP)
	}
}

func TestBuildOllamaRequest_ClientOverridesAllowed(t *testing.T) {
	thinkFalse := false
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     &thinkFalse,
	}

	temp := 0.5
	topP := 0.8
	req := &chatCompletionRequest{
		Model:       "test-model",
		MaxTokens:   100,
		Temperature: &temp,
		TopP:        &topP,
	}

	messages := []ollama.ChatMessage{{Role: "user", Content: "Hi"}}
	policy := config.PolicyConfig{AllowClientOverrideOptions: true}

	result := buildOllamaRequest("test-model", messages, defaults, nil, req, policy)

	if result.Options == nil {
		t.Fatal("expected non-nil Options from client overrides")
	}
	if result.Options.NumPredict != 100 {
		t.Errorf("expected NumPredict 100, got %d", result.Options.NumPredict)
	}
	if result.Options.Temperature != 0.5 {
		t.Errorf("expected Temperature 0.5 (client override), got %f", result.Options.Temperature)
	}
	if result.Options.TopP != 0.8 {
		t.Errorf("expected TopP 0.8 (client override), got %f", result.Options.TopP)
	}
	// NumThread and NumCtx only come from backend, not global defaults.
	if result.Options.NumThread != 0 {
		t.Errorf("expected NumThread 0 (not from global defaults), got %d", result.Options.NumThread)
	}
	if result.Options.NumCtx != 0 {
		t.Errorf("expected NumCtx 0 (not from global defaults), got %d", result.Options.NumCtx)
	}
}

func TestBuildOllamaRequest_ClientOverridesDisallowed(t *testing.T) {
	thinkFalse := false
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     &thinkFalse,
	}

	temp := 0.5
	req := &chatCompletionRequest{
		Model:       "test-model",
		MaxTokens:   100,
		Temperature: &temp,
	}

	messages := []ollama.ChatMessage{{Role: "user", Content: "Hi"}}
	policy := config.PolicyConfig{AllowClientOverrideOptions: false}

	result := buildOllamaRequest("test-model", messages, defaults, nil, req, policy)

	// Client overrides should be ignored when disallowed — Options stays nil.
	if result.Options != nil {
		t.Errorf("expected nil Options (client overrides disallowed, no alias), got %+v", result.Options)
	}
}

func TestBuildOllamaRequest_FullStack(t *testing.T) {
	// Test all three layers: defaults -> alias -> client overrides.
	// Options are no longer global; Temperature/TopP come from alias only.
	thinkFalse := false
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     &thinkFalse,
	}

	thinkTrue := true
	aliasKeepAlive := "15m"
	alias := &config.AliasConfig{
		Name:         "test-alias",
		PrimaryModel: "test-model",
		Overrides: config.AliasOverrides{
			Think:     &thinkTrue,
			KeepAlive: aliasKeepAlive,
			Options: &config.OllamaOptions{
				Temperature: 0.1, // alias overrides temperature
				TopP:        0.85,
			},
		},
	}

	temp := 0.5
	topP := 0.8
	req := &chatCompletionRequest{
		Model:       "test-model",
		MaxTokens:   200,
		Temperature: &temp,
		TopP:        &topP,
	}

	messages := []ollama.ChatMessage{{Role: "user", Content: "Hi"}}
	policy := config.PolicyConfig{AllowClientOverrideOptions: true}

	result := buildOllamaRequest("test-model", messages, defaults, alias, req, policy)

	// Alias KeepAlive override.
	if result.KeepAlive != "15m" {
		t.Errorf("expected KeepAlive '15m' (alias override), got %q", result.KeepAlive)
	}

	// Alias think override.
	if result.Think == nil {
		t.Fatal("expected non-nil Think")
	}
	if !*result.Think {
		t.Error("expected Think to be true (alias override)")
	}

	// Temperature: alias(0.1) -> client(0.5) = 0.5 (client wins).
	if result.Options.Temperature != 0.5 {
		t.Errorf("expected Temperature 0.5 (client overrides alias), got %f", result.Options.Temperature)
	}

	// TopP: alias(0.85) -> client(0.8) = 0.8 (client wins).
	if result.Options.TopP != 0.8 {
		t.Errorf("expected TopP 0.8, got %f", result.Options.TopP)
	}

	// MaxTokens -> NumPredict.
	if result.Options.NumPredict != 200 {
		t.Errorf("expected NumPredict 200, got %d", result.Options.NumPredict)
	}

	// NumThread/NumCtx are not set by alias, defaults, or client — only backend sets them.
	if result.Options.NumThread != 0 {
		t.Errorf("expected NumThread 0 (only backend sets it), got %d", result.Options.NumThread)
	}
	if result.Options.NumCtx != 0 {
		t.Errorf("expected NumCtx 0 (only backend sets it), got %d", result.Options.NumCtx)
	}
}

func TestBuildOllamaRequest_StopParsing_Array(t *testing.T) {
	policy := config.PolicyConfig{AllowClientOverrideOptions: true}
	req := &chatCompletionRequest{
		Model: "test",
		Stop:  json.RawMessage(`["\n", "user:"]`),
	}

	result := buildOllamaRequest("test", nil, config.OllamaDefaultsConfig{}, nil, req, policy)

	if len(result.Options.Stop) != 2 {
		t.Fatalf("expected 2 stop tokens, got %d: %v", len(result.Options.Stop), result.Options.Stop)
	}
	if result.Options.Stop[0] != "\n" {
		t.Errorf("expected stop[0] '\\n', got %q", result.Options.Stop[0])
	}
	if result.Options.Stop[1] != "user:" {
		t.Errorf("expected stop[1] 'user:', got %q", result.Options.Stop[1])
	}
}

func TestBuildOllamaRequest_StopParsing_SingleString(t *testing.T) {
	policy := config.PolicyConfig{AllowClientOverrideOptions: true}
	req := &chatCompletionRequest{
		Model: "test",
		Stop:  json.RawMessage(`"\n"`),
	}

	result := buildOllamaRequest("test", nil, config.OllamaDefaultsConfig{}, nil, req, policy)

	if len(result.Options.Stop) != 1 {
		t.Fatalf("expected 1 stop token, got %d: %v", len(result.Options.Stop), result.Options.Stop)
	}
	if result.Options.Stop[0] != "\n" {
		t.Errorf("expected stop[0] '\\n', got %q", result.Options.Stop[0])
	}
}
