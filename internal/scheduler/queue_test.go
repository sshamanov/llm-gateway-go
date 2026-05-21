package scheduler

import (
	"sync"
	"testing"
	"time"
)

func newTestJob(id string, kind JobKind, priority int) *Job {
	now := time.Now()
	return &Job{
		ID:        id,
		Kind:      kind,
		Priority:  priority,
		State:     StatePending,
		CreatedAt: now,
	}
}

func TestQueue_PriorityOrdering(t *testing.T) {
	q := NewQueue(0.05)

	doc := newTestJob("doc1", KindDocument, KindDocument.Priority())
	chat := newTestJob("chat1", KindChat, KindChat.Priority())

	q.Enqueue(doc)
	q.Enqueue(chat)

	got := q.Dequeue()
	if got == nil {
		t.Fatal("expected a job, got nil")
	}
	if got.ID != "chat1" {
		t.Errorf("expected chat1 (higher priority) first, got %s", got.ID)
	}

	got = q.Dequeue()
	if got == nil {
		t.Fatal("expected a job, got nil")
	}
	if got.ID != "doc1" {
		t.Errorf("expected doc1 second, got %s", got.ID)
	}
}

func TestQueue_Aging(t *testing.T) {
	// Use aging with a manual CreatedAt offset so the old job outranks a
	// fresh high-priority job.
	q := NewQueue(100.0)

	// Document(30) aged 1s: effective = 30 + 1.0*100 = 130
	oldJob := newTestJob("old", KindDocument, KindDocument.Priority())
	oldJob.CreatedAt = time.Now().Add(-1 * time.Second)

	// Chat(100) fresh: effective = 100 + 0*100 = 100
	newJob := newTestJob("new", KindChat, KindChat.Priority())

	q.Enqueue(oldJob)
	q.Enqueue(newJob)

	got := q.Dequeue()
	if got == nil {
		t.Fatal("expected a job, got nil")
	}
	if got.ID != "old" {
		t.Errorf("expected old (aged) job to dequeue first, got %s", got.ID)
	}
}

func TestQueue_Aging_WithSleep(t *testing.T) {
	// Aging via real wall-clock sleep.
	// With aging=2000 and 100ms sleep:
	//   lowPri effective ≈ 30 + 0.1*2000 = 230
	//   highPri effective = 100
	q := NewQueue(2000.0)

	lowPri := newTestJob("low", KindDocument, KindDocument.Priority())
	q.Enqueue(lowPri)
	time.Sleep(100 * time.Millisecond)
	highPri := newTestJob("high", KindChat, KindChat.Priority())
	q.Enqueue(highPri)

	got := q.Dequeue()
	if got == nil {
		t.Fatal("expected a job, got nil")
	}
	if got.ID != "low" {
		t.Errorf("expected low (aged) to dequeue first, got %s", got.ID)
	}
}

func TestQueue_TopN_DoesNotMutate(t *testing.T) {
	q := NewQueue(0.05)
	q.Enqueue(newTestJob("a", KindChat, KindChat.Priority()))
	q.Enqueue(newTestJob("b", KindTool, KindTool.Priority()))
	q.Enqueue(newTestJob("c", KindDocument, KindDocument.Priority()))

	top := q.TopN(2)
	if len(top) != 2 {
		t.Errorf("expected 2 jobs from TopN, got %d", len(top))
	}
	if q.Len() != 3 {
		t.Errorf("expected queue length 3 after TopN, got %d", q.Len())
	}
}

func TestQueue_TopN_ReturnsAtMostN(t *testing.T) {
	q := NewQueue(0.05)
	q.Enqueue(newTestJob("only", KindChat, KindChat.Priority()))

	top := q.TopN(5)
	if len(top) != 1 {
		t.Errorf("expected 1 job from TopN(5) with 1 enqueued, got %d", len(top))
	}
}

func TestQueue_Remove(t *testing.T) {
	q := NewQueue(0.05)
	a := newTestJob("a", KindChat, KindChat.Priority())
	b := newTestJob("b", KindDocument, KindDocument.Priority())
	q.Enqueue(a)
	q.Enqueue(b)

	removed := q.Remove("a")
	if removed == nil {
		t.Fatal("expected removed job, got nil")
	}
	if removed.ID != "a" {
		t.Errorf("expected removed job ID a, got %s", removed.ID)
	}
	if q.Len() != 1 {
		t.Errorf("expected queue length 1 after remove, got %d", q.Len())
	}

	got := q.Dequeue()
	if got == nil {
		t.Fatal("expected a job, got nil")
	}
	if got.ID != "b" {
		t.Errorf("expected remaining job b, got %s", got.ID)
	}
}

func TestQueue_Remove_NotFound(t *testing.T) {
	q := NewQueue(0.05)
	q.Enqueue(newTestJob("x", KindChat, KindChat.Priority()))

	removed := q.Remove("nonexistent")
	if removed != nil {
		t.Errorf("expected nil for non-existent ID, got %v", removed)
	}
}

func TestQueue_Dequeue_Empty(t *testing.T) {
	q := NewQueue(0.05)
	got := q.Dequeue()
	if got != nil {
		t.Errorf("expected nil from empty queue, got %v", got)
	}
}

func TestQueue_PendingCount(t *testing.T) {
	q := NewQueue(0.05)
	q.Enqueue(newTestJob("a", KindChat, KindChat.Priority()))
	q.Enqueue(newTestJob("b", KindTool, KindTool.Priority()))
	q.Enqueue(newTestJob("c", KindDocument, KindDocument.Priority()))

	// Mark one job as Running.
	items := q.TopN(3)
	for _, job := range items {
		if job.ID == "b" {
			job.State = StateRunning
			break
		}
	}

	if got := q.PendingCount(); got != 2 {
		t.Errorf("expected PendingCount 2, got %d", got)
	}
}

func TestQueue_ConcurrentSafety(t *testing.T) {
	q := NewQueue(0.05)
	var wg sync.WaitGroup

	// 10 goroutines enqueuing.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			q.Enqueue(newTestJob(id, KindChat, KindChat.Priority()))
		}(i)
	}
	wg.Wait()

	// 10 goroutines dequeuing.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.Dequeue()
		}()
	}
	wg.Wait()

	// Queue should be empty now.
	if q.Len() != 0 {
		t.Errorf("expected empty queue after concurrent enqueue/dequeue, got %d", q.Len())
	}
}

func TestQueue_ConcurrentEnqueue(t *testing.T) {
	q := NewQueue(0.05)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			job := newTestJob(string(rune('a'+i%26)), KindChat, KindChat.Priority())
			q.Enqueue(job)
		}(i)
	}
	wg.Wait()

	if q.Len() != 100 {
		t.Errorf("expected 100 jobs in queue, got %d", q.Len())
	}
}
