package backend

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/ollama"
)

func TestNewRegistry_CreatesBackends(t *testing.T) {
	cfg := &config.Config{
		OllamaBackends: []config.OllamaBackendConfig{
			{ID: "b1", URL: "http://localhost:1", Host: "h1", MaxConcurrentRequests: 5, Enabled: true},
			{ID: "b2", URL: "http://localhost:2", Host: "h2", MaxConcurrentRequests: 3, Enabled: true},
			{ID: "b3", URL: "http://localhost:3", Host: "h3", MaxConcurrentRequests: 4, Enabled: false},
		},
	}

	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)

	reg := NewRegistry(cfg, logger)
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}

	snapshots := reg.BackendSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 enabled backends, got %d", len(snapshots))
	}

	ids := make(map[string]bool)
	for _, s := range snapshots {
		ids[s.ID] = true
		if !s.Enabled {
			t.Errorf("expected backend %s to be enabled", s.ID)
		}
	}
	if !ids["b1"] || !ids["b2"] {
		t.Errorf("expected backends b1 and b2, got ids %v", ids)
	}
}

func TestBackendSnapshot_IsIndependent(t *testing.T) {
	bs := &BackendState{
		Config: config.OllamaBackendConfig{
			ID: "test", URL: "http://localhost:1", Host: "h1", Enabled: true,
		},
		AvailableModels: []string{"model-a"},
		LoadedModels:    []string{"model-a"},
	}
	bs.Health.SetHealthy(time.Now())

	snap := bs.Snapshot()

	// Modify the backend state directly.
	bs.mu.Lock()
	bs.AvailableModels = []string{"model-b"}
	bs.LoadedModels = []string{"model-b"}
	bs.mu.Unlock()
	bs.Health.SetUnhealthy(fmt.Errorf("test error"))

	// Verify snapshot is unchanged.
	if !slices.Equal(snap.AvailableModels, []string{"model-a"}) {
		t.Errorf("snapshot AvailableModels changed: got %v", snap.AvailableModels)
	}
	if !slices.Equal(snap.LoadedModels, []string{"model-a"}) {
		t.Errorf("snapshot LoadedModels changed: got %v", snap.LoadedModels)
	}
	if !snap.Healthy {
		t.Errorf("snapshot Healthy should be true, got false")
	}
	if snap.LastError != "" {
		t.Errorf("snapshot LastError should be empty, got %q", snap.LastError)
	}
	if snap.ID != "test" {
		t.Errorf("expected snap.ID 'test', got %q", snap.ID)
	}
}

func TestHealthState_SetHealthyAndUnhealthy(t *testing.T) {
	var hs HealthState
	now := time.Now().Truncate(time.Second)

	hs.SetHealthy(now)
	snap := hs.Snapshot()

	if !snap.Healthy {
		t.Error("expected healthy after SetHealthy")
	}
	if snap.LastError != "" {
		t.Errorf("expected empty LastError, got %q", snap.LastError)
	}
	if !snap.LastContact.Equal(now) {
		t.Errorf("expected LastContact %v, got %v", now, snap.LastContact)
	}

	// Set unhealthy.
	hs.SetUnhealthy(fmt.Errorf("connection refused"))
	snap = hs.Snapshot()

	if snap.Healthy {
		t.Error("expected unhealthy after SetUnhealthy")
	}
	if snap.LastError != "connection refused" {
		t.Errorf("expected LastError 'connection refused', got %q", snap.LastError)
	}

	// Recover.
	later := now.Add(time.Minute)
	hs.SetHealthy(later)
	snap = hs.Snapshot()

	if !snap.Healthy {
		t.Error("expected healthy after second SetHealthy")
	}
	if snap.LastError != "" {
		t.Errorf("expected empty LastError after recovery, got %q", snap.LastError)
	}
	if !snap.LastContact.Equal(later) {
		t.Errorf("expected LastContact %v, got %v", later, snap.LastContact)
	}
}

func TestModelNamesFromTags(t *testing.T) {
	tags := &ollama.TagsResponse{
		Models: []ollama.TagsModel{
			{Name: "z-model:latest", Size: 100},
			{Name: "a-model:latest", Size: 200},
			{Name: "z-model:latest", Size: 300}, // duplicate by name
		},
	}
	names := ModelNamesFromTags(tags)
	expected := []string{"a-model:latest", "z-model:latest"}
	if !slices.Equal(names, expected) {
		t.Errorf("expected %v, got %v", expected, names)
	}

	// Nil input returns nil.
	if names := ModelNamesFromTags(nil); names != nil {
		t.Errorf("expected nil for nil tags, got %v", names)
	}

	// Empty models returns empty slice.
	empty := ModelNamesFromTags(&ollama.TagsResponse{})
	if empty == nil || len(empty) != 0 {
		t.Errorf("expected empty slice for empty tags, got %v", empty)
	}
}

func TestModelNamesFromPS(t *testing.T) {
	ps := &ollama.PSResponse{
		Models: []ollama.PSModel{
			{Name: "model-b:latest", Model: "model-b-v1", Size: 100},
			{Name: "model-a:latest", Model: "model-a-v2", Size: 200},
			{Name: "model-b:latest", Model: "model-b-v3", Size: 300}, // duplicate by Name
		},
	}
	names := ModelNamesFromPS(ps)
	expected := []string{"model-a:latest", "model-b:latest"}
	if !slices.Equal(names, expected) {
		t.Errorf("expected %v, got %v", expected, names)
	}

	// Nil input returns nil.
	if names := ModelNamesFromPS(nil); names != nil {
		t.Errorf("expected nil for nil PS, got %v", names)
	}

	// Empty models returns empty slice.
	empty := ModelNamesFromPS(&ollama.PSResponse{})
	if empty == nil || len(empty) != 0 {
		t.Errorf("expected empty slice for empty PS, got %v", empty)
	}
}

func TestRegistry_PollCycle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/tags") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ollama.TagsResponse{
				Models: []ollama.TagsModel{
					{Name: "model-a:latest", Size: 100},
					{Name: "model-b:latest", Size: 200},
				},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/ps") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ollama.PSResponse{
				Models: []ollama.PSModel{
					{Name: "model-a:latest", Model: "model-a", Size: 100},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	cfg := &config.Config{
		OllamaBackends: []config.OllamaBackendConfig{
			{ID: "test-backend", URL: ts.URL, Host: "local", Enabled: true},
		},
	}

	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)

	reg := NewRegistry(cfg, logger)
	reg.Start()
	defer reg.Stop()

	// Wait for the initial poll to complete.
	time.Sleep(200 * time.Millisecond)

	snapshots := reg.BackendSnapshots()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}

	snap := snapshots[0]
	if !slices.Equal(snap.AvailableModels, []string{"model-a:latest", "model-b:latest"}) {
		t.Errorf("expected AvailableModels [model-a:latest model-b:latest], got %v", snap.AvailableModels)
	}
	if !slices.Equal(snap.LoadedModels, []string{"model-a:latest"}) {
		t.Errorf("expected LoadedModels [model-a:latest], got %v", snap.LoadedModels)
	}
	if !snap.Healthy {
		t.Errorf("expected backend to be healthy after successful poll")
	}
}

func TestRegistry_Stop(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "/api/tags") {
			json.NewEncoder(w).Encode(ollama.TagsResponse{})
		} else {
			json.NewEncoder(w).Encode(ollama.PSResponse{})
		}
	}))
	defer ts.Close()

	cfg := &config.Config{
		OllamaBackends: []config.OllamaBackendConfig{
			{ID: "b1", URL: ts.URL, Host: "local", Enabled: true},
			{ID: "b2", URL: ts.URL, Host: "local", Enabled: true},
		},
	}

	logger := logging.NewLogger(logging.LevelDebug, "test")
	logger.SetOutput(io.Discard, io.Discard)

	reg := NewRegistry(cfg, logger)
	reg.Start()

	done := make(chan struct{})
	go func() {
		reg.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Stop completed — all goroutines exited.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not complete within 2s timeout — possible goroutine leak")
	}
}

func TestRegistry_NilSafe(t *testing.T) {
	var reg *Registry

	snapshots := reg.BackendSnapshots()
	if snapshots != nil {
		t.Errorf("expected nil snapshots from nil registry, got %v", snapshots)
	}

	mc := reg.ModelsConfig()
	if mc.ExposeNativeOllamaModels || mc.Aliases != nil {
		t.Errorf("expected zero ModelsConfig from nil registry, got %+v", mc)
	}
}
