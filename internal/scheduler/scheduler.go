package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/ollama"
)

// ErrQueueFull is returned by Submit when the queue has reached its maximum
// pending capacity.
var ErrQueueFull = fmt.Errorf("scheduler: queue is at maximum pending capacity")

// Scheduler is the central dispatch orchestrator for the LLM Go Proxy.
// It accepts jobs, scores them against available backends, acquires host and
// backend capacity leases, and runs each assignment in its own goroutine.
type Scheduler struct {
	Queue       *Queue
	Scorer      *Scorer
	Stats       *StatsTracker
	Client      *http.Client
	BackendURLs map[string]string // backend ID -> base URL
	Logger      *logging.Logger
	Ctx         context.Context
	Cancel      context.CancelFunc
	Wg          sync.WaitGroup
	Wakeup      chan struct{} // buffered cap 1, signals dispatch to re-evaluate
	Snapshots   func() []backend.BackendSnapshot
}

// NewScheduler creates a new Scheduler with the given components.
func NewScheduler(
	queue *Queue,
	scorer *Scorer,
	stats *StatsTracker,
	client *http.Client,
	backendURLs map[string]string,
	logger *logging.Logger,
	snapshots func() []backend.BackendSnapshot,
) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		Queue:       queue,
		Scorer:      scorer,
		Stats:       stats,
		Client:      client,
		BackendURLs: backendURLs,
		Logger:      logger,
		Ctx:         ctx,
		Cancel:      cancel,
		Wakeup:      make(chan struct{}, 1),
		Snapshots:   snapshots,
	}
}

// Submit enqueues a job for scheduling. The job's state is set to Pending.
// Returns ErrQueueFull if the queue has reached QueueMaxPending capacity.
func (s *Scheduler) Submit(job *Job) error {
	if s.Queue.PendingCount() >= s.Scorer.Config.QueueMaxPending {
		return ErrQueueFull
	}
	job.State = StatePending
	s.Queue.Enqueue(job)
	// Non-blocking send: a wakeup is already queued if the channel is full.
	select {
	case s.Wakeup <- struct{}{}:
	default:
	}
	return nil
}

// Start launches the dispatch loop in a background goroutine.
func (s *Scheduler) Start() {
	s.Wg.Add(1)
	go s.dispatchLoop()
}

// Stop cancels the scheduler context and waits for all goroutines to finish.
func (s *Scheduler) Stop() {
	s.Cancel()
	s.Wg.Wait()
}

// dispatchLoop is the main event loop that waits for wakeup signals and
// triggers dispatch rounds. It exits when the context is cancelled.
func (s *Scheduler) dispatchLoop() {
	defer s.Wg.Done()
	for {
		select {
		case <-s.Ctx.Done():
			return
		case <-s.Wakeup:
			s.dispatch()
		}
	}
}

// dispatch finds the best available assignment for pending jobs, acquires
// capacity leases, and launches runAssignment goroutines. It iterates until
// no valid assignment remains.
func (s *Scheduler) dispatch() {
	n := s.Scorer.Config.TopNLookahead
	if n <= 0 {
		n = 64
	}

	pendingJobs := s.Queue.TopN(n)
	if len(pendingJobs) == 0 {
		return
	}

	snapshots := s.Snapshots()

	for {
		best := s.Scorer.BestAssignment(pendingJobs, snapshots)
		if best == nil {
			break
		}

		// Acquire host lease. If at capacity, remove this job from
		// consideration and retry with the next best.
		if !s.Scorer.Hosts.AcquireHost(best.Host) {
			pendingJobs = removeJob(pendingJobs, best.Job.ID)
			if len(pendingJobs) == 0 {
				break
			}
			continue
		}

		// Acquire backend lease. If at capacity, release the host lease,
		// remove this job from consideration, and retry.
		if !s.Scorer.Backends.AcquireBackend(best.BackendID) {
			s.Scorer.Hosts.ReleaseHost(best.Host)
			pendingJobs = removeJob(pendingJobs, best.Job.ID)
			if len(pendingJobs) == 0 {
				break
			}
			continue
		}

		// Mark the job as running with concrete dispatch details.
		best.Job.State = StateRunning
		best.Job.BackendID = best.BackendID
		best.Job.ConcreteModel = best.ModelName

		// Remove the job from the persistent queue and our working list.
		s.Queue.Remove(best.Job.ID)
		pendingJobs = removeJob(pendingJobs, best.Job.ID)

		// Launch the assignment in its own goroutine.
		s.Wg.Add(1)
		go s.runAssignment(best)

		if len(pendingJobs) == 0 {
			break
		}
	}
}

// runAssignment executes a single job assignment against an Ollama backend.
// It runs in its own goroutine and is responsible for releasing leases and
// sending the result on the job's ResultChan.
func (s *Scheduler) runAssignment(a *Assignment) {
	defer s.Wg.Done()

	// Copy the assignment to avoid any mutation issues from the iterator.
	assignment := *a
	job := assignment.Job

	// Ensure leases are always released and the job is marked appropriately.
	defer func() {
		s.Scorer.Hosts.ReleaseHost(assignment.Host)
		s.Scorer.Backends.ReleaseBackend(assignment.BackendID)
		if job.State == StateRunning {
			job.State = StateFailed
		}
		// Signal wakeup so dispatch re-evaluates available capacity.
		select {
		case s.Wakeup <- struct{}{}:
		default:
		}
	}()

	baseURL, ok := s.BackendURLs[assignment.BackendID]
	if !ok {
		err := fmt.Errorf("scheduler: unknown backend %q", assignment.BackendID)
		if s.Logger != nil {
			s.Logger.Error("assignment failed",
				logging.String("job_id", job.ID),
				logging.String("error", err.Error()),
			)
		}
		job.State = StateFailed
		job.ResultChan <- JobResult{Err: err}
		return
	}

	chatReq := ollama.ChatRequest{
		Model:    assignment.ModelName,
		Messages: job.Messages,
		Stream:   false,
		Options:  job.Options,
		Think:    nil, // use default
	}

	startTime := time.Now()
	chatResp, err := ollama.SendChat(s.Client, baseURL, &chatReq)
	duration := time.Since(startTime)

	if err != nil {
		if s.Logger != nil {
			s.Logger.Error("assignment failed",
				logging.String("job_id", job.ID),
				logging.String("backend_id", assignment.BackendID),
				logging.String("model", assignment.ModelName),
				logging.String("error", err.Error()),
				logging.Duration("duration", duration),
			)
		}
		s.Stats.RecordFailure(BackendModelKey{
			BackendID: assignment.BackendID,
			ModelName: assignment.ModelName,
		})
		job.State = StateFailed
		job.ResultChan <- JobResult{Err: err}
		return
	}

	tps := computeTPS(chatResp)
	coldLoad := computeColdLoad(chatResp)

	s.Stats.RecordSuccess(BackendModelKey{
		BackendID: assignment.BackendID,
		ModelName: assignment.ModelName,
	}, tps, coldLoad)

	job.State = StateCompleted
	job.ResultChan <- JobResult{Response: chatResp}
}

// computeTPS estimates tokens per second from a ChatResponse.
// Uses EvalCount and TotalDuration when available; falls back to 5.0.
func computeTPS(resp *ollama.ChatResponse) float64 {
	if resp.EvalCount > 0 {
		totalSeconds := float64(resp.TotalDuration) / 1e9
		if totalSeconds > 0.001 {
			return float64(resp.EvalCount) / totalSeconds
		}
	}
	return 5.0
}

// computeColdLoad extracts the cold load duration in seconds from a ChatResponse.
func computeColdLoad(resp *ollama.ChatResponse) float64 {
	return float64(resp.LoadDuration) / 1e9
}

// removeJob removes a job by ID from a slice and returns the modified slice.
func removeJob(jobs []*Job, id string) []*Job {
	for i, j := range jobs {
		if j.ID == id {
			return append(jobs[:i], jobs[i+1:]...)
		}
	}
	return jobs
}
