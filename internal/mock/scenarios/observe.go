package scenarios

import (
	"encoding/json"
	"fmt"

	"llm-go-proxy/internal/mock"
)

// MetricsEndpoint verifies that key metrics are present in /metrics output.
func MetricsEndpoint(h *mock.Harness) error {
	required := []string{
		"proxy_uptime_seconds",
		"proxy_queue_depth",
		"proxy_backend_up",
		"proxy_host_active_jobs",
	}

	for _, metric := range required {
		if err := h.AssertMetricsExists(metric); err != nil {
			return err
		}
	}

	return nil
}

// DebugEndpoints verifies the shape of each debug endpoint.
func DebugEndpoints(h *mock.Harness) error {
	// /debug/queue returns {"queue": {...}}
	_, body, err := h.Get("/debug/queue")
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}
	var qw struct {
		Queue map[string]interface{} `json:"queue"`
	}
	if err := json.Unmarshal(body, &qw); err != nil {
		return fmt.Errorf("queue: unmarshal: %w", err)
	}
	if qw.Queue == nil {
		return fmt.Errorf("queue: null response")
	}
	if qw.Queue["total_jobs"] == nil {
		return fmt.Errorf("queue: missing total_jobs")
	}

	// /debug/scheduler returns {"scheduler": {...}}
	_, body, err = h.Get("/debug/scheduler")
	if err != nil {
		return fmt.Errorf("scheduler: %w", err)
	}
	var sw struct {
		Scheduler map[string]interface{} `json:"scheduler"`
	}
	if err := json.Unmarshal(body, &sw); err != nil {
		return fmt.Errorf("scheduler: unmarshal: %w", err)
	}
	if sw.Scheduler == nil {
		return fmt.Errorf("scheduler: null response")
	}
	if sw.Scheduler["uptime_seconds"] == nil {
		return fmt.Errorf("scheduler: missing uptime_seconds")
	}

	// /debug/hosts returns {"hosts": [...]}
	_, body, err = h.Get("/debug/hosts")
	if err != nil {
		return fmt.Errorf("hosts: %w", err)
	}
	var hw struct {
		Hosts []interface{} `json:"hosts"`
	}
	if err := json.Unmarshal(body, &hw); err != nil {
		return fmt.Errorf("hosts: unmarshal: %w", err)
	}
	if hw.Hosts == nil {
		return fmt.Errorf("hosts: null response")
	}

	// /debug/config returns {"config": {...}}
	_, body, err = h.Get("/debug/config")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var cw struct {
		Config map[string]interface{} `json:"config"`
	}
	if err := json.Unmarshal(body, &cw); err != nil {
		return fmt.Errorf("config: unmarshal: %w", err)
	}
	if cw.Config == nil {
		return fmt.Errorf("config: null response")
	}

	return nil
}
