package backend

import "testing"

func TestHostStatus_NoBackendsFallsBackToCapacity(t *testing.T) {
	tests := []struct {
		name       string
		activeJobs int
		capacity   int
		want       string
	}{
		{"idle", 0, 2, "available"},
		{"partial", 1, 2, "available"},
		{"full", 2, 2, "full"},
		{"over-capacity", 3, 2, "full"},
		{"zero-capacity", 0, 0, "available"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HostStatus(nil, "host-a", tt.activeJobs, tt.capacity); got != tt.want {
				t.Errorf("HostStatus(nil, host-a, %d, %d) = %q, want %q", tt.activeJobs, tt.capacity, got, tt.want)
			}
		})
	}
}

func TestHostStatus_AllBackendsDisabled(t *testing.T) {
	snaps := []BackendSnapshot{
		{ID: "b1", Host: "host-a", Enabled: false},
		{ID: "b2", Host: "host-a", Enabled: false},
		{ID: "other", Host: "host-b", Enabled: true, Healthy: true},
	}
	if got := HostStatus(snaps, "host-a", 0, 2); got != "disabled" {
		t.Errorf("HostStatus = %q, want %q", got, "disabled")
	}
}

func TestHostStatus_AllEnabledDown(t *testing.T) {
	snaps := []BackendSnapshot{
		{ID: "b1", Host: "host-a", Enabled: true, Healthy: false},
		{ID: "b2", Host: "host-a", Enabled: true, Healthy: false},
	}
	if got := HostStatus(snaps, "host-a", 0, 2); got != "down" {
		t.Errorf("HostStatus = %q, want %q", got, "down")
	}
}

func TestHostStatus_DegradedWhenSomeDown(t *testing.T) {
	snaps := []BackendSnapshot{
		{ID: "b1", Host: "host-a", Enabled: true, Healthy: true},
		{ID: "b2", Host: "host-a", Enabled: true, Healthy: false},
	}
	if got := HostStatus(snaps, "host-a", 0, 2); got != "degraded" {
		t.Errorf("HostStatus = %q, want %q", got, "degraded")
	}
}

func TestHostStatus_AllHealthyUsesCapacity(t *testing.T) {
	snaps := []BackendSnapshot{
		{ID: "b1", Host: "host-a", Enabled: true, Healthy: true},
		{ID: "b2", Host: "host-a", Enabled: true, Healthy: true},
	}
	if got := HostStatus(snaps, "host-a", 0, 2); got != "available" {
		t.Errorf("HostStatus idle = %q, want %q", got, "available")
	}
	if got := HostStatus(snaps, "host-a", 2, 2); got != "full" {
		t.Errorf("HostStatus full = %q, want %q", got, "full")
	}
}

func TestHostStatus_IgnoresOtherHostsBackends(t *testing.T) {
	snaps := []BackendSnapshot{
		{ID: "b1", Host: "host-a", Enabled: true, Healthy: true},
		{ID: "b2", Host: "host-b", Enabled: true, Healthy: false},
	}
	// host-b is down, but host-a must stay available.
	if got := HostStatus(snaps, "host-a", 0, 2); got != "available" {
		t.Errorf("HostStatus = %q, want %q", got, "available")
	}
}
