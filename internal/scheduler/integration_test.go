package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/ollama"
)

// ---------------------------------------------------------------------------
// Fake Ollama streaming helpers
// ---------------------------------------------------------------------------

// streamChunk returns a raw Ollama ChatResponse JSON line (newline terminated).
// The Ollama streaming format is one JSON object per line.
func streamChunk(model, content string, done bool) string {
	chunk := ollama.ChatResponse{
		Model:     model,
		Message:   ollama.ChatMessage{Role: "assistant", Content: content},
		Done:      done,
		EvalCount: 5,
	}
	b, _ := json.Marshal(chunk)
	return string(b) + "\n"
}

// fakeStreamHandler returns a handler that writes ChatResponse JSON lines.
// If statusCode != 200, it returns the error status immediately (no body).
func fakeStreamHandler(t *testing.T, statusCode int, chunks []string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		for _, c := range chunks {
			_, _ = w.Write([]byte(c))
			flusher.Flush()
		}
	}
}

// fakeNonStreamHandler returns a handler that writes a single ChatResponse JSON.
func fakeNonStreamHandler(t *testing.T, model, content string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		resp := ollama.ChatResponse{
			Model:   model,
			Message: ollama.ChatMessage{Role: "assistant", Content: content},
			Done:    true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// newSingleBackendScheduler creates a scheduler with one backend pointing at server.
func newSingleBackendScheduler(t *testing.T, server *httptest.Server, cfg config.SchedulerConfig) *Scheduler {
	t.Helper()
	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "h1", MaxActiveJobs: 2},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "be", Host: "h1", MaxConcurrentRequests: 2, Enabled: true},
	})
	stats := NewStatsTracker()
	scorer := NewScorer(stats, hosts, backends, cfg)

	snapshots := func() []backend.BackendSnapshot {
		return []backend.BackendSnapshot{{
			ID:              "be",
			URL:             server.URL,
			Host:            "h1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"llama3"},
			LoadedModels:    []string{"llama3"},
		}}
	}

	return NewScheduler(NewQueue(cfg.AgingPerSecond), scorer, stats, http.DefaultClient,
		map[string]string{"be": server.URL}, nil, snapshots)
}

// newDualBackendScheduler creates a scheduler with two backends on separate servers.
func newDualBackendScheduler(t *testing.T, s1, s2 *httptest.Server, hostCapacity int) *Scheduler {
	t.Helper()
	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "h1", MaxActiveJobs: hostCapacity},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "b1", Host: "h1", MaxConcurrentRequests: 3, Enabled: true},
		{ID: "b2", Host: "h1", MaxConcurrentRequests: 3, Enabled: true},
	})
	stats := NewStatsTracker()
	cfg := config.SchedulerConfig{
		Strategy:                        "priority_host_model_affinity",
		TopNLookahead:                   64,
		QueueMaxPending:                 200,
		AgingPerSecond:                  0.05,
		UnknownTokensPerSecond:          5.0,
		UnknownColdLoadPenaltySeconds:   30.0,
		AliasSubstitutionPenaltySeconds: 8.0,
		DisruptionFactor:                0.25,
		Retry:                           config.RetryConfig{MaxAttempts: 2},
	}
	scorer := NewScorer(stats, hosts, backends, cfg)

	snapshots := func() []backend.BackendSnapshot {
		return []backend.BackendSnapshot{
			{ID: "b1", URL: s1.URL, Host: "h1", Enabled: true, Healthy: true,
				AvailableModels: []string{"llama3"}, LoadedModels: []string{"llama3"}},
			{ID: "b2", URL: s2.URL, Host: "h1", Enabled: true, Healthy: true,
				AvailableModels: []string{"llama3"}, LoadedModels: []string{"llama3"}},
		}
	}

	return NewScheduler(NewQueue(cfg.AgingPerSecond), scorer, stats, http.DefaultClient,
		map[string]string{"b1": s1.URL, "b2": s2.URL}, nil, snapshots)
}

// ---------------------------------------------------------------------------
// Streaming integration tests
// ---------------------------------------------------------------------------

func TestScheduler_StreamingDispatch(t *testing.T) {
	chunks := []string{
		streamChunk("llama3", "Hello", false),
		streamChunk("llama3", " world", false),
		streamChunk("llama3", "", true),
	}
	server := httptest.NewServer(fakeStreamHandler(t, http.StatusOK, chunks))
	defer server.Close()

	cfg := config.SchedulerConfig{
		Strategy:                        "priority_host_model_affinity",
		TopNLookahead:                   64,
		QueueMaxPending:                 100,
		AgingPerSecond:                  0.05,
		UnknownTokensPerSecond:          5.0,
		UnknownColdLoadPenaltySeconds:   30.0,
		AliasSubstitutionPenaltySeconds: 8.0,
		DisruptionFactor:                0.25,
		Retry:                           config.RetryConfig{MaxAttempts: 1},
	}
	s := newSingleBackendScheduler(t, server, cfg)
	defer s.Stop()

	streamCh := make(chan *ollama.StreamChunk, 100)
	job := &Job{
		ID:         "stream-job",
		Kind:       KindChat,
		Priority:   KindChat.Priority(),
		Candidates: []string{"llama3"},
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "Say hello"}},
		Streaming:  true,
		StreamCh:   streamCh,
		JobCtx:     context.Background(),
		ResultChan: make(chan JobResult, 1),
	}

	if err := s.Submit(job); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	s.Start()

	var received []string
	timeout := time.After(5 * time.Second)

	for {
		select {
		case chunk, ok := <-streamCh:
			if !ok {
				// Channel closed — stream done.
				if len(received) == 0 {
					t.Fatal("no content received from stream")
				}
				if job.State != StateCompleted {
					t.Errorf("expected StateCompleted, got %v", job.State)
				}
				return
			}
			if chunk.Err != nil {
				t.Fatalf("stream error: %v", chunk.Err)
			}
			if chunk.Response != nil && chunk.Response.Message.Content != "" {
				received = append(received, chunk.Response.Message.Content)
			}
		case <-timeout:
			t.Fatal("timed out")
		}
	}
}

func TestScheduler_StreamingRetryBeforeFirstToken(t *testing.T) {
	// b1 writes invalid JSON as first line → triggers decode error → retry.
	b1Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{garbage\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer b1Server.Close()

	// b2 sends valid streaming response.
	chunks := []string{streamChunk("llama3", "Recovered", true)}
	b2Server := httptest.NewServer(fakeStreamHandler(t, http.StatusOK, chunks))
	defer b2Server.Close()

	s := newDualBackendScheduler(t, b1Server, b2Server, 2)
	defer s.Stop()

	streamCh := make(chan *ollama.StreamChunk, 100)
	job := &Job{
		ID:         "retry-job",
		Kind:       KindChat,
		Priority:   KindChat.Priority(),
		Candidates: []string{"llama3"},
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "Retry"}},
		Streaming:  true,
		StreamCh:   streamCh,
		JobCtx:     context.Background(),
		ResultChan: make(chan JobResult, 1),
	}

	if err := s.Submit(job); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	s.Start()

	timeout := time.After(5 * time.Second)
	for {
		select {
		case chunk, ok := <-streamCh:
			if !ok {
				if job.State != StateCompleted {
					t.Errorf("expected StateCompleted, got %v", job.State)
				}
				return
			}
			if chunk.Err != nil {
				t.Fatalf("unexpected stream error: %v", chunk.Err)
			}
		case <-timeout:
			t.Fatal("timed out")
		}
	}
}

func TestScheduler_StreamingNoRetryAfterFirstToken(t *testing.T) {
	// b1 sends a good first chunk, then garbage → mid-stream error, no retry.
	var streamUsed atomic.Bool
	b1Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer streamUsed.Store(true)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(streamChunk("llama3", "First", false)))
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("{corrupt\n"))
		flusher.Flush()
	}))
	defer b1Server.Close()

	// b2 should never be called — no retry after first token.
	b2Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("b2 should not be called after first token sent")
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer b2Server.Close()

	s := newDualBackendScheduler(t, b1Server, b2Server, 2)
	defer s.Stop()

	streamCh := make(chan *ollama.StreamChunk, 100)
	job := &Job{
		ID:         "no-retry-job",
		Kind:       KindChat,
		Priority:   KindChat.Priority(),
		Candidates: []string{"llama3"},
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "No retry"}},
		Streaming:  true,
		StreamCh:   streamCh,
		JobCtx:     context.Background(),
		ResultChan: make(chan JobResult, 1),
	}

	if err := s.Submit(job); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	s.Start()

	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-streamCh:
			if !ok {
				if job.State != StateFailed {
					t.Errorf("expected StateFailed, got %v", job.State)
				}
				if !streamUsed.Load() {
					t.Error("expected b1 stream to have been used")
				}
				return
			}
		case <-timeout:
			t.Fatal("timed out")
		}
	}
}

func TestScheduler_AliasFallbackLoadedBackup(t *testing.T) {
	var b1Hit, b2Hit atomic.Int32

	b1Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b1Hit.Add(1)
		resp := ollama.ChatResponse{
			Model:   "llama3",
			Message: ollama.ChatMessage{Role: "assistant", Content: "from primary"},
			Done:    true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer b1Server.Close()

	b2Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b2Hit.Add(1)
		resp := ollama.ChatResponse{
			Model:   "llama3:backup",
			Message: ollama.ChatMessage{Role: "assistant", Content: "from backup"},
			Done:    true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer b2Server.Close()

	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "h1", MaxActiveJobs: 4},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "b1", Host: "h1", MaxConcurrentRequests: 2, Enabled: true},
		{ID: "b2", Host: "h1", MaxConcurrentRequests: 2, Enabled: true},
	})
	stats := NewStatsTracker()
	cfg := config.SchedulerConfig{
		Strategy:                        "priority_host_model_affinity",
		TopNLookahead:                   64,
		QueueMaxPending:                 200,
		AgingPerSecond:                  0.05,
		UnknownTokensPerSecond:          5.0,
		UnknownColdLoadPenaltySeconds:   30.0,
		AliasSubstitutionPenaltySeconds: 8.0,
		DisruptionFactor:                0.25,
		Retry:                           config.RetryConfig{MaxAttempts: 1},
	}
	scorer := NewScorer(stats, hosts, backends, cfg)

	// b1 has llama3 loaded (primary alias), b2 has llama3:backup loaded.
	snapshots := func() []backend.BackendSnapshot {
		return []backend.BackendSnapshot{
			{
				ID: "b1", URL: b1Server.URL, Host: "h1", Enabled: true, Healthy: true,
				AvailableModels: []string{"llama3"}, LoadedModels: []string{"llama3"},
			},
			{
				ID: "b2", URL: b2Server.URL, Host: "h1", Enabled: true, Healthy: true,
				AvailableModels: []string{"llama3:backup"}, LoadedModels: []string{"llama3:backup"},
			},
		}
	}

	s := NewScheduler(
		NewQueue(cfg.AgingPerSecond), scorer, stats, http.DefaultClient,
		map[string]string{"b1": b1Server.URL, "b2": b2Server.URL},
		nil, snapshots,
	)
	defer s.Stop()

	aliasCfg := &config.AliasConfig{
		Name:         "my-alias",
		PrimaryModel: "llama3",
		BackupModels: []string{"llama3:backup"},
	}

	job := &Job{
		ID:             "alias-job",
		Kind:           KindChat,
		Priority:       KindChat.Priority(),
		Candidates:     []string{"llama3", "llama3:backup"},
		AliasConfig:    aliasCfg,
		RequestedModel: "my-alias",
		Messages:       []ollama.ChatMessage{{Role: "user", Content: "Hi"}},
		ResultChan:     make(chan JobResult, 1),
	}

	if err := s.Submit(job); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	s.Start()

	select {
	case result := <-job.ResultChan:
		if result.Err != nil {
			t.Fatalf("job failed: %v", result.Err)
		}
		if result.Response == nil {
			t.Fatal("expected non-nil response")
		}
		total := b1Hit.Load() + b2Hit.Load()
		if total < 1 {
			t.Error("expected at least one backend to be called")
		}
		if job.State != StateCompleted {
			t.Errorf("expected StateCompleted, got %v", job.State)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}
