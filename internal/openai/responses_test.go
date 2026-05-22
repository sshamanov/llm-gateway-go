package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestResponses_StringInput(t *testing.T) {
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
			Name:         "llama3",
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

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"llama3","input":"Hello"}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(result.ID, "resp_") {
		t.Errorf("expected ID to start with 'resp_', got %q", result.ID)
	}
	if result.Object != "response" {
		t.Errorf("expected Object 'response', got %q", result.Object)
	}
	if result.Created <= 0 {
		t.Errorf("expected positive Created timestamp, got %d", result.Created)
	}
	if result.Model != "llama3" {
		t.Errorf("expected Model 'llama3', got %q", result.Model)
	}
	if len(result.Output) != 1 {
		t.Fatalf("expected 1 output, got %d", len(result.Output))
	}
	if result.Output[0].Type != "message" {
		t.Errorf("expected Output[0].Type 'message', got %q", result.Output[0].Type)
	}
	if result.Output[0].Role != "assistant" {
		t.Errorf("expected Output[0].Role 'assistant', got %q", result.Output[0].Role)
	}
	if len(result.Output[0].Content) != 1 {
		t.Fatalf("expected 1 output content, got %d", len(result.Output[0].Content))
	}
	if result.Output[0].Content[0].Type != "output_text" {
		t.Errorf("expected Content[0].Type 'output_text', got %q", result.Output[0].Content[0].Type)
	}
	if result.Output[0].Content[0].Text != "Hello! I am an AI assistant." {
		t.Errorf("unexpected text: %q", result.Output[0].Content[0].Text)
	}
	if len(result.Output[0].Content[0].Annotations) != 0 {
		t.Errorf("expected empty annotations, got %v", result.Output[0].Content[0].Annotations)
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("expected InputTokens 10, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 20 {
		t.Errorf("expected OutputTokens 20, got %d", result.Usage.OutputTokens)
	}
	if result.Usage.TotalTokens != 30 {
		t.Errorf("expected TotalTokens 30, got %d", result.Usage.TotalTokens)
	}
}

func TestResponses_ArrayInput(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Response from array input.",
				},
				Done:       true,
				DoneReason: "stop",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{
					{Name: "qwen:30b"},
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
			Name:         "llama3",
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

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"llama3","input":[{"role":"user","content":[{"type":"input_text","text":"Hello"}]}]}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Object != "response" {
		t.Errorf("expected Object 'response', got %q", result.Object)
	}
	if result.Model != "llama3" {
		t.Errorf("expected Model 'llama3', got %q", result.Model)
	}
	if len(result.Output) != 1 {
		t.Fatalf("expected 1 output, got %d", len(result.Output))
	}
	if result.Output[0].Content[0].Text != "Response from array input." {
		t.Errorf("unexpected text: %q", result.Output[0].Content[0].Text)
	}
}

func TestResponses_Instructions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Helpful response with instructions.",
				},
				Done:       true,
				DoneReason: "stop",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{
					{Name: "qwen:30b"},
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
			Name:         "llama3",
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

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"llama3","input":"Hello","instructions":"Be helpful"}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Object != "response" {
		t.Errorf("expected Object 'response', got %q", result.Object)
	}
	if result.Model != "llama3" {
		t.Errorf("expected Model 'llama3', got %q", result.Model)
	}
	if len(result.Output) != 1 {
		t.Fatalf("expected 1 output, got %d", len(result.Output))
	}
	if result.Output[0].Content[0].Text != "Helpful response with instructions." {
		t.Errorf("unexpected text: %q", result.Output[0].Content[0].Text)
	}
}

func TestResponses_ModelNotFound(t *testing.T) {
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

	handler := ResponsesHandler(logger, reg, nil, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"nonexistent-model","input":"Hello"}`
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

func TestResponses_Streaming(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Streaming response content.",
				},
				Done:            true,
				DoneReason:      "stop",
				PromptEvalCount: 5,
				EvalCount:       10,
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
			Name:         "llama3",
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

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"llama3","input":"Hello","stream":true}`
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
	if !strings.Contains(string(respBody), "response.output_text.delta") {
		t.Error("expected response.output_text.delta event in response body")
	}
	if !strings.Contains(string(respBody), "Streaming response content.") {
		t.Error("expected streaming response content in response body")
	}
}

func TestResponses_Streaming_Works(t *testing.T) {
	// Verify streaming works with array input (previously StreamRejected test).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			json.NewEncoder(w).Encode(ollama.ChatResponse{
				Model: "qwen:30b",
				CreatedAt: time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
				Message: ollama.ChatMessage{
					Role:    "assistant",
					Content: "Array streaming works.",
				},
				Done:       true,
				DoneReason: "stop",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{
					{Name: "qwen:30b"},
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
			Name:         "llama3",
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

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"llama3","input":[{"role":"user","content":[{"type":"input_text","text":"Hi"}]}],"stream":true}`
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
	if !strings.Contains(string(respBody), "Array streaming works.") {
		t.Error("expected streaming response content in response body")
	}
}

func TestResponses_QueueFull(t *testing.T) {
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
		func() []backend.BackendSnapshot { return nil },
	)

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"test-alias","input":"Hello"}`
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

// ---------------------------------------------------------------------------
// convertInput unit tests
// ---------------------------------------------------------------------------

func TestConvertInput_String(t *testing.T) {
	input := json.RawMessage(`"Hello"`)
	messages, meta, err := convertInput(input, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer meta.cleanup()

	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Role != "user" {
		t.Errorf("expected Role 'user', got %q", messages[0].Role)
	}
	if messages[0].Content != "Hello" {
		t.Errorf("expected Content 'Hello', got %q", messages[0].Content)
	}
	if len(messages[0].Images) != 0 {
		t.Errorf("expected no images, got %d", len(messages[0].Images))
	}
}

func TestConvertInput_Array(t *testing.T) {
	input := json.RawMessage(`[
		{"role":"user","content":[{"type":"input_text","text":"Hello"}]},
		{"role":"user","content":[{"type":"input_text","text":"World"}]}
	]`)
	messages, meta, err := convertInput(input, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer meta.cleanup()

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Role != "user" {
		t.Errorf("expected messages[0].Role 'user', got %q", messages[0].Role)
	}
	if messages[0].Content != "Hello" {
		t.Errorf("expected messages[0].Content 'Hello', got %q", messages[0].Content)
	}
	if messages[1].Role != "user" {
		t.Errorf("expected messages[1].Role 'user', got %q", messages[1].Role)
	}
	if messages[1].Content != "World" {
		t.Errorf("expected messages[1].Content 'World', got %q", messages[1].Content)
	}
	if len(messages[0].Images) != 0 {
		t.Errorf("expected no images, got %d", len(messages[0].Images))
	}
}

// ---------------------------------------------------------------------------
// convertInstructions unit tests
// ---------------------------------------------------------------------------

func TestConvertInstructions_NoSystem(t *testing.T) {
	messages := []ollama.ChatMessage{{Role: "user", Content: "Hi"}}
	result := convertInstructions("Be helpful", messages)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("expected result[0].Role 'system', got %q", result[0].Role)
	}
	if result[0].Content != "Be helpful" {
		t.Errorf("expected result[0].Content 'Be helpful', got %q", result[0].Content)
	}
	if result[1].Role != "user" {
		t.Errorf("expected result[1].Role 'user', got %q", result[1].Role)
	}
	if result[1].Content != "Hi" {
		t.Errorf("expected result[1].Content 'Hi', got %q", result[1].Content)
	}
}

func TestConvertInstructions_Empty(t *testing.T) {
	messages := []ollama.ChatMessage{{Role: "user", Content: "Hi"}}
	result := convertInstructions("", messages)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("expected Role 'user', got %q", result[0].Role)
	}
	if result[0].Content != "Hi" {
		t.Errorf("expected Content 'Hi', got %q", result[0].Content)
	}
}

func TestConvertInstructions_WithExistingSystem(t *testing.T) {
	messages := []ollama.ChatMessage{
		{Role: "system", Content: "You are a bot."},
		{Role: "user", Content: "Hi"},
	}
	result := convertInstructions("Be concise", messages)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("expected result[0].Role 'system', got %q", result[0].Role)
	}
	if result[0].Content != "You are a bot.\nBe concise" {
		t.Errorf("expected result[0].Content 'You are a bot.\\nBe concise', got %q", result[0].Content)
	}
	if result[1].Role != "user" {
		t.Errorf("expected result[1].Role 'user', got %q", result[1].Role)
	}
}

// ---------------------------------------------------------------------------
// spoolBase64File unit tests
// ---------------------------------------------------------------------------

func TestSpoolBase64File_Valid(t *testing.T) {
	uploadDir := t.TempDir()
	// Minimal 1x1 transparent PNG as a data URL.
	dataURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYPgPAAEDAQAIicLsAAAAAElFTkSuQmCC"

	filePath, base64Data, err := spoolBase64File(dataURL, "test.png", uploadDir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filePath)

	if filePath == "" {
		t.Error("expected non-empty file path")
	}
	if !strings.HasSuffix(filePath, ".png") {
		t.Errorf("expected .png extension, got %q", filePath)
	}
	if base64Data != dataURL {
		t.Errorf("expected base64Data to match input data URL")
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("file was not created at %q", filePath)
	}
}

func TestSpoolBase64File_InvalidMIME(t *testing.T) {
	uploadDir := t.TempDir()
	dataURL := "data:application/pdf;base64,JVBERi0xLjcK"

	_, _, err := spoolBase64File(dataURL, "test.pdf", uploadDir)
	if err == nil {
		t.Fatal("expected error for unsupported MIME type")
	}
	if !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("expected 'not yet supported' error, got %q", err.Error())
	}
}

func TestSpoolBase64File_InvalidPrefix(t *testing.T) {
	uploadDir := t.TempDir()
	dataURL := "not-a-data-url"

	_, _, err := spoolBase64File(dataURL, "test.png", uploadDir)
	if err == nil {
		t.Fatal("expected error for missing data: prefix")
	}
	if !strings.Contains(err.Error(), "data:") {
		t.Errorf("expected error mentioning 'data:', got %q", err.Error())
	}
}

func TestSpoolBase64File_InvalidBase64(t *testing.T) {
	uploadDir := t.TempDir()
	dataURL := "data:image/png;base64,!!!invalid!!!base64!!!data!!!"

	_, _, err := spoolBase64File(dataURL, "test.png", uploadDir)
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if !strings.Contains(err.Error(), "base64") {
		t.Errorf("expected error mentioning 'base64', got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Responses handler backend error test
// ---------------------------------------------------------------------------

func TestResponses_HTTPError(t *testing.T) {
	var callCount atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			callCount.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
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

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"good-model","input":"Hi"}`
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
	if callCount.Load() != 1 {
		t.Errorf("expected 1 backend call, got %d", callCount.Load())
	}
}

func TestResponses_Streaming_QueueFull(t *testing.T) {
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
		func() []backend.BackendSnapshot { return nil },
	)

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"test-alias","input":"Hello","stream":true}`
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

	if !strings.Contains(string(respBody), "data:") {
		t.Error("expected SSE data event in body")
	}
	if !strings.Contains(string(respBody), `"code":"queue_full"`) {
		t.Errorf("expected queue_full error in SSE body, got: %s", string(respBody))
	}
}

// ---------------------------------------------------------------------------
// generateResponseID unit tests
// ---------------------------------------------------------------------------

func TestGenerateResponseID(t *testing.T) {
	id := generateResponseID()
	if !strings.HasPrefix(id, "resp_") {
		t.Errorf("expected ID to start with 'resp_', got %q", id)
	}
	// resp_ (5) + 16 hex chars = 21 chars total.
	if len(id) != 21 {
		t.Errorf("expected ID length 21, got %d: %q", len(id), id)
	}

	// Verify uniqueness.
	id2 := generateResponseID()
	if id == id2 {
		t.Error("expected unique IDs")
	}
}

// ---------------------------------------------------------------------------
// Input validation tests
// ---------------------------------------------------------------------------

func TestResponses_MissingModel(t *testing.T) {
	logger := newDiscardLogger()
	cfg := config.DefaultConfig()
	reg := backend.NewRegistry(&cfg, logger)

	handler := ResponsesHandler(logger, reg, nil, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"input":"Hello"}`
	resp, err := http.Post(handlerTS.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var errResp chatError
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("expected Type 'invalid_request_error', got %q", errResp.Error.Type)
	}
	if !strings.Contains(errResp.Error.Message, "model") {
		t.Errorf("expected message to mention 'model', got %q", errResp.Error.Message)
	}
}

// ---------------------------------------------------------------------------
// mapResponsesResponse unit tests
// ---------------------------------------------------------------------------

func TestMapResponsesResponse(t *testing.T) {
	ollamaResp := &ollama.ChatResponse{
		Model: "qwen:30b",
		Message: ollama.ChatMessage{
			Role:    "assistant",
			Content: "Test content.",
		},
		Done:            true,
		DoneReason:      "stop",
		PromptEvalCount: 7,
		EvalCount:       13,
	}

	result := mapResponsesResponse(ollamaResp, "llama3")
	if result.Object != "response" {
		t.Errorf("expected Object 'response', got %q", result.Object)
	}
	if result.Model != "llama3" {
		t.Errorf("expected Model 'llama3', got %q", result.Model)
	}
	if len(result.Output) != 1 {
		t.Fatalf("expected 1 output, got %d", len(result.Output))
	}
	if result.Output[0].Content[0].Text != "Test content." {
		t.Errorf("expected 'Test content.', got %q", result.Output[0].Content[0].Text)
	}
	if result.Usage.InputTokens != 7 {
		t.Errorf("expected InputTokens 7, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 13 {
		t.Errorf("expected OutputTokens 13, got %d", result.Usage.OutputTokens)
	}
	if result.Usage.TotalTokens != 20 {
		t.Errorf("expected TotalTokens 20, got %d", result.Usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// Responses handler streaming client disconnect
// ---------------------------------------------------------------------------

func TestResponses_Streaming_ClientDisconnect(t *testing.T) {
	var serverCancelled atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/chat") {
			w.Header().Set("Content-Type", "application/x-ndjson")
			// Send first chunk and flush.
			fmt.Fprintf(w, `{"model":"qwen:30b","message":{"role":"assistant","content":"Hello"},"done":false}`+"\n")
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
				Models: []ollama.TagsModel{{Name: "qwen:30b"}},
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

	handler := ResponsesHandler(logger, reg, sched, t.TempDir())
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"qwen:30b","input":"Hi","stream":true}`
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
