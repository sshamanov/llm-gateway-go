package backend

import (
	"sync"
	"time"
)

// HealthState tracks the health status of a backend.
// The zero value is usable — defaults to unhealthy with no error.
type HealthState struct {
	healthy     bool
	lastError   string
	lastContact time.Time
	mu          sync.RWMutex
}

// SetHealthy marks the backend as healthy and records the contact time.
func (h *HealthState) SetHealthy(t time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthy = true
	h.lastError = ""
	h.lastContact = t
}

// SetUnhealthy marks the backend as unhealthy and stores the error string.
func (h *HealthState) SetUnhealthy(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthy = false
	if err != nil {
		h.lastError = err.Error()
	} else {
		h.lastError = ""
	}
	h.lastContact = time.Now()
}

// Snapshot returns a value-type copy of the health state for safe external reads.
func (h *HealthState) Snapshot() HealthSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return HealthSnapshot{
		Healthy:     h.healthy,
		LastError:   h.lastError,
		LastContact: h.lastContact,
	}
}

// HealthSnapshot is a value-type copy of health state.
type HealthSnapshot struct {
	Healthy     bool      `json:"healthy"`
	LastError   string    `json:"last_error"`
	LastContact time.Time `json:"last_contact"`
}
