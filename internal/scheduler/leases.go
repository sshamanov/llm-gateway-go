package scheduler

import (
	"sort"
	"sync"

	"llm-go-proxy/internal/config"
)

// HostLeaseManager tracks and limits active jobs per host.
type HostLeaseManager struct {
	mu      sync.Mutex
	active  map[string]int32 // current active jobs per host
	maxJobs map[string]int32 // capacity per host from config
}

// NewHostLeaseManager creates a HostLeaseManager from host configs and
// optional backend host IDs. Backend hosts not already in the explicit
// hosts list are added with maxActiveJobs=1 so minimal configs can omit
// the hosts section entirely.
// Unknown hosts (not in config and not in backendHosts) also default to
// maxActiveJobs=1 via maxForHost.
func NewHostLeaseManager(hosts []config.HostConfig, backendHosts ...string) *HostLeaseManager {
	maxJobs := make(map[string]int32, len(hosts)+len(backendHosts))
	for _, h := range hosts {
		maxJobs[h.ID] = int32(h.MaxActiveJobs)
	}
	for _, hostID := range backendHosts {
		if _, exists := maxJobs[hostID]; !exists {
			maxJobs[hostID] = 1
		}
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

// AddBackend registers a backend with the given ID and max concurrent requests.
// This allows non-Ollama backends (e.g., image, audio) to participate in
// capacity tracking after construction.
func (b *BackendLeaseManager) AddBackend(id string, maxConcurrent int32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maxConcurrent[id] = maxConcurrent
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

// HostState is a point-in-time snapshot of host capacity.
type HostState struct {
	HostID     string `json:"host_id"`
	ActiveJobs int    `json:"active_jobs"`
	Capacity   int    `json:"capacity"`
}

// States returns a slice of host states for all known hosts.
func (h *HostLeaseManager) States() []HostState {
	h.mu.Lock()
	defer h.mu.Unlock()
	states := make([]HostState, 0, len(h.maxJobs))
	for hostID, max := range h.maxJobs {
		states = append(states, HostState{
			HostID:     hostID,
			ActiveJobs: int(h.active[hostID]),
			Capacity:   int(max),
		})
	}
	sort.Slice(states, func(i, j int) bool { return states[i].HostID < states[j].HostID })
	return states
}

// BackendLeaseState is a point-in-time snapshot of backend capacity.
type BackendLeaseState struct {
	BackendID  string `json:"backend_id"`
	ActiveJobs int    `json:"active_jobs"`
	Capacity   int    `json:"capacity"`
}

// States returns a slice of backend capacity states for all known backends.
func (b *BackendLeaseManager) States() []BackendLeaseState {
	b.mu.Lock()
	defer b.mu.Unlock()
	states := make([]BackendLeaseState, 0, len(b.maxConcurrent))
	for backendID, max := range b.maxConcurrent {
		states = append(states, BackendLeaseState{
			BackendID:  backendID,
			ActiveJobs: int(b.active[backendID]),
			Capacity:   int(max),
		})
	}
	sort.Slice(states, func(i, j int) bool { return states[i].BackendID < states[j].BackendID })
	return states
}
