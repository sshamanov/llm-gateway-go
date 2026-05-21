package scheduler

import (
	"sync"

	"llm-go-proxy/internal/config"
)

// HostLeaseManager tracks and limits active jobs per host.
type HostLeaseManager struct {
	mu      sync.Mutex
	active  map[string]int32 // current active jobs per host
	maxJobs map[string]int32 // capacity per host from config
}

// NewHostLeaseManager creates a HostLeaseManager from host configs.
// Unknown hosts default to maxActiveJobs=1.
func NewHostLeaseManager(hosts []config.HostConfig) *HostLeaseManager {
	maxJobs := make(map[string]int32, len(hosts))
	for _, h := range hosts {
		maxJobs[h.ID] = int32(h.MaxActiveJobs)
	}
	return &HostLeaseManager{
		active:  make(map[string]int32),
		maxJobs: maxJobs,
	}
}

func (h *HostLeaseManager) maxForHost(hostID string) int32 {
	if max, ok := h.maxJobs[hostID]; ok {
		return max
	}
	return 1
}

// AcquireHost tries to acquire a slot on a host. Returns false if at capacity.
func (h *HostLeaseManager) AcquireHost(hostID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active[hostID] < h.maxForHost(hostID) {
		h.active[hostID]++
		return true
	}
	return false
}

// ReleaseHost releases a slot on a host. Idempotent: never goes negative.
func (h *HostLeaseManager) ReleaseHost(hostID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active[hostID] > 0 {
		h.active[hostID]--
	}
}

// HostActiveJobs returns the current number of active jobs on a host.
func (h *HostLeaseManager) HostActiveJobs(hostID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return int(h.active[hostID])
}

// HostFreeCapacity returns available slots on a host.
func (h *HostLeaseManager) HostFreeCapacity(hostID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return int(h.maxForHost(hostID) - h.active[hostID])
}

// BackendLeaseManager tracks and limits active jobs per backend.
type BackendLeaseManager struct {
	mu            sync.Mutex
	active        map[string]int32
	maxConcurrent map[string]int32
}

// NewBackendLeaseManager creates a BackendLeaseManager from backend configs.
func NewBackendLeaseManager(backends []config.OllamaBackendConfig) *BackendLeaseManager {
	maxConcurrent := make(map[string]int32, len(backends))
	for _, b := range backends {
		maxConcurrent[b.ID] = int32(b.MaxConcurrentRequests)
	}
	return &BackendLeaseManager{
		active:        make(map[string]int32),
		maxConcurrent: maxConcurrent,
	}
}

func (b *BackendLeaseManager) maxForBackend(backendID string) int32 {
	if max, ok := b.maxConcurrent[backendID]; ok {
		return max
	}
	return 1
}

// AcquireBackend tries to acquire a slot on a backend. Returns false if at capacity.
func (b *BackendLeaseManager) AcquireBackend(backendID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[backendID] < b.maxForBackend(backendID) {
		b.active[backendID]++
		return true
	}
	return false
}

// ReleaseBackend releases a slot on a backend. Idempotent: never goes negative.
func (b *BackendLeaseManager) ReleaseBackend(backendID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[backendID] > 0 {
		b.active[backendID]--
	}
}

// BackendActiveJobs returns the current number of active jobs on a backend.
func (b *BackendLeaseManager) BackendActiveJobs(backendID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.active[backendID])
}

// BackendFreeCapacity returns available slots on a backend.
func (b *BackendLeaseManager) BackendFreeCapacity(backendID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.maxForBackend(backendID) - b.active[backendID])
}
