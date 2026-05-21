package scheduler

import (
	"testing"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
)

// defaultScorerConfig returns a SchedulerConfig with reasonable defaults for testing.
func defaultScorerConfig() config.SchedulerConfig {
	return config.SchedulerConfig{
		Strategy:                        "priority_host_model_affinity",
		TopNLookahead:                   64,
		QueueMaxPending:                 100,
		AgingPerSecond:                  0.05,
		UnknownTokensPerSecond:          5.0,
		UnknownColdLoadPenaltySeconds:   30.0,
		AliasSubstitutionPenaltySeconds: 8.0,
		DisruptionFactor:                0.25,
		Retry: config.RetryConfig{MaxAttempts: 2},
	}
}

// newTestScorer creates a Scorer with one host and one backend, both with capacity 2.
func newTestScorer(hostID, backendID string) *Scorer {
	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: hostID, MaxActiveJobs: 2},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: backendID, Host: hostID, MaxConcurrentRequests: 2, Enabled: true},
	})
	return NewScorer(NewStatsTracker(), hosts, backends, defaultScorerConfig())
}

func TestValidAssignments_Basic(t *testing.T) {
	now := time.Now()
	job := &Job{
		ID:         "j1",
		State:      StatePending,
		Candidates: []string{"model-a"},
		CreatedAt:  now,
	}
	scorer := newTestScorer("host-1", "b1")
	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"model-a"},
			LoadedModels:    nil,
		},
	}

	assignments := scorer.ValidAssignments([]*Job{job}, snapshots)
	if len(assignments) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(assignments))
	}
	if assignments[0].BackendID != "b1" {
		t.Errorf("expected BackendID b1, got %s", assignments[0].BackendID)
	}
	if assignments[0].ModelName != "model-a" {
		t.Errorf("expected ModelName model-a, got %s", assignments[0].ModelName)
	}
	if assignments[0].IsBackup {
		t.Error("expected IsBackup to be false for first candidate")
	}
	if assignments[0].ModelLoaded {
		t.Error("expected ModelLoaded to be false (model not in LoadedModels)")
	}
}

func TestValidAssignments_NoMatchingModel(t *testing.T) {
	job := &Job{
		ID:         "j1",
		State:      StatePending,
		Candidates: []string{"model-a"},
		CreatedAt:  time.Now(),
	}
	scorer := newTestScorer("host-1", "b1")
	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"model-b"},
			LoadedModels:    nil,
		},
	}
	assignments := scorer.ValidAssignments([]*Job{job}, snapshots)
	if len(assignments) != 0 {
		t.Fatalf("expected 0 assignments, got %d", len(assignments))
	}
}

func TestValidAssignments_DisabledBackend(t *testing.T) {
	job := &Job{
		ID:         "j1",
		State:      StatePending,
		Candidates: []string{"model-a"},
		CreatedAt:  time.Now(),
	}
	scorer := newTestScorer("host-1", "b1")
	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         false, // disabled
			Healthy:         true,
			AvailableModels: []string{"model-a"},
			LoadedModels:    nil,
		},
	}
	assignments := scorer.ValidAssignments([]*Job{job}, snapshots)
	if len(assignments) != 0 {
		t.Fatalf("expected 0 assignments for disabled backend, got %d", len(assignments))
	}
}

func TestValidAssignments_UnhealthyBackend(t *testing.T) {
	job := &Job{
		ID:         "j1",
		State:      StatePending,
		Candidates: []string{"model-a"},
		CreatedAt:  time.Now(),
	}
	scorer := newTestScorer("host-1", "b1")
	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         false, // unhealthy
			AvailableModels: []string{"model-a"},
			LoadedModels:    nil,
		},
	}
	assignments := scorer.ValidAssignments([]*Job{job}, snapshots)
	if len(assignments) != 0 {
		t.Fatalf("expected 0 assignments for unhealthy backend, got %d", len(assignments))
	}
}

func TestValidAssignments_HostAtCapacity(t *testing.T) {
	job := &Job{
		ID:         "j1",
		State:      StatePending,
		Candidates: []string{"model-a"},
		CreatedAt:  time.Now(),
	}

	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "host-1", MaxActiveJobs: 1},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "b1", Host: "host-1", MaxConcurrentRequests: 2, Enabled: true},
	})
	scorer := NewScorer(NewStatsTracker(), hosts, backends, defaultScorerConfig())

	// Fill the host to capacity.
	scorer.Hosts.AcquireHost("host-1")
	if got := scorer.Hosts.HostFreeCapacity("host-1"); got != 0 {
		t.Fatalf("expected host free capacity 0, got %d", got)
	}

	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"model-a"},
			LoadedModels:    nil,
		},
	}
	assignments := scorer.ValidAssignments([]*Job{job}, snapshots)
	if len(assignments) != 0 {
		t.Fatalf("expected 0 assignments when host is at capacity, got %d", len(assignments))
	}
}

func TestValidAssignments_BackendAtCapacity(t *testing.T) {
	job := &Job{
		ID:         "j1",
		State:      StatePending,
		Candidates: []string{"model-a"},
		CreatedAt:  time.Now(),
	}

	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "host-1", MaxActiveJobs: 2},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "b1", Host: "host-1", MaxConcurrentRequests: 1, Enabled: true},
	})
	scorer := NewScorer(NewStatsTracker(), hosts, backends, defaultScorerConfig())

	// Fill the backend to capacity.
	scorer.Backends.AcquireBackend("b1")
	if got := scorer.Backends.BackendFreeCapacity("b1"); got != 0 {
		t.Fatalf("expected backend free capacity 0, got %d", got)
	}

	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"model-a"},
			LoadedModels:    nil,
		},
	}
	assignments := scorer.ValidAssignments([]*Job{job}, snapshots)
	if len(assignments) != 0 {
		t.Fatalf("expected 0 assignments when backend is at capacity, got %d", len(assignments))
	}
}

func TestValidAssignments_BackupModels(t *testing.T) {
	now := time.Now()
	job := &Job{
		ID:    "j1",
		State: StatePending,
		// Primary is not on any backend; backup model is available on backend-2.
		Candidates: []string{"primary-model", "backup-model"},
		CreatedAt:  now,
	}

	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "host-1", MaxActiveJobs: 2},
		{ID: "host-2", MaxActiveJobs: 2},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "b1", Host: "host-1", MaxConcurrentRequests: 2, Enabled: true},
		{ID: "b2", Host: "host-2", MaxConcurrentRequests: 2, Enabled: true},
	})
	scorer := NewScorer(NewStatsTracker(), hosts, backends, defaultScorerConfig())

	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"primary-model"},
			LoadedModels:    nil,
		},
		{
			ID:              "b2",
			Host:            "host-2",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"backup-model"},
			LoadedModels:    nil,
		},
	}

	assignments := scorer.ValidAssignments([]*Job{job}, snapshots)
	if len(assignments) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(assignments))
	}

	// Assignment 0: primary model on b1.
	a0 := assignments[0]
	if a0.BackendID != "b1" || a0.ModelName != "primary-model" {
		t.Errorf("expected primary assignment (b1, primary-model), got (%s, %s)", a0.BackendID, a0.ModelName)
	}
	if a0.IsBackup {
		t.Error("expected IsBackup=false for primary model")
	}

	// Assignment 1: backup model on b2.
	a1 := assignments[1]
	if a1.BackendID != "b2" || a1.ModelName != "backup-model" {
		t.Errorf("expected backup assignment (b2, backup-model), got (%s, %s)", a1.BackendID, a1.ModelName)
	}
	if !a1.IsBackup {
		t.Error("expected IsBackup=true for backup model")
	}
}

func TestScore_LoadedModelCheaper(t *testing.T) {
	stats := NewStatsTracker()
	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "host-1", MaxActiveJobs: 2},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "b1", Host: "host-1", MaxConcurrentRequests: 2, Enabled: true},
	})
	cfg := defaultScorerConfig()
	scorer := NewScorer(stats, hosts, backends, cfg)

	// Seed identical stats for both models so estGenTime is the same.
	loadedKey := BackendModelKey{BackendID: "b1", ModelName: "loaded-model"}
	unloadedKey := BackendModelKey{BackendID: "b1", ModelName: "unloaded-model"}
	stats.RecordSuccess(loadedKey, 20.0, 5.0)
	stats.RecordSuccess(unloadedKey, 20.0, 5.0)

	now := time.Now()
	job := &Job{ID: "j1", Priority: 100, CreatedAt: now}

	loaded := Assignment{
		Job:         job,
		BackendID:   "b1",
		ModelName:   "loaded-model",
		ModelLoaded: true,
		IsBackup:    false,
	}
	unloaded := Assignment{
		Job:         job,
		BackendID:   "b1",
		ModelName:   "unloaded-model",
		ModelLoaded: false,
		IsBackup:    false,
	}

	costLoaded := scorer.Score(&loaded)
	costUnloaded := scorer.Score(&unloaded)

	if costLoaded >= costUnloaded {
		t.Errorf("expected loaded model cost (%.2f) < unloaded model cost (%.2f)", costLoaded, costUnloaded)
	}
}

func TestScore_SubstitutionPenalty(t *testing.T) {
	scorer := newTestScorer("host-1", "b1")
	now := time.Now()
	job := &Job{ID: "j1", Priority: 100, CreatedAt: now}

	primary := Assignment{
		Job:         job,
		BackendID:   "b1",
		ModelName:   "model-a",
		ModelLoaded: true,
		IsBackup:    false,
	}
	backup := Assignment{
		Job:         job,
		BackendID:   "b1",
		ModelName:   "model-a",
		ModelLoaded: true,
		IsBackup:    true,
	}

	costPrimary := scorer.Score(&primary)
	costBackup := scorer.Score(&backup)

	if costBackup-costPrimary < 7.0 || costBackup-costPrimary > 9.0 {
		t.Errorf("expected backup cost to be ~%.0f more than primary, got difference %.2f",
			scorer.Config.AliasSubstitutionPenaltySeconds, costBackup-costPrimary)
	}
}

func TestScore_PriorityCredit(t *testing.T) {
	scorer := newTestScorer("host-1", "b1")
	now := time.Now()

	highPriorityJob := &Job{ID: "j1", Priority: 100, CreatedAt: now}
	lowPriorityJob := &Job{ID: "j2", Priority: 50, CreatedAt: now}

	baseAssign := Assignment{
		BackendID:   "b1",
		Host:        "host-1",
		ModelName:   "model-a",
		ModelLoaded: true,
		IsBackup:    false,
	}

	highAssign := baseAssign
	highAssign.Job = highPriorityJob
	lowAssign := baseAssign
	lowAssign.Job = lowPriorityJob

	costHigh := scorer.Score(&highAssign)
	costLow := scorer.Score(&lowAssign)

	if costHigh >= costLow {
		t.Errorf("expected higher-priority job cost (%.2f) < lower-priority job cost (%.2f)", costHigh, costLow)
	}

	// The difference should be exactly (100/10) - (50/10) = 5.0, minus any minor
	// variation from Duration() drift. Allow a small tolerance.
	diff := costLow - costHigh
	if diff < 4.5 || diff > 5.5 {
		t.Errorf("expected cost difference ~5.0 from priority credit, got %.2f", diff)
	}
}

func TestScore_AgingCredit(t *testing.T) {
	scorer := newTestScorer("host-1", "b1")

	oldJob := &Job{ID: "j1", Priority: 100, CreatedAt: time.Now().Add(-10 * time.Second)}
	newJob := &Job{ID: "j2", Priority: 100, CreatedAt: time.Now()}

	baseAssign := Assignment{
		BackendID:   "b1",
		Host:        "host-1",
		ModelName:   "model-a",
		ModelLoaded: true,
		IsBackup:    false,
	}

	oldAssign := baseAssign
	oldAssign.Job = oldJob
	newAssign := baseAssign
	newAssign.Job = newJob

	costOld := scorer.Score(&oldAssign)
	costNew := scorer.Score(&newAssign)

	if costOld >= costNew {
		t.Errorf("expected older job cost (%.2f) < newer job cost (%.2f) due to aging credit", costOld, costNew)
	}
}

func TestScore_FailurePenalty(t *testing.T) {
	stats := NewStatsTracker()
	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "host-1", MaxActiveJobs: 2},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "b1", Host: "host-1", MaxConcurrentRequests: 2, Enabled: true},
	})
	scorer := NewScorer(stats, hosts, backends, defaultScorerConfig())

	// Record 3 failures for backend b1 + model-a.
	// RecordFailure does NOT reset failures; only RecordSuccess does.
	failKey := BackendModelKey{BackendID: "b1", ModelName: "model-a"}
	stats.RecordFailure(failKey)
	stats.RecordFailure(failKey)
	stats.RecordFailure(failKey)

	// Model-b has no failures. Both models will use the same fallback values
	// for tokens-per-second and cold load time, so the only cost difference
	// is the failure penalty.

	now := time.Now()
	job := &Job{ID: "j1", Priority: 100, CreatedAt: now}

	failAssign := Assignment{
		Job:         job,
		BackendID:   "b1",
		ModelName:   "model-a",
		ModelLoaded: true,
		IsBackup:    false,
	}
	cleanAssign := Assignment{
		Job:         job,
		BackendID:   "b1",
		ModelName:   "model-b",
		ModelLoaded: true,
		IsBackup:    false,
	}

	costFail := scorer.Score(&failAssign)
	costClean := scorer.Score(&cleanAssign)

	if costFail <= costClean {
		t.Errorf("expected failed model cost (%.2f) > clean model cost (%.2f)", costFail, costClean)
	}

	// Both models use fallback stats (no RecordSuccess), so all other cost
	// components are identical. The only difference is failure_penalty = 3*60 = 180.
	diff := costFail - costClean
	if diff < 170 || diff > 190 {
		t.Errorf("expected cost difference ~180 from failure penalty, got %.2f", diff)
	}
}

func TestBestAssignment_ReturnsLowestCost(t *testing.T) {
	stats := NewStatsTracker()
	hosts := NewHostLeaseManager([]config.HostConfig{
		{ID: "host-1", MaxActiveJobs: 2},
	})
	backends := NewBackendLeaseManager([]config.OllamaBackendConfig{
		{ID: "b1", Host: "host-1", MaxConcurrentRequests: 2, Enabled: true},
		{ID: "b2", Host: "host-1", MaxConcurrentRequests: 2, Enabled: true},
	})
	cfg := defaultScorerConfig()
	scorer := NewScorer(stats, hosts, backends, cfg)

	now := time.Now()
	job := &Job{
		ID:         "j1",
		State:      StatePending,
		Priority:   100,
		Candidates: []string{"model-a"},
		CreatedAt:  now,
	}

	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"model-a"},
			LoadedModels:    []string{"model-a"}, // model already loaded
		},
		{
			ID:              "b2",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"model-a"},
			LoadedModels:    nil, // model not loaded
		},
	}

	best := scorer.BestAssignment([]*Job{job}, snapshots)
	if best == nil {
		t.Fatal("expected a best assignment, got nil")
	}
	if best.BackendID != "b1" {
		t.Errorf("expected best assignment to be b1 (loaded model), got %s", best.BackendID)
	}
	if best.ModelName != "model-a" {
		t.Errorf("expected best model to be model-a, got %s", best.ModelName)
	}
	if best.IsBackup {
		t.Error("expected IsBackup to be false")
	}
	if !best.ModelLoaded {
		t.Error("expected ModelLoaded to be true for best assignment")
	}
}

func TestBestAssignment_NoValid(t *testing.T) {
	scorer := newTestScorer("host-1", "b1")
	now := time.Now()
	job := &Job{
		ID:         "j1",
		State:      StatePending,
		Candidates: []string{"unknown-model"},
		CreatedAt:  now,
	}

	snapshots := []backend.BackendSnapshot{
		{
			ID:              "b1",
			Host:            "host-1",
			Enabled:         true,
			Healthy:         true,
			AvailableModels: []string{"model-a"},
			LoadedModels:    nil,
		},
	}

	best := scorer.BestAssignment([]*Job{job}, snapshots)
	if best != nil {
		t.Fatalf("expected nil when no valid assignment exists, got %+v", *best)
	}
}
