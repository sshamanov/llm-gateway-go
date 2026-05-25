package backend

import (
	"io"
	"slices"
	"testing"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
)

// testLogger is a helper that creates a discard logger for tests.
func testLogger() *logging.Logger {
	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)
	return logger
}

func TestResolve_AliasMatch(t *testing.T) {
	reg := &Registry{
		backends: map[string]*BackendState{
			"b1": {
				Config: config.OllamaBackendConfig{ID: "b1", Enabled: true},
			},
		},
		modelsConfig: config.ModelsConfig{
			Aliases: []config.AliasConfig{
				{
					Name:         "my-alias",
					PrimaryModel: "qwen:30b",
					BackupModels: []string{"qwen:14b", "qwen:72b"},
				},
			},
		},
		logger: testLogger(),
	}

	result, err := reg.Resolve("my-alias")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"qwen:30b", "qwen:14b", "qwen:72b"}
	if !slices.Equal(result.Candidates, expected) {
		t.Errorf("expected candidates %v, got %v", expected, result.Candidates)
	}
}

func TestResolve_AliasHasCorrectKind(t *testing.T) {
	reg := &Registry{
		backends: map[string]*BackendState{
			"b1": {
				Config: config.OllamaBackendConfig{ID: "b1", Enabled: true},
			},
		},
		modelsConfig: config.ModelsConfig{
			Aliases: []config.AliasConfig{
				{
					Name:         "my-alias",
					PrimaryModel: "qwen:30b",
				},
			},
		},
		logger: testLogger(),
	}

	result, err := reg.Resolve("my-alias")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != ResolutionAlias {
		t.Errorf("expected ResolutionAlias, got %v", result.Kind)
	}
	if result.RequestedID != "my-alias" {
		t.Errorf("expected RequestedID 'my-alias', got %q", result.RequestedID)
	}
	if result.AliasConfig == nil {
		t.Fatal("expected non-nil AliasConfig for alias resolution")
	}
	if result.AliasConfig.Name != "my-alias" {
		t.Errorf("expected AliasConfig.Name 'my-alias', got %q", result.AliasConfig.Name)
	}
}

func TestResolve_NativeModel_Found(t *testing.T) {
	reg := &Registry{
		backends: map[string]*BackendState{
			"b1": {
				Config:          config.OllamaBackendConfig{ID: "b1", Enabled: true},
				AvailableModels: []string{"qwen:30b", "llama3:latest"},
			},
		},
		modelsConfig: config.ModelsConfig{
			ExposeNativeOllamaModels: true,
		},
		logger: testLogger(),
	}

	result, err := reg.Resolve("qwen:30b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != ResolutionNative {
		t.Errorf("expected ResolutionNative, got %v", result.Kind)
	}
	if result.RequestedID != "qwen:30b" {
		t.Errorf("expected RequestedID 'qwen:30b', got %q", result.RequestedID)
	}
	if !slices.Equal(result.Candidates, []string{"qwen:30b"}) {
		t.Errorf("expected candidates [qwen:30b], got %v", result.Candidates)
	}
	if result.AliasConfig != nil {
		t.Errorf("expected nil AliasConfig for native resolution, got %+v", result.AliasConfig)
	}
}

func TestResolve_NativeModel_NotExposed(t *testing.T) {
	reg := &Registry{
		backends: map[string]*BackendState{
			"b1": {
				Config:          config.OllamaBackendConfig{ID: "b1", Enabled: true},
				AvailableModels: []string{"qwen:30b"},
			},
		},
		modelsConfig: config.ModelsConfig{
			ExposeNativeOllamaModels: false,
		},
		logger: testLogger(),
	}

	_, err := reg.Resolve("qwen:30b")
	if err == nil {
		t.Fatal("expected error when native models not exposed")
	}
	if err.Error() != "unknown model: qwen:30b" {
		t.Errorf("expected 'unknown model: qwen:30b', got %q", err.Error())
	}
}

func TestResolve_NativeModel_NotFoundOnBackends(t *testing.T) {
	reg := &Registry{
		backends: map[string]*BackendState{
			"b1": {
				Config:          config.OllamaBackendConfig{ID: "b1", Enabled: true},
				AvailableModels: []string{"llama3:latest"},
			},
		},
		modelsConfig: config.ModelsConfig{
			ExposeNativeOllamaModels: true,
		},
		logger: testLogger(),
	}

	_, err := reg.Resolve("qwen:30b")
	if err == nil {
		t.Fatal("expected error when model not on any backend")
	}
	if err.Error() != "unknown model: qwen:30b" {
		t.Errorf("expected 'unknown model: qwen:30b', got %q", err.Error())
	}
}

func TestResolve_UnknownModel(t *testing.T) {
	reg := &Registry{
		backends: map[string]*BackendState{
			"b1": {
				Config:          config.OllamaBackendConfig{ID: "b1", Enabled: true},
				AvailableModels: []string{"llama3:latest"},
			},
		},
		modelsConfig: config.ModelsConfig{
			ExposeNativeOllamaModels: false,
		},
		logger: testLogger(),
	}

	_, err := reg.Resolve("nonexistent-model")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
	if err.Error() != "unknown model: nonexistent-model" {
		t.Errorf("expected 'unknown model: nonexistent-model', got %q", err.Error())
	}
}

func TestResolve_NilRegistry(t *testing.T) {
	var reg *Registry

	result, err := reg.Resolve("any-model")
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
	if result != nil {
		t.Errorf("expected nil result from nil registry, got %+v", result)
	}
}

func TestFindFirstBackend_Found(t *testing.T) {
	snapshots := []BackendSnapshot{
		{
			ID:              "b1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"qwen:30b", "llama3:latest"},
		},
		{
			ID:              "b2",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"qwen:14b"},
		},
	}

	result, err := FindFirstBackend(snapshots, "qwen:30b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "b1" {
		t.Errorf("expected b1, got %s", result.ID)
	}
}

func TestFindFirstBackend_NotFound(t *testing.T) {
	snapshots := []BackendSnapshot{
		{
			ID:              "b1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"llama3:latest"},
		},
	}

	_, err := FindFirstBackend(snapshots, "qwen:30b")
	if err == nil {
		t.Fatal("expected error when model not found")
	}
}

func TestFindFirstBackend_SkipsDisabled(t *testing.T) {
	snapshots := []BackendSnapshot{
		{
			ID:              "b1",
			Enabled:         false,
			Healthy:         true,
			AvailableModels: []string{"qwen:30b"},
		},
		{
			ID:              "b2",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"qwen:30b"},
		},
	}

	result, err := FindFirstBackend(snapshots, "qwen:30b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "b2" {
		t.Errorf("expected b2 (first enabled+healthy), got %s", result.ID)
	}
}

func TestFindFirstBackend_SkipsUnhealthy(t *testing.T) {
	snapshots := []BackendSnapshot{
		{
			ID:              "b1",
			Enabled:         true,
			Healthy:         false,
			AvailableModels: []string{"qwen:30b"},
		},
		{
			ID:              "b2",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"qwen:30b"},
		},
	}

	result, err := FindFirstBackend(snapshots, "qwen:30b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "b2" {
		t.Errorf("expected b2 (first healthy), got %s", result.ID)
	}
}

func TestFindFirstBackend_EmptySnapshots(t *testing.T) {
	// Nil slice.
	_, err := FindFirstBackend(nil, "qwen:30b")
	if err == nil {
		t.Fatal("expected error for nil snapshots")
	}

	// Empty slice.
	_, err = FindFirstBackend([]BackendSnapshot{}, "qwen:30b")
	if err == nil {
		t.Fatal("expected error for empty snapshots")
	}
}

func TestOllamaDefaultsAccessor(t *testing.T) {
	// Nil registry returns zero value.
	var reg *Registry
	defaults := reg.OllamaDefaults()
	if defaults.KeepAlive != "" || defaults.Think != nil {
		t.Errorf("expected zero value from nil registry, got %+v", defaults)
	}

	// Non-nil registry returns stored value.
	thinkTrue := true
	reg = &Registry{
		ollamaDefaults: config.OllamaDefaultsConfig{
			KeepAlive: "5m",
			Think:     &thinkTrue,
		},
	}
	defaults = reg.OllamaDefaults()
	if defaults.KeepAlive != "5m" {
		t.Errorf("expected KeepAlive '5m', got %q", defaults.KeepAlive)
	}
	if defaults.Think == nil || !*defaults.Think {
		t.Errorf("expected Think true")
	}
}

func TestPolicyAccessor(t *testing.T) {
	// Nil registry returns zero value.
	var reg *Registry
	policy := reg.Policy()
	if policy.IgnoreUnsupportedFields {
		t.Errorf("expected zero value from nil registry, got %+v", policy)
	}

	// Non-nil registry returns stored value.
	reg = &Registry{
		policy: config.PolicyConfig{
			IgnoreUnsupportedFields:    true,
			LogUnsupportedFields:       true,
			AllowClientOverrideOptions: true,
		},
	}
	policy = reg.Policy()
	if !policy.IgnoreUnsupportedFields {
		t.Errorf("expected IgnoreUnsupportedFields true")
	}
	if !policy.LogUnsupportedFields {
		t.Errorf("expected LogUnsupportedFields true")
	}
	if !policy.AllowClientOverrideOptions {
		t.Errorf("expected AllowClientOverrideOptions true")
	}
}
