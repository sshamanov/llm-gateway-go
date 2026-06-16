package anthropic

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
// Test helpers
// ---------------------------------------------------------------------------

// newTestScheduler creates a Scheduler from config and registry for use in
// anthropic handler tests. The scheduler is NOT started — the caller must call
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

// ---------------------------------------------------------------------------
// Handler integration tests
// ---------------------------------------------------------------------------

func TestMessages_Basic(t *testing.T) {
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

	handler := MessagesHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"my-alias","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result anthropicMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Verify ID format: "msg_" (4) + 16 hex chars = 20 chars total.
	if !strings.HasPrefix(result.ID, "msg_") {
		t.Errorf("expected ID to start with 'msg_', got %q", result.ID)
	}
	if len(result.ID) != 20 {
		t.Errorf("expected ID length 20, got %d: %q", len(result.ID), result.ID)
	}

	if result.Type != "message" {
		t.Errorf("expected Type 'message', got %q", result.Type)
	}
	if result.Role != "assistant" {
		t.Errorf("expected Role 'assistant', got %q", result.Role)
	}
	if result.Model != "my-alias" {
		t.Errorf("expected Model 'my-alias', got %q", result.Model)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("expected StopReason 'end_turn', got %q", result.StopReason)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Type != "text" {
		t.Errorf("expected content Type 'text', got %q", result.Content[0].Type)
	}
	if result.Content[0].Text != "Hello! I am an AI assistant." {
		t.Errorf("expected content Text 'Hello! I am an AI assistant.', got %q", result.Content[0].Text)
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("expected InputTokens 10, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 20 {
		t.Errorf("expected OutputTokens 20, got %d", result.Usage.OutputTokens)
	}
}

func TestMessages_SystemString(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			capturedBody, _ = io.ReadAll(r.Body)
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Response.",
				},
				Done:       true,
				DoneReason: "stop",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "qwen:30b"}},
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

	handler := MessagesHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"my-alias","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"system":"You are a helpful assistant."}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify the Ollama request included a system message.
	var chatReq ollama.ChatRequest
	if err := json.Unmarshal(capturedBody, &chatReq); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, msg := range chatReq.Messages {
		if msg.Role == "system" && msg.Content == "You are a helpful assistant." {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected system message 'You are a helpful assistant.' in Ollama request messages")
	}
}

func TestMessages_SystemArray(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			capturedBody, _ = io.ReadAll(r.Body)
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Response.",
				},
				Done:       true,
				DoneReason: "stop",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "qwen:30b"}},
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

	handler := MessagesHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"my-alias","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"system":[{"type":"text","text":"You are helpful."},{"type":"text","text":" Be concise."}]}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var chatReq ollama.ChatRequest
	if err := json.Unmarshal(capturedBody, &chatReq); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, msg := range chatReq.Messages {
		if msg.Role == "system" && msg.Content == "You are helpful. Be concise." {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected combined system message 'You are helpful. Be concise.' in Ollama request messages")
	}
}

func TestMessages_ThinkingEnabled(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			capturedBody, _ = io.ReadAll(r.Body)
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Thinking result.",
				},
				Done:       true,
				DoneReason: "stop",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "qwen:30b"}},
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

	handler := MessagesHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	// Request with thinking enabled.
	body := `{"model":"my-alias","messages":[{"role":"user","content":"Think hard"}],"max_tokens":100,"thinking":{"type":"enabled","budget_tokens":1024}}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify the Ollama request has Temperature=1.0 (forced by thinking).
	var chatReq ollama.ChatRequest
	if err := json.Unmarshal(capturedBody, &chatReq); err != nil {
		t.Fatal(err)
	}

	if chatReq.Options == nil {
		t.Fatal("expected non-nil Options in Ollama request")
	}
	if chatReq.Options.Temperature != 1.0 {
		t.Errorf("expected Temperature 1.0 (forced by thinking), got %f", chatReq.Options.Temperature)
	}
}

func TestMessages_MaxTokensRequired(t *testing.T) {
	cfg := config.DefaultConfig()
	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

	handler := MessagesHandler(logger, reg, nil)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"my-alias","messages":[{"role":"user","content":"Hi"}],"max_tokens":0}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var errResp anthropicErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Type != "error" {
		t.Errorf("expected Type 'error', got %q", errResp.Type)
	}
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("expected Error.Type 'invalid_request_error', got %q", errResp.Error.Type)
	}
	if !strings.Contains(errResp.Error.Message, "max_tokens") {
		t.Errorf("expected message to mention 'max_tokens', got %q", errResp.Error.Message)
	}
}

func TestMessages_ModelNotFound(t *testing.T) {
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

	handler := MessagesHandler(logger, reg, nil)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"nonexistent-model","messages":[{"role":"user","content":"Hi"}],"max_tokens":100}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	var errResp anthropicErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("expected Error.Type 'invalid_request_error', got %q", errResp.Error.Type)
	}
	if !strings.Contains(errResp.Error.Message, "nonexistent-model") {
		t.Errorf("expected message to mention 'nonexistent-model', got %q", errResp.Error.Message)
	}
}

func TestMessages_Streaming(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
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
	defer ts.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "my-alias",
			PrimaryModel: "model-b",
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

	handler := MessagesHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"my-alias","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":true}`
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

	bodyStr := string(respBody)

	// Check for Anthropic SSE event types.
	if !strings.Contains(bodyStr, "content_block_start") {
		t.Error("expected content_block_start event")
	}
	if !strings.Contains(bodyStr, "content_block_delta") {
		t.Error("expected content_block_delta event")
	}
	if !strings.Contains(bodyStr, "content_block_stop") {
		t.Error("expected content_block_stop event")
	}
	if !strings.Contains(bodyStr, "message_delta") {
		t.Error("expected message_delta event")
	}
	if !strings.Contains(bodyStr, "message_stop") {
		t.Error("expected message_stop event")
	}
	if !strings.Contains(bodyStr, "[DONE]") {
		t.Error("expected [DONE] marker")
	}

	// Verify content is present in delta events.
	if !strings.Contains(bodyStr, `"text":"Hello"`) {
		t.Error("expected 'Hello' in content delta")
	}
	if !strings.Contains(bodyStr, `"text":" world"`) {
		t.Error("expected ' world' in content delta")
	}
}

func TestMessages_StreamingClientDisconnect(t *testing.T) {
	var serverCancelled atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintf(w, `{"model":"model-a","message":{"role":"assistant","content":"Hello"},"done":false}`+"\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
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

	handler := MessagesHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"model-a","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":true}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Read first SSE event to confirm streaming started.
	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() {
		t.Fatal("expected at least one SSE event")
	}
	firstLine := scanner.Text()
	if !strings.HasPrefix(firstLine, "event:") && !strings.HasPrefix(firstLine, "data:") {
		t.Errorf("expected SSE event line, got %q", firstLine)
	}

	// Client disconnect: close the response body.
	resp.Body.Close()

	// Wait briefly for cancellation to propagate.
	time.Sleep(300 * time.Millisecond)

	if !serverCancelled.Load() {
		t.Error("expected backend request context to be cancelled on client disconnect")
	}
}

func TestMessages_QueueFull(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{{
			Name:         "test-alias",
			PrimaryModel: "some-model",
		}},
	}
	cfg.Scheduler.QueueMaxPending = 0

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

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

	handler := MessagesHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"test-alias","messages":[{"role":"user","content":"Hi"}],"max_tokens":100}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}

	var errResp anthropicErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Type != "error" {
		t.Errorf("expected Type 'error', got %q", errResp.Type)
	}
	if errResp.Error.Type != "api_error" {
		t.Errorf("expected Error.Type 'api_error', got %q", errResp.Error.Type)
	}
	if !strings.Contains(errResp.Error.Message, "Queue full") {
		t.Errorf("expected message to contain 'Queue full', got %q", errResp.Error.Message)
	}
}

// ---------------------------------------------------------------------------
// Unit tests for conversion functions
// ---------------------------------------------------------------------------

func TestConvertInputMessages_StringContent(t *testing.T) {
	rawContent := json.RawMessage(`"Hello"`)
	msgs := []anthropicInputMessage{
		{Role: "user", Content: rawContent},
	}

	result, err := convertInputMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("expected Role 'user', got %q", result[0].Role)
	}
	if result[0].Content != "Hello" {
		t.Errorf("expected Content 'Hello', got %q", result[0].Content)
	}
	if len(result[0].Images) != 0 {
		t.Errorf("expected no images, got %d", len(result[0].Images))
	}
}

func TestConvertInputMessages_ArrayContent(t *testing.T) {
	rawContent := json.RawMessage(`[{"type":"text","text":"Hello"},{"type":"text","text":"World"}]`)
	msgs := []anthropicInputMessage{
		{Role: "user", Content: rawContent},
	}

	result, err := convertInputMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Content != "HelloWorld" {
		t.Errorf("expected combined Content 'HelloWorld', got %q", result[0].Content)
	}
}

func TestConvertInputMessages_ImageBlock(t *testing.T) {
	rawContent := json.RawMessage(`[
		{"type":"text","text":"What is in this image?"},
		{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}
	]`)
	msgs := []anthropicInputMessage{
		{Role: "user", Content: rawContent},
	}

	result, err := convertInputMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Content != "What is in this image?" {
		t.Errorf("expected Content 'What is in this image?', got %q", result[0].Content)
	}
	if len(result[0].Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(result[0].Images))
	}
	expectedBase64 := "iVBORw0KGgo="
	if result[0].Images[0] != expectedBase64 {
		t.Errorf("expected raw base64 %q, got %q", expectedBase64, result[0].Images[0])
	}
}

func TestConvertSystem_Nil(t *testing.T) {
	result, err := convertSystem(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Errorf("expected nil for empty system, got %+v", result)
	}

	// Also test empty raw message.
	result, err = convertSystem(json.RawMessage{})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Errorf("expected nil for empty system, got %+v", result)
	}
}

func TestConvertSystem_String(t *testing.T) {
	raw := json.RawMessage(`"You are a helpful assistant."`)
	result, err := convertSystem(raw)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Role != "system" {
		t.Errorf("expected Role 'system', got %q", result.Role)
	}
	if result.Content != "You are a helpful assistant." {
		t.Errorf("expected Content 'You are a helpful assistant.', got %q", result.Content)
	}
}

func TestConvertSystem_EmptyString(t *testing.T) {
	raw := json.RawMessage(`""`)
	result, err := convertSystem(raw)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Errorf("expected nil for empty string system, got %+v", result)
	}
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"", "end_turn"},
		{"unknown", "end_turn"},
		{"tool_calls", "end_turn"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := mapStopReason(tc.input)
			if got != tc.expected {
				t.Errorf("mapStopReason(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestMessages_EmptyModel(t *testing.T) {
	cfg := config.DefaultConfig()
	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

	handler := MessagesHandler(logger, reg, nil)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"messages":[{"role":"user","content":"Hi"}],"max_tokens":100}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var errResp anthropicErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errResp.Error.Message, "model is required") {
		t.Errorf("expected message about model required, got %q", errResp.Error.Message)
	}
}

func TestBuildAnthropicOptions_ThinkOverride(t *testing.T) {
	thinkFalse := false
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     &thinkFalse,
	}

	thinkingEnabled := "enabled"
	req := &anthropicMessageRequest{
		Model:     "test",
		MaxTokens: 100,
		Thinking: &anthropicThinkingConfig{
			Type:         thinkingEnabled,
			BudgetTokens: 1024,
		},
	}

	options, think, keepAlive := buildAnthropicOptions(defaults, nil, req, config.PolicyConfig{})

	if keepAlive != "5m" {
		t.Errorf("expected KeepAlive '5m', got %q", keepAlive)
	}
	if options.Temperature != 1.0 {
		t.Errorf("expected Temperature 1.0 (forced by thinking), got %f", options.Temperature)
	}
	if think == nil {
		t.Fatal("expected non-nil think pointer")
	}
	if !*think {
		t.Error("expected think to be true")
	}
	if options.NumPredict != 100 {
		t.Errorf("expected NumPredict 100, got %d", options.NumPredict)
	}
	// NumThread/NumCtx not set from global defaults; only from backend.
	if options.NumThread != 0 {
		t.Errorf("expected NumThread 0 (not from global), got %d", options.NumThread)
	}
	if options.NumCtx != 0 {
		t.Errorf("expected NumCtx 0 (not from global), got %d", options.NumCtx)
	}
}

func TestBuildAnthropicOptions_ClientOverrides(t *testing.T) {
	defaults := config.OllamaDefaultsConfig{}

	temp := 0.3
	topP := 0.5
	topK := 40
	req := &anthropicMessageRequest{
		Model:         "test",
		MaxTokens:     200,
		Temperature:   &temp,
		TopP:          &topP,
		TopK:          &topK,
		StopSequences: []string{"\n", "user:"},
	}

	options, think, keepAlive := buildAnthropicOptions(defaults, nil, req, config.PolicyConfig{
		AllowClientOverrideOptions: true,
	})

	if keepAlive != "" {
		t.Errorf("expected empty KeepAlive, got %q", keepAlive)
	}
	if think != nil {
		t.Error("expected nil think when thinking not enabled")
	}
	if options.Temperature != 0.3 {
		t.Errorf("expected Temperature 0.3, got %f", options.Temperature)
	}
	if options.TopP != 0.5 {
		t.Errorf("expected TopP 0.5, got %f", options.TopP)
	}
	if options.TopK != 40 {
		t.Errorf("expected TopK 40, got %d", options.TopK)
	}
	if len(options.Stop) != 2 {
		t.Fatalf("expected 2 stop sequences, got %d", len(options.Stop))
	}
	if options.Stop[0] != "\n" {
		t.Errorf("expected stop[0] '\\n', got %q", options.Stop[0])
	}
	if options.Stop[1] != "user:" {
		t.Errorf("expected stop[1] 'user:', got %q", options.Stop[1])
	}
	if options.NumPredict != 200 {
		t.Errorf("expected NumPredict 200, got %d", options.NumPredict)
	}
}

func TestBuildAnthropicOptions_ClientOverridesDisallowed(t *testing.T) {
	defaults := config.OllamaDefaultsConfig{}

	temp := 0.3
	req := &anthropicMessageRequest{
		Model:       "test",
		MaxTokens:   200,
		Temperature: &temp,
	}

	options, _, _ := buildAnthropicOptions(defaults, nil, req, config.PolicyConfig{
		AllowClientOverrideOptions: false,
	})

	// Temperature should be 0 (no global, no alias, client override disallowed).
	if options.Temperature != 0 {
		t.Errorf("expected Temperature 0 (no global/alias defaults), got %f", options.Temperature)
	}
	// MaxTokens always applies regardless of AllowClientOverrideOptions.
	if options.NumPredict != 200 {
		t.Errorf("expected NumPredict 200 (always applied), got %d", options.NumPredict)
	}
}

func TestBuildAnthropicOptions_AliasOverrides(t *testing.T) {
	defaults := config.OllamaDefaultsConfig{}

	alias := &config.AliasConfig{
		Name: "test-alias",
		Overrides: config.AliasOverrides{
			Options: &config.OllamaOptions{
				Temperature: 0.1,
			},
		},
	}

	req := &anthropicMessageRequest{
		Model:     "test",
		MaxTokens: 100,
	}

	options, _, _ := buildAnthropicOptions(defaults, alias, req, config.PolicyConfig{})

	if options.Temperature != 0.1 {
		t.Errorf("expected Temperature 0.1 (alias override), got %f", options.Temperature)
	}
	// NumThread/NumCtx are not set by alias — only by backend.
	if options.NumThread != 0 {
		t.Errorf("expected NumThread 0 (not from alias/defaults), got %d", options.NumThread)
	}
	if options.NumCtx != 0 {
		t.Errorf("expected NumCtx 0 (not from alias/defaults), got %d", options.NumCtx)
	}
	if options.TopP != 0 {
		t.Errorf("expected TopP 0 (not from alias/defaults), got %f", options.TopP)
	}
}

func TestMessages_BackendError(t *testing.T) {
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

	handler := MessagesHandler(logger, reg, sched)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"good-model","messages":[{"role":"user","content":"Hi"}],"max_tokens":100}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}

	var errResp anthropicErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Type != "api_error" {
		t.Errorf("expected Error.Type 'api_error', got %q", errResp.Error.Type)
	}
}

func TestConvertInputMessages_ToolUseBlock(t *testing.T) {
	rawContent := json.RawMessage(`[
		{"type":"text","text":"Let me check the weather."},
		{"type":"tool_use","id":"toolu_123","name":"get_weather","input":{"location":"SF"}}
	]`)
	msgs := []anthropicInputMessage{
		{Role: "assistant", Content: rawContent},
	}

	result, err := convertInputMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "assistant" {
		t.Errorf("expected Role 'assistant', got %q", result[0].Role)
	}
	if !strings.Contains(result[0].Content, "Let me check the weather.") {
		t.Errorf("expected content to contain text, got %q", result[0].Content)
	}
	if !strings.Contains(result[0].Content, "tool_use") {
		t.Errorf("expected content to contain tool_use JSON, got %q", result[0].Content)
	}
	if !strings.Contains(result[0].Content, "get_weather") {
		t.Errorf("expected content to contain function name, got %q", result[0].Content)
	}
}
