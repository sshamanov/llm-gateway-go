package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/ollama"
)

// ---------------------------------------------------------------------------
// Handler integration tests
// ---------------------------------------------------------------------------

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

	handler := ChatCompletionsHandler(logger, reg)
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

	handler := ChatCompletionsHandler(logger, reg)
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

	handler := ChatCompletionsHandler(logger, reg)
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

func TestChatCompletions_NoBackend(t *testing.T) {
	// Fake server that has models that don't match the alias candidate.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{{Name: "unrelated-model"}},
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
			Name:         "test-alias",
			PrimaryModel: "nonexistent-model",
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

	handler := ChatCompletionsHandler(logger, reg)
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
	if errResp.Error.Code != "backend_unavailable" {
		t.Errorf("expected Code 'backend_unavailable', got %q", errResp.Error.Code)
	}
}

func TestChatCompletions_StreamRejected(t *testing.T) {
	logger := newDiscardLogger()
	handler := ChatCompletionsHandler(logger, nil)
	handlerTS := httptest.NewServer(handler)
	defer handlerTS.Close()

	body := `{"model":"any-model","messages":[{"role":"user","content":"Hi"}],"stream":true}`
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

	handler := ChatCompletionsHandler(logger, reg)
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

// ---------------------------------------------------------------------------
// buildOllamaRequest unit tests
// ---------------------------------------------------------------------------

func TestBuildOllamaRequest_DefaultsOnly(t *testing.T) {
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     false,
		Options: config.OllamaOptions{
			NumThread:   4,
			NumCtx:      8192,
			Temperature: 0.7,
			TopP:        0.9,
		},
	}

	req := &chatCompletionRequest{
		Model:    "test-model",
		Messages: []chatRequestMessage{{Role: "user", Content: "Hello"}},
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
	if result.Options == nil {
		t.Fatal("expected non-nil Options")
	}
	if result.Options.NumThread != 4 {
		t.Errorf("expected NumThread 4, got %d", result.Options.NumThread)
	}
	if result.Options.NumCtx != 8192 {
		t.Errorf("expected NumCtx 8192, got %d", result.Options.NumCtx)
	}
	if result.Options.Temperature != 0.7 {
		t.Errorf("expected Temperature 0.7, got %f", result.Options.Temperature)
	}
	if result.Options.TopP != 0.9 {
		t.Errorf("expected TopP 0.9, got %f", result.Options.TopP)
	}
	if result.Options.NumPredict != 0 {
		t.Errorf("expected NumPredict 0 (unset), got %d", result.Options.NumPredict)
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
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     false,
		Options: config.OllamaOptions{
			NumThread: 4, NumCtx: 8192, Temperature: 0.7, TopP: 0.9,
		},
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
	// Other defaults should remain unchanged.
	if result.Options.NumThread != 4 {
		t.Errorf("expected NumThread 4 (kept from defaults), got %d", result.Options.NumThread)
	}
	if result.KeepAlive != "5m" {
		t.Errorf("expected KeepAlive '5m', got %q", result.KeepAlive)
	}
}

func TestBuildOllamaRequest_AliasOptionsMerge(t *testing.T) {
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     false,
		Options: config.OllamaOptions{
			NumThread:   4,
			NumCtx:      8192,
			Temperature: 0.7,
			TopP:        0.9,
		},
	}

	alias := &config.AliasConfig{
		Name:         "test-alias",
		PrimaryModel: "test-model",
		Overrides: config.AliasOverrides{
			Options: &config.OllamaOptions{
				Temperature: 0.1, // override
				// NumThread is 0, should keep default
			},
		},
	}

	req := &chatCompletionRequest{Model: "test-model"}
	messages := []ollama.ChatMessage{{Role: "user", Content: "Hi"}}

	result := buildOllamaRequest("test-model", messages, defaults, alias, req, config.PolicyConfig{})

	// Zero-value alias options should not override defaults.
	if result.Options.NumThread != 4 {
		t.Errorf("expected NumThread 4 (kept from defaults), got %d", result.Options.NumThread)
	}
	// Non-zero alias options should override.
	if result.Options.Temperature != 0.1 {
		t.Errorf("expected Temperature 0.1 (from alias override), got %f", result.Options.Temperature)
	}
	// Unchanged defaults.
	if result.Options.NumCtx != 8192 {
		t.Errorf("expected NumCtx 8192 (kept from defaults), got %d", result.Options.NumCtx)
	}
	if result.Options.TopP != 0.9 {
		t.Errorf("expected TopP 0.9 (kept from defaults), got %f", result.Options.TopP)
	}
}

func TestBuildOllamaRequest_ClientOverridesAllowed(t *testing.T) {
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     false,
		Options: config.OllamaOptions{
			NumThread:   4,
			NumCtx:      8192,
			Temperature: 0.7,
			TopP:        0.9,
		},
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

	if result.Options.NumPredict != 100 {
		t.Errorf("expected NumPredict 100, got %d", result.Options.NumPredict)
	}
	if result.Options.Temperature != 0.5 {
		t.Errorf("expected Temperature 0.5 (client override), got %f", result.Options.Temperature)
	}
	if result.Options.TopP != 0.8 {
		t.Errorf("expected TopP 0.8 (client override), got %f", result.Options.TopP)
	}
	// Other defaults should remain.
	if result.Options.NumThread != 4 {
		t.Errorf("expected NumThread 4 (kept from defaults), got %d", result.Options.NumThread)
	}
	if result.Options.NumCtx != 8192 {
		t.Errorf("expected NumCtx 8192 (kept from defaults), got %d", result.Options.NumCtx)
	}
}

func TestBuildOllamaRequest_ClientOverridesDisallowed(t *testing.T) {
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     false,
		Options: config.OllamaOptions{
			NumThread:   4,
			NumCtx:      8192,
			Temperature: 0.7,
			TopP:        0.9,
		},
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

	// Client overrides should be ignored.
	if result.Options.NumPredict != 0 {
		t.Errorf("expected NumPredict 0 (no override), got %d", result.Options.NumPredict)
	}
	if result.Options.Temperature != 0.7 {
		t.Errorf("expected Temperature 0.7 (default preserved), got %f", result.Options.Temperature)
	}
}

func TestBuildOllamaRequest_FullStack(t *testing.T) {
	// Test all three layers: defaults → alias → client overrides.
	defaults := config.OllamaDefaultsConfig{
		KeepAlive: "5m",
		Think:     false,
		Options: config.OllamaOptions{
			NumThread:   4,
			NumCtx:      8192,
			Temperature: 0.7,
			TopP:        0.9,
		},
	}

	thinkTrue := true
	alias := &config.AliasConfig{
		Name:         "test-alias",
		PrimaryModel: "test-model",
		Overrides: config.AliasOverrides{
			Think: &thinkTrue,
			Options: &config.OllamaOptions{
				Temperature: 0.1, // alias overrides temperature
				// NumThread left at 0, should keep default
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

	// Default KeepAlive.
	if result.KeepAlive != "5m" {
		t.Errorf("expected KeepAlive '5m', got %q", result.KeepAlive)
	}

	// Alias think override.
	if result.Think == nil {
		t.Fatal("expected non-nil Think")
	}
	if !*result.Think {
		t.Error("expected Think to be true (alias override)")
	}

	// Default NumThread kept (alias did not override, client did not override).
	if result.Options.NumThread != 4 {
		t.Errorf("expected NumThread 4, got %d", result.Options.NumThread)
	}

	// Temperature: defaults(0.7) → alias(0.1) → client(0.5) = 0.5 (client wins).
	if result.Options.Temperature != 0.5 {
		t.Errorf("expected Temperature 0.5 (client overrides alias), got %f", result.Options.Temperature)
	}

	// TopP: defaults(0.9) → client(0.8) = 0.8.
	if result.Options.TopP != 0.8 {
		t.Errorf("expected TopP 0.8, got %f", result.Options.TopP)
	}

	// MaxTokens → NumPredict.
	if result.Options.NumPredict != 200 {
		t.Errorf("expected NumPredict 200, got %d", result.Options.NumPredict)
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

