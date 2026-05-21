package scheduler

import (
	"sync"
	"testing"

	"llm-go-proxy/internal/config"
)

func TestHostLease_Acquire_BelowCapacity(t *testing.T) {
	hosts := []config.HostConfig{
		{ID: "host1", MaxActiveJobs: 3},
	}
	m := NewHostLeaseManager(hosts)
	if !m.AcquireHost("host1") {
		t.Error("expected acquire to succeed when below capacity")
	}
	if got := m.HostActiveJobs("host1"); got != 1 {
		t.Errorf("expected active=1, got %d", got)
	}
}

func TestHostLease_Acquire_AtCapacity(t *testing.T) {
	hosts := []config.HostConfig{
		{ID: "host1", MaxActiveJobs: 1},
	}
	m := NewHostLeaseManager(hosts)
	if !m.AcquireHost("host1") {
		t.Fatal("expected first acquire to succeed")
	}
	if m.AcquireHost("host1") {
		t.Error("expected second acquire to fail when at capacity")
	}
}

func TestHostLease_Release_ThenAcquire(t *testing.T) {
	hosts := []config.HostConfig{
		{ID: "host1", MaxActiveJobs: 1},
	}
	m := NewHostLeaseManager(hosts)
	if !m.AcquireHost("host1") {
		t.Fatal("expected first acquire to succeed")
	}
	m.ReleaseHost("host1")
	if !m.AcquireHost("host1") {
		t.Error("expected acquire to succeed after release")
	}
}

func TestHostLease_DoubleRelease_Idempotent(t *testing.T) {
	hosts := []config.HostConfig{
		{ID: "host1", MaxActiveJobs: 2},
	}
	m := NewHostLeaseManager(hosts)
	m.AcquireHost("host1")
	m.ReleaseHost("host1")
	m.ReleaseHost("host1") // second release, should be no-op
	if got := m.HostActiveJobs("host1"); got != 0 {
		t.Errorf("expected active=0 after double release, got %d", got)
	}
}

func TestHostLease_UnknownHost_DefaultsToCapacity1(t *testing.T) {
	m := NewHostLeaseManager(nil) // no hosts configured
	if !m.AcquireHost("unknown") {
		t.Error("expected first acquire for unknown host to succeed (default capacity 1)")
	}
	if m.AcquireHost("unknown") {
		t.Error("expected second acquire for unknown host to fail (default capacity 1)")
	}
}

func TestHostLease_FreeCapacity(t *testing.T) {
	hosts := []config.HostConfig{
		{ID: "host1", MaxActiveJobs: 3},
	}
	m := NewHostLeaseManager(hosts)
	m.AcquireHost("host1")
	// capacity 3 - active 1 = 2
	if got := m.HostFreeCapacity("host1"); got != 2 {
		t.Errorf("expected free capacity=2, got %d", got)
	}
}

func TestHostLease_ConcurrentAcquire(t *testing.T) {
	hosts := []config.HostConfig{
		{ID: "host1", MaxActiveJobs: 1},
	}
	m := NewHostLeaseManager(hosts)

	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0

	// Channel to synchronize goroutine start
	ready := make(chan struct{})

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready // wait for the starting signal
			if m.AcquireHost("host1") {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	close(ready) // release all goroutines at once
	wg.Wait()

	if successes != 1 {
		t.Errorf("expected exactly 1 success, got %d", successes)
	}
}

func TestBackendLease_Acquire_BelowCapacity(t *testing.T) {
	backends := []config.OllamaBackendConfig{
		{ID: "backend1", MaxConcurrentRequests: 3},
	}
	m := NewBackendLeaseManager(backends)
	if !m.AcquireBackend("backend1") {
		t.Error("expected acquire to succeed when below capacity")
	}
	if got := m.BackendActiveJobs("backend1"); got != 1 {
		t.Errorf("expected active=1, got %d", got)
	}
}

func TestBackendLease_Acquire_AtCapacity(t *testing.T) {
	backends := []config.OllamaBackendConfig{
		{ID: "backend1", MaxConcurrentRequests: 1},
	}
	m := NewBackendLeaseManager(backends)
	if !m.AcquireBackend("backend1") {
		t.Fatal("expected first acquire to succeed")
	}
	if m.AcquireBackend("backend1") {
		t.Error("expected second acquire to fail when at capacity")
	}
}

func TestBackendLease_Release_ThenAcquire(t *testing.T) {
	backends := []config.OllamaBackendConfig{
		{ID: "backend1", MaxConcurrentRequests: 1},
	}
	m := NewBackendLeaseManager(backends)
	if !m.AcquireBackend("backend1") {
		t.Fatal("expected first acquire to succeed")
	}
	m.ReleaseBackend("backend1")
	if !m.AcquireBackend("backend1") {
		t.Error("expected acquire to succeed after release")
	}
}

func TestBackendLease_DoubleRelease_Idempotent(t *testing.T) {
	backends := []config.OllamaBackendConfig{
		{ID: "backend1", MaxConcurrentRequests: 2},
	}
	m := NewBackendLeaseManager(backends)
	m.AcquireBackend("backend1")
	m.ReleaseBackend("backend1")
	m.ReleaseBackend("backend1") // second release, should be no-op
	if got := m.BackendActiveJobs("backend1"); got != 0 {
		t.Errorf("expected active=0 after double release, got %d", got)
	}
}

func TestBackendLease_FreeCapacity(t *testing.T) {
	backends := []config.OllamaBackendConfig{
		{ID: "backend1", MaxConcurrentRequests: 3},
	}
	m := NewBackendLeaseManager(backends)
	m.AcquireBackend("backend1")
	// capacity 3 - active 1 = 2
	if got := m.BackendFreeCapacity("backend1"); got != 2 {
		t.Errorf("expected free capacity=2, got %d", got)
	}
}

func TestBackendLease_ConcurrentAcquire(t *testing.T) {
	backends := []config.OllamaBackendConfig{
		{ID: "backend1", MaxConcurrentRequests: 1},
	}
	m := NewBackendLeaseManager(backends)

	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0

	// Channel to synchronize goroutine start
	ready := make(chan struct{})

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready // wait for the starting signal
			if m.AcquireBackend("backend1") {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	close(ready) // release all goroutines at once
	wg.Wait()

	if successes != 1 {
		t.Errorf("expected exactly 1 success, got %d", successes)
	}
}
