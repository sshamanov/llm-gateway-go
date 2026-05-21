package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/ollama"
)

// newDiscardLogger creates a logger that discards all output, for use in tests.
func newDiscardLogger() *logging.Logger {
	l := logging.NewLogger(logging.LevelDebug, "test")
	l.SetOutput(io.Discard, io.Discard)
	return l
}

// fakeOllamaServer creates an httptest.Server that responds to /api/tags and /api/ps
// with the given tags models and an empty ps response.
func fakeOllamaServer(tagsModels []ollama.TagsModel) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{Models: tagsModels})
			return
		}
		// /api/ps returns empty — just mark the backend healthy.
		json.NewEncoder(w).Encode(ollama.PSResponse{Models: []ollama.PSModel{}})
	}))
}

func TestModelsHandler_ReturnsAliases(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{
			{Name: "alias-a", PrimaryModel: "model-a"},
			{Name: "alias-b", PrimaryModel: "model-b"},
		},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

	handler := ModelsHandler(logger, reg)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if len(result.Data) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result.Data))
	}

	// First alias.
	if result.Data[0].ID != "alias-a" {
		t.Errorf("expected first model ID 'alias-a', got %q", result.Data[0].ID)
	}
	if result.Data[0].Object != "model" {
		t.Errorf("expected Object 'model', got %q", result.Data[0].Object)
	}
	if result.Data[0].Created != 0 {
		t.Errorf("expected Created 0 for alias, got %d", result.Data[0].Created)
	}
	if result.Data[0].OwnedBy != "proxy" {
		t.Errorf("expected OwnedBy 'proxy', got %q", result.Data[0].OwnedBy)
	}

	// Second alias.
	if result.Data[1].ID != "alias-b" {
		t.Errorf("expected second model ID 'alias-b', got %q", result.Data[1].ID)
	}
	if result.Data[1].Created != 0 {
		t.Errorf("expected Created 0 for alias, got %d", result.Data[1].Created)
	}
	if result.Data[1].OwnedBy != "proxy" {
		t.Errorf("expected OwnedBy 'proxy', got %q", result.Data[1].OwnedBy)
	}
}

func TestModelsHandler_NativeModelsExposed(t *testing.T) {
	fake := fakeOllamaServer([]ollama.TagsModel{
		{Name: "zebra:latest", Size: 100},
		{Name: "alpha:latest", Size: 200},
	})
	defer fake.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: true,
		Aliases: []config.AliasConfig{
			{Name: "my-alias", PrimaryModel: "model-a"},
		},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: fake.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	// Wait for initial poll to complete.
	time.Sleep(200 * time.Millisecond)

	handler := ModelsHandler(logger, reg)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if len(result.Data) != 3 {
		t.Fatalf("expected 3 models (1 alias + 2 native), got %d", len(result.Data))
	}

	// First should be the alias.
	if result.Data[0].ID != "my-alias" {
		t.Errorf("expected first model 'my-alias', got %q", result.Data[0].ID)
	}
	if result.Data[0].Created != 0 {
		t.Errorf("expected Created 0 for alias, got %d", result.Data[0].Created)
	}
	if result.Data[0].OwnedBy != "proxy" {
		t.Errorf("expected OwnedBy 'proxy', got %q", result.Data[0].OwnedBy)
	}

	// Native models should appear sorted alphabetically.
	if result.Data[1].ID != "alpha:latest" {
		t.Errorf("expected first native model 'alpha:latest', got %q", result.Data[1].ID)
	}
	if result.Data[1].OwnedBy != "ollama" {
		t.Errorf("expected native model owned_by 'ollama', got %q", result.Data[1].OwnedBy)
	}
	if result.Data[1].Created <= 0 {
		t.Errorf("expected positive Created for native model, got %d", result.Data[1].Created)
	}

	if result.Data[2].ID != "zebra:latest" {
		t.Errorf("expected second native model 'zebra:latest', got %q", result.Data[2].ID)
	}
	if result.Data[2].OwnedBy != "ollama" {
		t.Errorf("expected native model owned_by 'ollama', got %q", result.Data[2].OwnedBy)
	}
	if result.Data[2].Created <= 0 {
		t.Errorf("expected positive Created for native model, got %d", result.Data[2].Created)
	}
}

func TestModelsHandler_NativeModelsNotExposed(t *testing.T) {
	fake := fakeOllamaServer([]ollama.TagsModel{
		{Name: "hidden-model:latest", Size: 100},
	})
	defer fake.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{
			{Name: "only-alias", PrimaryModel: "model-a"},
		},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: fake.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	handler := ModelsHandler(logger, reg)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if len(result.Data) != 1 {
		t.Fatalf("expected 1 model (alias only), got %d: %+v", len(result.Data), result.Data)
	}

	if result.Data[0].ID != "only-alias" {
		t.Errorf("expected model ID 'only-alias', got %q", result.Data[0].ID)
	}
	if result.Data[0].OwnedBy != "proxy" {
		t.Errorf("expected OwnedBy 'proxy', got %q", result.Data[0].OwnedBy)
	}
}

func TestModelsHandler_Deduplication(t *testing.T) {
	fake := fakeOllamaServer([]ollama.TagsModel{
		{Name: "shared-name", Size: 100},
		{Name: "native-only", Size: 200},
	})
	defer fake.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: true,
		Aliases: []config.AliasConfig{
			{Name: "shared-name", PrimaryModel: "model-a"},
			{Name: "alias-only", PrimaryModel: "model-b"},
		},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: fake.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	handler := ModelsHandler(logger, reg)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Expected: 2 aliases + 1 native-only (shared-name deduped from native).
	if len(result.Data) != 3 {
		t.Fatalf("expected 3 models, got %d: %+v", len(result.Data), result.Data)
	}

	// shared-name should be the alias version, not the native one.
	for _, m := range result.Data {
		if m.ID == "shared-name" {
			if m.Created != 0 {
				t.Errorf("expected Created 0 for alias 'shared-name', got %d", m.Created)
			}
			if m.OwnedBy != "proxy" {
				t.Errorf("expected OwnedBy 'proxy' for alias 'shared-name', got %q", m.OwnedBy)
			}
		}
		if m.ID == "native-only" {
			if m.OwnedBy != "ollama" {
				t.Errorf("expected OwnedBy 'ollama' for native model 'native-only', got %q", m.OwnedBy)
			}
			if m.Created <= 0 {
				t.Errorf("expected positive Created for native model, got %d", m.Created)
			}
		}
	}
}

func TestModelsHandler_Ordering(t *testing.T) {
	fake := fakeOllamaServer([]ollama.TagsModel{
		{Name: "z-model", Size: 100},
		{Name: "a-model", Size: 200},
	})
	defer fake.Close()

	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: true,
		Aliases: []config.AliasConfig{
			{Name: "beta-alias", PrimaryModel: "model-b"},
			{Name: "alpha-alias", PrimaryModel: "model-a"},
		},
	}
	cfg.OllamaBackends = []config.OllamaBackendConfig{
		{ID: "test-backend", URL: fake.URL, Host: "default", Enabled: true, MaxConcurrentRequests: 1},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)
	reg.Start()
	defer reg.Stop()

	time.Sleep(200 * time.Millisecond)

	handler := ModelsHandler(logger, reg)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	expected := []string{"beta-alias", "alpha-alias", "a-model", "z-model"}
	if len(result.Data) != len(expected) {
		t.Fatalf("expected %d models, got %d: %+v", len(expected), len(result.Data), result.Data)
	}
	for i, exp := range expected {
		if result.Data[i].ID != exp {
			t.Errorf("position %d: expected %q, got %q", i, exp, result.Data[i].ID)
		}
	}
}

func TestModelsHandler_NilRegistry(t *testing.T) {
	logger := newDiscardLogger()
	handler := ModelsHandler(logger, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if len(result.Data) != 0 {
		t.Errorf("expected empty data for nil registry, got %d models", len(result.Data))
	}
}

func TestModelsHandler_ObjectField(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{
		ExposeNativeOllamaModels: false,
		Aliases: []config.AliasConfig{
			{Name: "test-model", PrimaryModel: "model-a"},
		},
	}

	logger := newDiscardLogger()
	reg := backend.NewRegistry(&cfg, logger)

	handler := ModelsHandler(logger, reg)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Object != "list" {
		t.Errorf("expected object \"list\", got %q", result.Object)
	}
}
