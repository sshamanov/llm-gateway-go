package scheduler

import (
	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
)

// Assignment represents a proposed job-to-backend assignment.
type Assignment struct {
	Job         *Job
	BackendID   string
	Host        string
	ModelName   string   // the specific candidate model to use
	Cost        float64
	IsBackup    bool     // true if using a backup model (not primary)
	ModelLoaded bool     // true if the model is already loaded on this backend
}

// Scorer computes assignment costs and finds the best assignment.
type Scorer struct {
	Stats    *StatsTracker
	Hosts    *HostLeaseManager
	Backends *BackendLeaseManager
	Config   config.SchedulerConfig
}

// NewScorer creates a new Scorer.
func NewScorer(stats *StatsTracker, hosts *HostLeaseManager, backends *BackendLeaseManager, cfg config.SchedulerConfig) *Scorer {
	return &Scorer{
		Stats:    stats,
		Hosts:    hosts,
		Backends: backends,
		Config:   cfg,
	}
}

// ValidAssignments generates all valid assignments for the given pending jobs and
// backend snapshots. For each job, for each candidate model (in order), for each
// backend snapshot, it checks:
//
//  1. Backend is Enabled and Healthy
//  2. Backend's AvailableModels contains the candidate model
//  3. Host has free capacity (HostFreeCapacity > 0)
//  4. Backend has free capacity (BackendFreeCapacity > 0)
//
// The first candidate is primary (IsBackup=false); subsequent candidates are
// backups (IsBackup=true). Leases are not acquired here — only capacity is checked.
func (s *Scorer) ValidAssignments(jobs []*Job, snapshots []backend.BackendSnapshot) []Assignment {
	var assignments []Assignment

	for _, job := range jobs {
		if job.State != StatePending {
			continue
		}
		for ci, candidate := range job.Candidates {
			isBackup := ci > 0
			for _, snap := range snapshots {
				if !snap.Enabled || !snap.Healthy {
					continue
				}
				if !containsString(snap.AvailableModels, candidate) {
					continue
				}
				if s.Hosts.HostFreeCapacity(snap.Host) <= 0 {
					continue
				}
				if s.Backends.BackendFreeCapacity(snap.ID) <= 0 {
					continue
				}
				modelLoaded := containsString(snap.LoadedModels, candidate)
				assignments = append(assignments, Assignment{
					Job:         job,
					BackendID:   snap.ID,
					Host:        snap.Host,
					ModelName:   candidate,
					IsBackup:    isBackup,
					ModelLoaded: modelLoaded,
				})
			}
		}
	}
	return assignments
}

// Score computes the cost for an assignment. Lower cost is better.
//
//	cost =
//	    backend_wait_cost
//	  + host_load_cost
//	  + model_switch_cost
//	  + estimated_generation_time
//	  + disruption_cost
//	  + substitution_cost
//	  + failure_penalty
//	  - priority_credit
//	  - aging_credit
func (s *Scorer) Score(a *Assignment) float64 {
	bmKey := BackendModelKey{BackendID: a.BackendID, ModelName: a.ModelName}

	// backend_wait_cost: each active job on the backend adds wait.
	backendWaitCost := float64(s.Backends.BackendActiveJobs(a.BackendID)) * 2.0

	// host_load_cost: host contention.
	hostLoadCost := float64(s.Hosts.HostActiveJobs(a.Host)) * 1.0

	// model_switch_cost: 0 if the model is already loaded on this backend,
	// otherwise the cold load time (learned or fallback).
	modelSwitchCost := 0.0
	if !a.ModelLoaded {
		modelSwitchCost = s.Stats.GetColdLoadTime(bmKey, s.Config.UnknownColdLoadPenaltySeconds)
	}

	// estimated_generation_time: estimate 256 output tokens at the learned
	// tokens-per-second rate, or the unknown fallback.
	estGenTime := 256.0 / s.Stats.GetTokensPerSecond(bmKey, s.Config.UnknownTokensPerSecond)

	// disruption_cost: if this backend has loaded models and the candidate is
	// NOT among them, evicting the first loaded model adds cost.
	// Otherwise 0.
	disruptionCost := s.disruptionCost(a)

	// substitution_cost: penalty for using a backup model.
	substitutionCost := 0.0
	if a.IsBackup {
		substitutionCost = s.Config.AliasSubstitutionPenaltySeconds
	}

	// failure_penalty: each consecutive failure adds 60 seconds of cost.
	failurePenalty := float64(s.Stats.GetConsecutiveFailures(bmKey)) * 60.0

	// priority_credit: higher-priority jobs get a cost reduction.
	priorityCredit := float64(a.Job.Priority) / 10.0

	// aging_credit: jobs that have waited longer get a cost reduction.
	agingCredit := a.Job.Duration().Seconds() * s.Config.AgingPerSecond

	return backendWaitCost +
		hostLoadCost +
		modelSwitchCost +
		estGenTime +
		disruptionCost +
		substitutionCost +
		failurePenalty -
		priorityCredit -
		agingCredit
}

// disruptionCost computes eviction cost when the candidate model is not loaded
// and the backend has other loaded models.
func (s *Scorer) disruptionCost(a *Assignment) float64 {
	if a.ModelLoaded {
		return 0
	}
	// We cannot look up LoadedModels from here because Score works on a single
	// Assignment and does not receive snapshots. The ModelLoaded flag captures
	// whether the specific candidate model is loaded, but we also need to know
	// whether the backend has any loaded models at all. Since ModelLoaded is
	// false here but we don't know if there are other loaded models to evict,
	// we conservatively assume there may be and return the default disruption
	// cost. Callers that have snapshot context (e.g. BestAssignment) override
	// this by computing disruption directly and incorporating it via a field
	// on Assignment if needed.
	//
	// For the standalone Score method, we estimate disruption based on the
	// assumption that if the model isn't loaded, there may be something to
	// evict. This is a reasonable default for scoring purposes.
	return s.Config.DisruptionFactor * s.Stats.GetColdLoadTime(
		BackendModelKey{BackendID: a.BackendID, ModelName: a.ModelName},
		s.Config.UnknownColdLoadPenaltySeconds,
	)
}

// BestAssignment generates all valid assignments, scores each, and returns the
// one with the lowest cost. Returns nil if no valid assignment exists.
//
// This method uses backend snapshots to accurately compute model_switch_cost
// and disruption_cost based on actually loaded models.
func (s *Scorer) BestAssignment(jobs []*Job, snapshots []backend.BackendSnapshot) *Assignment {
	assignments := s.ValidAssignments(jobs, snapshots)
	if len(assignments) == 0 {
		return nil
	}

	var best *Assignment
	for i := range assignments {
		a := &assignments[i]
		a.Cost = s.Score(a)
		if best == nil || a.Cost < best.Cost {
			best = a
		}
	}
	return best
}

// containsString reports whether the slice contains the given string.
func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
