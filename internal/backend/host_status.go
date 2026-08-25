package backend

// capacityStatus derives a host status from capacity alone. It is used when a
// host has no tracked backends (or all its backends are healthy), so only job
// load can drive the status.
func capacityStatus(activeJobs, capacity int) string {
	if capacity > 0 && activeJobs >= capacity {
		return "full"
	}
	return "available"
}

// HostStatus computes the debug-UI status for a host given its backends.
// It is a pure function so the debug UI and its tests stay deterministic.
//
// Status semantics:
//
//	disabled  — every backend on the host is disabled in config
//	down      — backends exist but none of the enabled ones is healthy
//	degraded  — at least one enabled backend is healthy and at least one is down
//	full      — all enabled backends healthy and the host is at capacity
//	available — all enabled backends healthy and the host has free capacity
//
// A host with no tracked backends falls back to capacity-only status so
// capacity is still visible in the debug UI.
func HostStatus(snapshots []BackendSnapshot, hostID string, activeJobs, capacity int) string {
	foundAny := false
	anyEnabled := false
	anyHealthy := false
	anyDown := false
	for _, s := range snapshots {
		if s.Host != hostID {
			continue
		}
		foundAny = true
		if !s.Enabled {
			continue
		}
		anyEnabled = true
		if s.Healthy {
			anyHealthy = true
		} else {
			anyDown = true
		}
	}

	if !foundAny {
		return capacityStatus(activeJobs, capacity)
	}
	if !anyEnabled {
		return "disabled"
	}
	if !anyHealthy {
		return "down"
	}
	if anyDown {
		return "degraded"
	}
	return capacityStatus(activeJobs, capacity)
}
