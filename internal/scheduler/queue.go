package scheduler

import (
	"container/heap"
	"sync"
	"time"
)

// jobHeap implements heap.Interface for a max-heap of *Job,
// ordered by effective priority (higher = earlier dequeue).
type jobHeap struct {
	items          []*Job
	agingPerSecond float64
}

func (h *jobHeap) Len() int { return len(h.items) }

func (h *jobHeap) Less(i, j int) bool {
	epI := float64(h.items[i].Priority) + time.Since(h.items[i].CreatedAt).Seconds()*h.agingPerSecond
	epJ := float64(h.items[j].Priority) + time.Since(h.items[j].CreatedAt).Seconds()*h.agingPerSecond
	return epI > epJ // max-heap: higher effective priority dequeues first
}

func (h *jobHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *jobHeap) Push(x any) {
	h.items = append(h.items, x.(*Job))
}

func (h *jobHeap) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	h.items = old[:n-1]
	return item
}

// Queue is a concurrency-safe priority queue that uses aging to
// gradually boost the effective priority of waiting jobs.
type Queue struct {
	mu    sync.Mutex
	items *jobHeap
}

// NewQueue creates a new Queue. agingPerSecond controls how fast
// waiting jobs gain effective priority over time.
func NewQueue(agingPerSecond float64) *Queue {
	return &Queue{
		items: &jobHeap{
			items:          make([]*Job, 0),
			agingPerSecond: agingPerSecond,
		},
	}
}

// EffectivePriority returns the job's base priority plus an aging bonus
// proportional to how long the job has been waiting.
func (q *Queue) EffectivePriority(job *Job) float64 {
	return float64(job.Priority) + job.Duration().Seconds()*q.items.agingPerSecond
}

// Enqueue adds a job to the queue. The heap invariant is maintained.
func (q *Queue) Enqueue(job *Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	heap.Push(q.items, job)
}

// Dequeue removes and returns the highest-effective-priority job.
// Returns nil if the queue is empty.
func (q *Queue) Dequeue() *Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.items.Len() == 0 {
		return nil
	}
	return heap.Pop(q.items).(*Job)
}

// TopN returns at most n jobs without removing them from the queue.
// The returned slice is a copy; mutations do not affect the queue.
func (q *Queue) TopN(n int) []*Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	if n > q.items.Len() {
		n = q.items.Len()
	}
	result := make([]*Job, n)
	for i := 0; i < n; i++ {
		result[i] = q.items.items[i]
	}
	return result
}

// Remove removes a job by ID from the queue. Returns the removed job
// or nil if not found.
func (q *Queue) Remove(id string) *Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, job := range q.items.items {
		if job.ID == id {
			heap.Remove(q.items, i)
			return job
		}
	}
	return nil
}

// Len returns the total number of jobs in the queue (all states).
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.items.Len()
}

// PendingCount returns the number of jobs in Pending state.
func (q *Queue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := 0
	for _, job := range q.items.items {
		if job.State == StatePending {
			count++
		}
	}
	return count
}
