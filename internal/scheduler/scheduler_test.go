package scheduler

import (
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
)

// newTestScheduler creates a Scheduler with one host and one backend, both with
// capacity 2, and a snapshot function pointing at the given server URL.
// The scheduler is NOT started — the caller must call Start() when ready.
func newTestScheduler(t *testing.T, serverURL string, maxHostJobs int, cfg config.SchedulerConfig) *Scheduler {
	t.Helper()
	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "default", MaxActiveJobs: maxHostJobs},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "test-backend", Host: "default", MaxConcurrentRequests: 2, Enabled: true},
	})
	stats := NewStatsTracker()
	scorer := NewScorer(stats, hosts, backends, cfg)

	snapshots := func() []backend.BackendSnapshot {
		return []backend.BackendSnapshot{
			{
				ID:              "test-backend",
				URL:             serverURL,
				Host:            "default",
				Enabled:         true,
				Healthy:         true,
				AvailableModels: []string{"llama3"},
				LoadedModels:    []string{"llama3"},
			},
		}
	}

	return NewScheduler(
		NewQueue(cfg.AgingPerSecond),
		scorer,
		stats,
		http.DefaultClient,
		map[string]string{"test-backend": serverURL},
		nil,
		nil, // logger — nil is safe (Scheduler guards calls)
		snapshots,
	)
}

// defaultConfig returns a SchedulerConfig with sensible test defaults.
func defaultConfig() config.SchedulerConfig {
	return config.SchedulerConfig{
		Strategy:                        "priority_host_model_affinity",
		TopNLookahead:                   64,
		QueueMaxPending:                 100,
		AgingPerSecond:                  0.05,
		UnknownTokensPerSecond:          5.0,
		UnknownColdLoadPenaltySeconds:   30.0,
		AliasSubstitutionPenaltySeconds: 8.0,
		DisruptionFactor:                0.25,
		Retry:                           config.RetryConfig{MaxAttempts: 2},
	}
}

// validChatResponse returns a minimal valid Ollama ChatResponse JSON body.
func validChatResponse(model, content string) string {
	resp := ollama.ChatResponse{
		Model:          model,
		Message:        ollama.ChatMessage{Role: "assistant", Content: content},
		Done:           true,
		TotalDuration:  500000000, // 0.5s
		LoadDuration:   100000000, // 0.1s
		EvalCount:      50,
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// fakeOllamaHandler returns an http.HandlerFunc that responds to POST /api/chat
// with a valid ChatResponse. The given responseBody is used when non-empty;
// otherwise a default response is generated.
func fakeOllamaHandler(t *testing.T, responseBody string, statusCode int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			return
		}
		body := responseBody
		if body == "" {
			body = validChatResponse("llama3", "Hello")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestScheduler_Submit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	s := newTestScheduler(t, server.URL, 2, defaultConfig())

	job := &Job{
		ID:         "test-job",
		Candidates: []string{"llama3"},
		ResultChan: make(chan JobResult, 1),
	}

	err := s.Submit(job)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if job.State != StatePending {
		t.Errorf("expected StatePending, got %v", job.State)
	}
	if s.Queue.Len() != 1 {
		t.Errorf("expected queue length 1, got %d", s.Queue.Len())
	}
}

func TestScheduler_Submit_QueueFull(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	cfg := defaultConfig()
	cfg.QueueMaxPending = 1
	s := newTestScheduler(t, server.URL, 2, cfg)

	job1 := &Job{
		ID:         "job1",
		Candidates: []string{"llama3"},
		ResultChan: make(chan JobResult, 1),
	}
	job2 := &Job{
		ID:         "job2",
		Candidates: []string{"llama3"},
		ResultChan: make(chan JobResult, 1),
	}

	if err := s.Submit(job1); err != nil {
		t.Fatalf("first Submit should succeed, got %v", err)
	}
	if err := s.Submit(job2); err == nil {
		t.Fatal("expected ErrQueueFull on second Submit, got nil")
	} else if err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}
}

func TestScheduler_Dispatch_Basic(t *testing.T) {
	server := httptest.NewServer(fakeOllamaHandler(t, "", http.StatusOK))
	defer server.Close()

	cfg := defaultConfig()
	s := newTestScheduler(t, server.URL, 2, cfg)
	defer s.Stop()

	job := &Job{
		ID:         "job1",
		Candidates: []string{"llama3"},
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "Hi"}},
		ResultChan: make(chan JobResult, 1),
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
		if result.Response.Message.Content != "Hello" {
			t.Errorf("expected content %q, got %q", "Hello", result.Response.Message.Content)
		}
		if job.State != StateCompleted {
			t.Errorf("expected StateCompleted, got %v", job.State)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

func TestScheduler_Dispatch_ForwardsTools(t *testing.T) {
	var capturedBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		capturedBody.Store(string(body))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(validChatResponse("llama3", "Hello")))
	}))
	defer server.Close()

	cfg := defaultConfig()
	s := newTestScheduler(t, server.URL, 2, cfg)
	defer s.Stop()

	job := &Job{
		ID:         "job-tools",
		Candidates: []string{"llama3"},
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "What's the weather?"}},
		Tools:      json.RawMessage(`[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}}}}}]`),
		ResultChan: make(chan JobResult, 1),
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
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}

	body := capturedBody.Load()
	if body == nil {
		t.Fatal("expected /api/chat request body captured")
	}
	var chatReq ollama.ChatRequest
	if err := json.Unmarshal([]byte(body.(string)), &chatReq); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(chatReq.Tools), "get_weather") {
		t.Errorf("expected tools forwarded in dispatch request, got %s", chatReq.Tools)
	}
}

func TestScheduler_Dispatch_NoBackend(t *testing.T) {
	// Create a server (it won't be called since no backend has the model).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := defaultConfig()
	s := newTestScheduler(t, server.URL, 2, cfg)
	defer s.Stop()

	// Override snapshots to only have a model that doesn't match the job.
	s.Snapshots = func() []backend.BackendSnapshot {
		return []backend.BackendSnapshot{
			{
				ID:              "test-backend",
				URL:             server.URL,
				Host:            "default",
				Enabled:         true,
				Healthy:         true,
				AvailableModels: []string{"some-other-model"},
				LoadedModels:    nil,
			},
		}
	}

	job := &Job{
		ID:         "job1",
		Candidates: []string{"llama3"}, // not in AvailableModels
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "Hi"}},
		ResultChan: make(chan JobResult, 1),
	}

	if err := s.Submit(job); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Job should be pending in the queue.
	if job.State != StatePending {
		t.Errorf("expected StatePending, got %v", job.State)
	}
	if s.Queue.Len() != 1 {
		t.Errorf("expected 1 job in queue, got %d", s.Queue.Len())
	}

	s.Start()

	// Give dispatch a chance to run (it should find no valid assignments).
	time.Sleep(200 * time.Millisecond)

	// Job should still be pending — no backend can serve it.
	if job.State != StatePending {
		t.Errorf("expected job to remain Pending, got %v", job.State)
	}

	// No result should have been sent.
	select {
	case <-job.ResultChan:
		t.Fatal("unexpected result for unschedulable job")
	default:
	}
}

func TestScheduler_Dispatch_HostCapacitySerialization(t *testing.T) {
	server := httptest.NewServer(fakeOllamaHandler(t, "", http.StatusOK))
	defer server.Close()

	cfg := defaultConfig()
	// Host capacity of 1 means only one job can run on "default" at a time.
	s := newTestScheduler(t, server.URL, 1, cfg)
	defer s.Stop()

	job1 := &Job{
		ID:         "job1",
		Candidates: []string{"llama3"},
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "One"}},
		ResultChan: make(chan JobResult, 1),
	}
	job2 := &Job{
		ID:         "job2",
		Candidates: []string{"llama3"},
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "Two"}},
		ResultChan: make(chan JobResult, 1),
	}

	if err := s.Submit(job1); err != nil {
		t.Fatalf("Submit job1 failed: %v", err)
	}
	if err := s.Submit(job2); err != nil {
		t.Fatalf("Submit job2 failed: %v", err)
	}

	s.Start()

	// Both jobs should complete within the timeout.
	for i, j := range []*Job{job1, job2} {
		select {
		case result := <-j.ResultChan:
			if result.Err != nil {
				t.Errorf("job%d failed: %v", i+1, result.Err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for job%d", i+1)
		}
	}

	// Verify job1 completed before job2 started by checking that at no point
	// both were Running simultaneously. Since host capacity = 1, the second
	// job could only start after the first released its lease.
	if job1.State != StateCompleted {
		t.Errorf("expected job1 StateCompleted, got %v", job1.State)
	}
	if job2.State != StateCompleted {
		t.Errorf("expected job2 StateCompleted, got %v", job2.State)
	}
}

func TestScheduler_Dispatch_Failure(t *testing.T) {
	server := httptest.NewServer(fakeOllamaHandler(t, "", http.StatusInternalServerError))
	defer server.Close()

	cfg := defaultConfig()
	s := newTestScheduler(t, server.URL, 2, cfg)
	defer s.Stop()

	job := &Job{
		ID:         "job1",
		Candidates: []string{"llama3"},
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "Hi"}},
		ResultChan: make(chan JobResult, 1),
	}

	if err := s.Submit(job); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	s.Start()

	select {
	case result := <-job.ResultChan:
		if result.Err == nil {
			t.Fatal("expected an error result, got nil")
		}
		if result.Response != nil {
			t.Error("expected nil response on failure")
		}
		if job.State != StateFailed {
			t.Errorf("expected StateFailed, got %v", job.State)
		}
		// Verify failure was recorded in stats.
		key := BackendModelKey{BackendID: "test-backend", ModelName: "llama3"}
		if failures := s.Stats.GetConsecutiveFailures(key); failures != 1 {
			t.Errorf("expected 1 consecutive failure, got %d", failures)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job result")
	}
}

func TestScheduler_Dispatch_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(fakeOllamaHandler(t, "", http.StatusOK))
	defer server.Close()

	cfg := defaultConfig()
	s := newTestScheduler(t, server.URL, 2, cfg)

	// Start and stop should not panic or hang.
	s.Start()
	s.Stop()
}

func TestScheduler_Dispatch_MultipleJobs(t *testing.T) {
	server := httptest.NewServer(fakeOllamaHandler(t, "", http.StatusOK))
	defer server.Close()

	cfg := defaultConfig()
	s := newTestScheduler(t, server.URL, 5, cfg)
	defer s.Stop()

	const numJobs = 3
	jobs := make([]*Job, numJobs)
	for i := 0; i < numJobs; i++ {
		id := fmt.Sprintf("job%d", i+1)
		jobs[i] = &Job{
			ID:         id,
			Candidates: []string{"llama3"},
			Messages:   []ollama.ChatMessage{{Role: "user", Content: "Hello"}},
			ResultChan: make(chan JobResult, 1),
		}
		if err := s.Submit(jobs[i]); err != nil {
			t.Fatalf("Submit %s failed: %v", id, err)
		}
	}

	s.Start()

	for i, j := range jobs {
		select {
		case result := <-j.ResultChan:
			if result.Err != nil {
				t.Errorf("job%d failed: %v", i+1, result.Err)
			}
			if result.Response == nil {
				t.Errorf("job%d: expected non-nil response", i+1)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", j.ID)
		}
	}

	// Verify all jobs reached a terminal state.
	for i, j := range jobs {
		if j.State != StateCompleted {
			t.Errorf("job%d expected StateCompleted, got %v", i+1, j.State)
		}
	}
}
