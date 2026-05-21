package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/httpapi"
	"llm-go-proxy/internal/logging"
)

func TestDebugModelsHandler_NilRegistry(t *testing.T) {
	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)

	handler := httpapi.DebugModelsHandler(logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/debug/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}

	aliases, ok := body["aliases"].([]interface{})
	if !ok {
		t.Fatalf("expected 'aliases' to be an array, got %T", body["aliases"])
	}
	if len(aliases) != 0 {
		t.Errorf("expected empty aliases, got %d", len(aliases))
	}

	nativeModels, ok := body["native_models"].([]interface{})
	if !ok {
		t.Fatalf("expected 'native_models' to be an array, got %T", body["native_models"])
	}
	if len(nativeModels) != 0 {
		t.Errorf("expected empty native_models, got %d", len(nativeModels))
	}

	loadedModels, ok := body["loaded_models"].([]interface{})
	if !ok {
		t.Fatalf("expected 'loaded_models' to be an array, got %T", body["loaded_models"])
	}
	if len(loadedModels) != 0 {
		t.Errorf("expected empty loaded_models, got %d", len(loadedModels))
	}
}

func TestDebugModelsHandler_ReturnsAliases(t *testing.T) {
	thinkFalse := false
	cfg := &config.Config{
		Models: config.ModelsConfig{
			Aliases: []config.AliasConfig{
				{
					Name:         "qwen-instruct",
					PrimaryModel: "qwen:30b",
					BackupModels: []string{"qwen:14b"},
					Overrides: config.AliasOverrides{
						Think:   &thinkFalse,
						Options: &config.OllamaOptions{Temperature: 0.2},
					},
				},
			},
		},
	}

	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)

	reg := backend.NewRegistry(cfg, logger)
	defer reg.Stop()
	handler := httpapi.DebugModelsHandler(logger, reg)

	req := httptest.NewRequest(http.MethodGet, "/debug/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Aliases []map[string]interface{} `json:"aliases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(body.Aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(body.Aliases))
	}

	alias := body.Aliases[0]
	if alias["name"] != "qwen-instruct" {
		t.Errorf("name = %v, want 'qwen-instruct'", alias["name"])
	}
	if alias["primary_model"] != "qwen:30b" {
		t.Errorf("primary_model = %v, want 'qwen:30b'", alias["primary_model"])
	}

	backupModels, ok := alias["backup_models"].([]interface{})
	if !ok {
		t.Fatal("expected backup_models to be an array")
	}
	if len(backupModels) != 1 || backupModels[0] != "qwen:14b" {
		t.Errorf("backup_models = %v, want ['qwen:14b']", backupModels)
	}

	overrides, ok := alias["overrides"].(map[string]interface{})
	if !ok {
		t.Fatal("expected overrides to be an object")
	}
	if overrides["think"] != false {
		t.Errorf("overrides.think = %v, want false", overrides["think"])
	}
	options, ok := overrides["options"].(map[string]interface{})
	if !ok {
		t.Fatal("expected options to be an object")
	}
	if options["temperature"] != 0.2 {
		t.Errorf("options.temperature = %v, want 0.2", options["temperature"])
	}
}

func TestDebugModelsHandler_RespectsExposeFlag(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"model-a:latest","size":100},{"name":"model-b:latest","size":200}]}`))
		} else {
			w.Write([]byte(`{"models":[]}`))
		}
	}))
	defer ts.Close()

	t.Run("expose true populates native_models", func(t *testing.T) {
		cfg := &config.Config{
			Models: config.ModelsConfig{
				ExposeNativeOllamaModels: true,
			},
			OllamaBackends: []config.OllamaBackendConfig{
				{ID: "b1", URL: ts.URL, Host: "h1", MaxConcurrentRequests: 5, Enabled: true},
			},
		}
		logger := logging.NewLogger(logging.LevelDebug, "test")
		logger.SetOutput(io.Discard, io.Discard)
		reg := backend.NewRegistry(cfg, logger)
		reg.Start()
		time.Sleep(200 * time.Millisecond)
		reg.Stop()

		handler := httpapi.DebugModelsHandler(logger, reg)
		req := httptest.NewRequest(http.MethodGet, "/debug/models", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		nativeModels := body["native_models"].([]interface{})
		if len(nativeModels) == 0 {
			t.Error("expected non-empty native_models when ExposeNativeOllamaModels is true")
		}
	})

	t.Run("expose false returns empty native_models", func(t *testing.T) {
		cfg := &config.Config{
			Models: config.ModelsConfig{
				ExposeNativeOllamaModels: false,
			},
			OllamaBackends: []config.OllamaBackendConfig{
				{ID: "b1", URL: ts.URL, Host: "h1", MaxConcurrentRequests: 5, Enabled: true},
			},
		}
		logger := logging.NewLogger(logging.LevelDebug, "test")
		logger.SetOutput(io.Discard, io.Discard)
		reg := backend.NewRegistry(cfg, logger)
		reg.Start()
		time.Sleep(200 * time.Millisecond)
		reg.Stop()

		handler := httpapi.DebugModelsHandler(logger, reg)
		req := httptest.NewRequest(http.MethodGet, "/debug/models", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		nativeModels := body["native_models"].([]interface{})
		if len(nativeModels) != 0 {
			t.Errorf("expected empty native_models, got %d elements", len(nativeModels))
		}
	})
}

func TestDebugModelsHandler_LoadedModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[]}`))
		} else {
			w.Write([]byte(`{"models":[{"name":"model-a:latest","model":"model-a","size":100},{"name":"model-b:latest","model":"model-b","size":200}]}`))
		}
	}))
	defer ts.Close()

	cfg := &config.Config{
		Models: config.ModelsConfig{
			ExposeNativeOllamaModels: false,
		},
		OllamaBackends: []config.OllamaBackendConfig{
			{ID: "b1", URL: ts.URL, Host: "h1", MaxConcurrentRequests: 5, Enabled: true},
		},
	}

	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)
	reg := backend.NewRegistry(cfg, logger)
	reg.Start()
	time.Sleep(200 * time.Millisecond)
	reg.Stop()

	handler := httpapi.DebugModelsHandler(logger, reg)
	req := httptest.NewRequest(http.MethodGet, "/debug/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	loadedModels := body["loaded_models"].([]interface{})
	if len(loadedModels) == 0 {
		t.Fatal("expected non-empty loaded_models")
	}

	// Check that models are sorted alphabetically.
	if loadedModels[0] != "model-a:latest" {
		t.Errorf("loaded_models[0] = %v, want 'model-a:latest'", loadedModels[0])
	}
	if loadedModels[1] != "model-b:latest" {
		t.Errorf("loaded_models[1] = %v, want 'model-b:latest'", loadedModels[1])
	}
}
