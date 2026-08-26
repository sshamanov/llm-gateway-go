package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
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
	Queue          *Queue
	Scorer         *Scorer
	Stats          *StatsTracker
	Client         *http.Client
	BackendURLs    map[string]string                  // backend ID -> base URL
	BackendConfigs map[string]config.OllamaBackendConfig // backend ID -> config
	Logger         *logging.Logger
	Ctx            context.Context
	Cancel         context.CancelFunc
	Wg             sync.WaitGroup
	Wakeup         chan struct{} // buffered cap 1, signals dispatch to re-evaluate
	Snapshots      func() []backend.BackendSnapshot
	StartedAt      time.Time
}

// NewScheduler creates a new Scheduler with the given components.
func NewScheduler(
	queue *Queue,
	scorer *Scorer,
	stats *StatsTracker,
	client *http.Client,
	backendURLs map[string]string,
	backendConfigs map[string]config.OllamaBackendConfig,
	logger *logging.Logger,
	snapshots func() []backend.BackendSnapshot,
) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		Queue:          queue,
		Scorer:         scorer,
		Stats:          stats,
		Client:         client,
		BackendURLs:    backendURLs,
		BackendConfigs: backendConfigs,
		Logger:         logger,
		Ctx:            ctx,
		Cancel:         cancel,
		Wakeup:         make(chan struct{}, 1),
		Snapshots:      snapshots,
		StartedAt:   time.Now(),
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
			job.State = StateAborted
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

	if job.Streaming {
		s.runStreamingAssignment(&assignment, baseURL)
		return
	}

	s.runNonStreamingAssignment(&assignment, baseURL)
}

// runNonStreamingAssignment executes a non-streaming job assignment with retry.
// On failure it finds alternative backends (excluding previously-failed ones),
// up to maxAttempts. Leases are released and re-acquired for each attempt.
func (s *Scheduler) runNonStreamingAssignment(a *Assignment, baseURL string) {
	job := a.Job
	currentBackendID := a.BackendID
	currentHost := a.Host
	currentModel := a.ModelName
	currentBaseURL := baseURL

	failedBackends := make(map[string]bool)

	// Ensure the outer defer in runAssignment releases the correct (last-used)
	// leases on return.
	defer func() {
		s.Scorer.Hosts.ReleaseHost(currentHost)
		s.Scorer.Backends.ReleaseBackend(currentBackendID)
	}()

	maxAttempts := s.Scorer.Config.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Release current (failed) leases.
			s.Scorer.Hosts.ReleaseHost(currentHost)
			s.Scorer.Backends.ReleaseBackend(currentBackendID)

			alt := s.findAlternativeBackend(job, failedBackends)
			if alt == nil {
				err := fmt.Errorf("scheduler: no alternative backend after %d attempts", attempt)
				if s.Logger != nil {
					s.Logger.Error("non-streaming retry exhausted",
						logging.String("job_id", job.ID),
						logging.Int("attempts", attempt),
					)
				}
				job.State = StateFailed
				job.ResultChan <- JobResult{Err: err}
				return
			}

			if !s.Scorer.Hosts.AcquireHost(alt.Host) {
				err := fmt.Errorf("scheduler: host at capacity for retry")
				job.State = StateFailed
				job.ResultChan <- JobResult{Err: err}
				return
			}
			if !s.Scorer.Backends.AcquireBackend(alt.BackendID) {
				s.Scorer.Hosts.ReleaseHost(alt.Host)
				err := fmt.Errorf("scheduler: backend at capacity for retry")
				job.State = StateFailed
				job.ResultChan <- JobResult{Err: err}
				return
			}

			currentBackendID = alt.BackendID
			currentHost = alt.Host
			currentModel = alt.ModelName
			currentBaseURL = s.BackendURLs[alt.BackendID]

			// Update outer assignment so the defer in runAssignment uses correct leases.
			a.Host = alt.Host
			a.BackendID = alt.BackendID
			job.BackendID = alt.BackendID
			job.ConcreteModel = alt.ModelName
		}

		failedBackends[currentBackendID] = true

		chatReq := ollama.ChatRequest{
			Model:     currentModel,
			Messages:  job.Messages,
			Stream:    false,
			Options:   job.Options,
			Think:     job.Think,
			KeepAlive: job.KeepAlive,
			Tools:     job.Tools,
		}
		s.mergeBackendOptions(&chatReq, currentBackendID)

		ctx := job.JobCtx
		if ctx == nil {
			ctx = context.Background()
		}

		startTime := time.Now()
		chatResp, err := ollama.SendChat(ctx, s.Client, currentBaseURL, &chatReq)
		duration := time.Since(startTime)

		if err != nil {
			// If the client cancelled, abort without recording a failure.
			if job.JobCtx != nil {
				select {
				case <-job.JobCtx.Done():
					job.State = StateAborted
					job.ResultChan <- JobResult{Err: job.JobCtx.Err()}
					return
				default:
				}
			}
			s.Stats.RecordFailure(BackendModelKey{
				BackendID: currentBackendID,
				ModelName: currentModel,
			})
			if s.Logger != nil {
				s.Logger.Warn("non-streaming request failed, will retry",
					logging.String("job_id", job.ID),
					logging.String("backend_id", currentBackendID),
					logging.String("model", currentModel),
					logging.String("error", err.Error()),
					logging.Duration("duration", duration),
					logging.Int("attempt", attempt+1),
				)
			}
			continue
		}

		tps := computeTPS(chatResp)
		coldLoad := computeColdLoad(chatResp)

		s.Stats.RecordSuccess(BackendModelKey{
			BackendID: currentBackendID,
			ModelName: currentModel,
		}, tps, coldLoad)

		job.State = StateCompleted
		job.ResultChan <- JobResult{Response: chatResp}
		return
	}

	err := fmt.Errorf("scheduler: non-streaming job failed after %d attempts", maxAttempts)
	job.State = StateFailed
	job.ResultChan <- JobResult{Err: err}
}

// runStreamingAssignment executes a streaming job assignment against an Ollama
// backend. It manages its own retry loop (with context-aware chunk forwarding)
// and releases/captures leases so the outer defer in runAssignment correctly
// releases the final set of leases on return.
func (s *Scheduler) runStreamingAssignment(a *Assignment, baseURL string) {
	job := a.Job
	currentBackendID := a.BackendID
	currentHost := a.Host
	currentModel := a.ModelName
	currentBaseURL := baseURL

	// Track failed backend IDs so we don't retry the same one.
	failedBackends := make(map[string]bool)

	// Ensure cleanup on exit.
	defer func() {
		s.Scorer.Hosts.ReleaseHost(currentHost)
		s.Scorer.Backends.ReleaseBackend(currentBackendID)
		if job.State == StateRunning {
			job.State = StateAborted
		}
		close(job.StreamCh)
		select {
		case s.Wakeup <- struct{}{}:
		default:
		}
	}()

	maxAttempts := s.Scorer.Config.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// If not first attempt, find an alternative backend.
		if attempt > 0 {
			// Release current (failed) leases.
			s.Scorer.Hosts.ReleaseHost(currentHost)
			s.Scorer.Backends.ReleaseBackend(currentBackendID)

			// Find an alternative backend, excluding previously-failed ones.
			alt := s.findAlternativeBackend(job, failedBackends)
			if alt == nil {
				err := fmt.Errorf("scheduler: no alternative backend after %d attempts", attempt)
				if s.Logger != nil {
					s.Logger.Error("streaming retry exhausted",
						logging.String("job_id", job.ID),
						logging.Int("attempts", attempt),
					)
				}
				job.State = StateFailed
				job.ResultChan <- JobResult{Err: err}
				return
			}

			// Acquire new leases.
			if !s.Scorer.Hosts.AcquireHost(alt.Host) {
				err := fmt.Errorf("scheduler: host at capacity for retry")
				job.State = StateFailed
				job.ResultChan <- JobResult{Err: err}
				return
			}
			if !s.Scorer.Backends.AcquireBackend(alt.BackendID) {
				s.Scorer.Hosts.ReleaseHost(alt.Host)
				err := fmt.Errorf("scheduler: backend at capacity for retry")
				job.State = StateFailed
				job.ResultChan <- JobResult{Err: err}
				return
			}

			// Update tracking variables and the outer assignment so the defer
			// in runAssignment releases the correct (new) leases.
			currentBackendID = alt.BackendID
			currentHost = alt.Host
			currentModel = alt.ModelName
			currentBaseURL = s.BackendURLs[alt.BackendID]

			a.Host = alt.Host
			a.BackendID = alt.BackendID

			job.BackendID = alt.BackendID
			job.ConcreteModel = alt.ModelName
		}

		failedBackends[currentBackendID] = true

		// Build streaming chat request.
		chatReq := ollama.ChatRequest{
			Model:     currentModel,
			Messages:  job.Messages,
			Stream:    true,
			Options:   job.Options,
			Think:     job.Think,
			KeepAlive: job.KeepAlive,
			Tools:     job.Tools,
		}
		s.mergeBackendOptions(&chatReq, currentBackendID)

		// Send streaming request (context-aware).
		streamCh, err := ollama.SendChatStream(job.JobCtx, s.Client, currentBaseURL, &chatReq)
		if err != nil {
			// If the client cancelled, abort without recording a failure.
			if job.JobCtx != nil {
				select {
				case <-job.JobCtx.Done():
					job.State = StateAborted
					job.ResultChan <- JobResult{Err: job.JobCtx.Err()}
					return
				default:
				}
			}
			s.Stats.RecordFailure(BackendModelKey{
				BackendID: currentBackendID,
				ModelName: currentModel,
			})
			if s.Logger != nil {
				s.Logger.Warn("streaming request failed, will retry",
					logging.String("job_id", job.ID),
					logging.String("backend_id", currentBackendID),
					logging.String("error", err.Error()),
					logging.Int("attempt", attempt+1),
				)
			}
			continue // retry
		}

		// Read the first chunk. Hold it — don't send to StreamCh yet.
		// This implements retry allowed before first meaningful token.
		firstChunk, ok := <-streamCh
		if !ok {
			// Stream closed immediately.
			if job.JobCtx != nil {
				select {
				case <-job.JobCtx.Done():
					job.State = StateAborted
					job.ResultChan <- JobResult{Err: job.JobCtx.Err()}
					return
				default:
				}
			}
			s.Stats.RecordFailure(BackendModelKey{
				BackendID: currentBackendID,
				ModelName: currentModel,
			})
			continue
		}

		if firstChunk.Err != nil {
			if job.JobCtx != nil {
				select {
				case <-job.JobCtx.Done():
					job.State = StateAborted
					job.ResultChan <- JobResult{Err: job.JobCtx.Err()}
					return
				default:
				}
			}
			s.Stats.RecordFailure(BackendModelKey{
				BackendID: currentBackendID,
				ModelName: currentModel,
			})
			continue
		}

		// We got the first meaningful chunk. From this point, no more retries.
		// Record success will happen after all chunks are consumed.
		var finalResponse *ollama.ChatResponse
		var tps, coldLoad float64

		// Send the first chunk to the handler.
		select {
		case job.StreamCh <- &firstChunk:
		case <-job.JobCtx.Done():
			return // client disconnected
		}

		// Drain remaining chunks.
		for chunk := range streamCh {
			if chunk.Err != nil {
				// Error mid-stream: can't retry (already sent first chunk).
				if s.Logger != nil {
					s.Logger.Warn("streaming error mid-stream",
						logging.String("job_id", job.ID),
						logging.String("error", chunk.Err.Error()),
					)
				}
				// Client disconnect is abort, not failure.
				if job.JobCtx != nil {
					select {
					case <-job.JobCtx.Done():
						job.State = StateAborted
						job.ResultChan <- JobResult{Err: chunk.Err}
						return
					default:
					}
				}
				job.State = StateFailed
				job.ResultChan <- JobResult{Err: chunk.Err}
				return
			}

			if chunk.Response != nil && chunk.Response.Done {
				// Final chunk with metrics.
				finalResponse = chunk.Response
				tps = computeTPS(chunk.Response)
				coldLoad = computeColdLoad(chunk.Response)
			}

			// Send chunk to handler (non-blocking, respect context).
			select {
			case job.StreamCh <- &chunk:
			case <-job.JobCtx.Done():
				return
			}
		}

		// Stream completed successfully.
		s.Stats.RecordSuccess(BackendModelKey{
			BackendID: currentBackendID,
			ModelName: currentModel,
		}, tps, coldLoad)

		job.State = StateCompleted
		if finalResponse != nil {
			job.ResultChan <- JobResult{Response: finalResponse}
		} else {
			job.ResultChan <- JobResult{Response: &ollama.ChatResponse{Done: true}}
		}
		return
	}

	// All retries exhausted.
	err := fmt.Errorf("scheduler: streaming job failed after %d attempts", maxAttempts)
	job.State = StateFailed
	job.ResultChan <- JobResult{Err: err}
}

// findAlternativeBackend finds the best backend for a streaming retry, excluding
// previously-failed backends. It temporarily sets the job state to Pending
// because ValidAssignments only considers pending jobs.
func (s *Scheduler) findAlternativeBackend(job *Job, excludeBackends map[string]bool) *Assignment {
	// ValidAssignments only considers StatePending jobs. Streaming retries have
	// StateRunning, so temporarily set Pending for the lookup.
	originalState := job.State
	job.State = StatePending
	defer func() { job.State = originalState }()

	snapshots := s.Snapshots()
	pendingJobs := []*Job{job}

	// Generate valid assignments, then filter out excluded backends.
	allAssignments := s.Scorer.ValidAssignments(pendingJobs, snapshots)

	var best *Assignment
	for i := range allAssignments {
		a := &allAssignments[i]
		if excludeBackends[a.BackendID] {
			continue
		}
		a.Cost = s.Scorer.Score(a)
		if best == nil || a.Cost < best.Cost {
			best = a
		}
	}
	return best
}

// mergeBackendOptions applies per-backend overrides on top of the handler-built
// request options. Backend options (keep_alive, think, num_thread, num_ctx)
// override values already set on the ChatRequest.
func (s *Scheduler) mergeBackendOptions(req *ollama.ChatRequest, backendID string) {
	bc, ok := s.BackendConfigs[backendID]
	if !ok {
		return
	}
	if bc.KeepAlive != "" {
		req.KeepAlive = bc.KeepAlive
	}
	if bc.Think != nil {
		req.Think = bc.Think
	}
	if bc.Options != nil {
		if req.Options == nil {
			req.Options = &ollama.ChatOptions{}
		}
		if bc.Options.NumThread != 0 {
			req.Options.NumThread = bc.Options.NumThread
		}
		if bc.Options.NumCtx != 0 {
			req.Options.NumCtx = bc.Options.NumCtx
		}
		if bc.Options.TopK != 0 {
			req.Options.TopK = bc.Options.TopK
		}
		if bc.Options.RepeatPenalty != 0 {
			req.Options.RepeatPenalty = bc.Options.RepeatPenalty
		}
		if bc.Options.NumPredict != 0 {
			req.Options.NumPredict = bc.Options.NumPredict
		}
		if bc.Options.UseMmap != nil {
			req.Options.UseMmap = bc.Options.UseMmap
		}
	}
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
