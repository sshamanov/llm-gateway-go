package metrics

import (
	"sort"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/scheduler"
)

// Collector gathers all metrics at scrape time by reading state from the
// scheduler, stats tracker, and backend registry.
type Collector struct {
	Reg      *Registry
	Stats    *scheduler.StatsTracker
	Queue    *scheduler.Queue
	Hosts    *scheduler.HostLeaseManager
	Backends *scheduler.BackendLeaseManager
	Sched    *scheduler.Scheduler
	Registry *backend.Registry
}

// NewCollector creates a new Collector with references to all metric sources.
func NewCollector(
	reg *Registry,
	stats *scheduler.StatsTracker,
	queue *scheduler.Queue,
	hosts *scheduler.HostLeaseManager,
	backends *scheduler.BackendLeaseManager,
	sched *scheduler.Scheduler,
	backendReg *backend.Registry,
) *Collector {
	return &Collector{
		Reg:      reg,
		Stats:    stats,
		Queue:    queue,
		Hosts:    hosts,
		Backends: backends,
		Sched:    sched,
		Registry: backendReg,
	}
}

// Collect returns all metric families: registry counters/gauges + dynamic state.
func (c *Collector) Collect() []MetricFamily {
	var families []MetricFamily

	// 1. Collect static registered metrics from the registry.
	if c.Reg != nil {
		families = c.Reg.Collect()
	}

	// 2. Add dynamic state metrics.
	c.collectUptime(&families)
	c.collectQueueMetrics(&families)
	c.collectBackendMetrics(&families)
	c.collectBackendLeaseMetrics(&families)
	c.collectHostMetrics(&families)
	c.collectStatsMetrics(&families)

	return families
}

func (c *Collector) collectUptime(families *[]MetricFamily) {
	if c.Sched == nil {
		return
	}
	uptime := time.Since(c.Sched.StartedAt).Seconds()
	*families = append(*families, MetricFamily{
		Name: "proxy_uptime_seconds",
		Help: "Proxy uptime in seconds",
		Type: MetricTypeGauge,
		Metrics: []MetricSample{
			{Value: uptime},
		},
	})
}

func (c *Collector) collectQueueMetrics(families *[]MetricFamily) {
	if c.Queue == nil {
		return
	}
	snap := c.Queue.Snapshot(999999)

	// proxy_queue_depth{kind}
	depthFamily := MetricFamily{
		Name: "proxy_queue_depth",
		Help: "Current queue depth by job kind",
		Type: MetricTypeGauge,
	}

	// proxy_queue_wait_seconds{kind}
	waitFamily := MetricFamily{
		Name: "proxy_queue_wait_seconds",
		Help: "Oldest queue wait time in seconds by job kind",
		Type: MetricTypeGauge,
	}

	// proxy_jobs_started_total{kind}
	startedFamily := MetricFamily{
		Name: "proxy_jobs_started_total",
		Help: "Total jobs started by kind",
		Type: MetricTypeGauge,
	}

	// proxy_jobs_completed_total{kind}
	completedFamily := MetricFamily{
		Name: "proxy_jobs_completed_total",
		Help: "Total jobs completed by kind",
		Type: MetricTypeGauge,
	}

	// proxy_jobs_failed_total{kind}
	failedFamily := MetricFamily{
		Name: "proxy_jobs_failed_total",
		Help: "Total jobs failed by kind",
		Type: MetricTypeGauge,
	}

	// Determine the dominant kind (most jobs) for wait time approximation.
	var dominantKind string
	maxCount := 0
	kinds := make([]string, 0, len(snap.ByKind))
	for k, count := range snap.ByKind {
		kinds = append(kinds, k)
		if count > maxCount {
			maxCount = count
			dominantKind = k
		}
	}
	sort.Strings(kinds)

	oldestWaitSec := float64(snap.OldestWaitMs) / 1000.0

	for _, kind := range kinds {
		count := snap.ByKind[kind]

		depthFamily.Metrics = append(depthFamily.Metrics, MetricSample{
			Labels: map[string]string{"kind": kind},
			Value:  float64(count),
		})

		// Wait seconds: only the dominant kind gets the actual oldest wait time.
		waitVal := 0.0
		if kind == dominantKind {
			waitVal = oldestWaitSec
		}
		waitFamily.Metrics = append(waitFamily.Metrics, MetricSample{
			Labels: map[string]string{"kind": kind},
			Value:  waitVal,
		})

		// Started: current count by kind (approximation as gauge).
		startedFamily.Metrics = append(startedFamily.Metrics, MetricSample{
			Labels: map[string]string{"kind": kind},
			Value:  float64(count),
		})

		// Completed: placeholder (no completion tracking in v1).
		completedFamily.Metrics = append(completedFamily.Metrics, MetricSample{
			Labels: map[string]string{"kind": kind},
			Value:  0,
		})

		// Failed: placeholder.
		failedFamily.Metrics = append(failedFamily.Metrics, MetricSample{
			Labels: map[string]string{"kind": kind},
			Value:  0,
		})
	}

	*families = append(*families, depthFamily, waitFamily, startedFamily, completedFamily, failedFamily)
}

func (c *Collector) collectBackendMetrics(families *[]MetricFamily) {
	if c.Registry == nil {
		return
	}
	snapshots := c.Registry.BackendSnapshots()

	// proxy_backend_up{backend}
	upFamily := MetricFamily{
		Name: "proxy_backend_up",
		Help: "Backend health status (1=healthy, 0=unhealthy)",
		Type: MetricTypeGauge,
	}

	// proxy_backend_disabled{backend}
	disabledFamily := MetricFamily{
		Name: "proxy_backend_disabled",
		Help: "Backend disabled status (1=disabled, 0=enabled)",
		Type: MetricTypeGauge,
	}

	// Sort snapshots by ID for deterministic output.
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].ID < snapshots[j].ID
	})

	for _, snap := range snapshots {
		healthyVal := 0.0
		if snap.Healthy {
			healthyVal = 1.0
		}
		upFamily.Metrics = append(upFamily.Metrics, MetricSample{
			Labels: map[string]string{"backend": snap.ID},
			Value:  healthyVal,
		})

		disabledVal := 0.0
		if !snap.Enabled {
			disabledVal = 1.0
		}
		disabledFamily.Metrics = append(disabledFamily.Metrics, MetricSample{
			Labels: map[string]string{"backend": snap.ID},
			Value:  disabledVal,
		})
	}

	*families = append(*families, upFamily, disabledFamily)
}

func (c *Collector) collectBackendLeaseMetrics(families *[]MetricFamily) {
	if c.Backends == nil {
		return
	}
	states := c.Backends.States()

	// Sort by backend ID for deterministic output.
	sort.Slice(states, func(i, j int) bool {
		return states[i].BackendID < states[j].BackendID
	})

	activeFamily := MetricFamily{
		Name: "proxy_backend_active_jobs",
		Help: "Current active jobs per backend",
		Type: MetricTypeGauge,
	}
	for _, state := range states {
		activeFamily.Metrics = append(activeFamily.Metrics, MetricSample{
			Labels: map[string]string{"backend": state.BackendID},
			Value:  float64(state.ActiveJobs),
		})
	}

	*families = append(*families, activeFamily)
}

func (c *Collector) collectHostMetrics(families *[]MetricFamily) {
	if c.Hosts == nil {
		return
	}
	states := c.Hosts.States()

	// Sort by host ID for deterministic output.
	sort.Slice(states, func(i, j int) bool {
		return states[i].HostID < states[j].HostID
	})

	activeFamily := MetricFamily{
		Name: "proxy_host_active_jobs",
		Help: "Current active jobs per host",
		Type: MetricTypeGauge,
	}

	capacityFamily := MetricFamily{
		Name: "proxy_host_capacity",
		Help: "Maximum job capacity per host",
		Type: MetricTypeGauge,
	}

	for _, state := range states {
		activeFamily.Metrics = append(activeFamily.Metrics, MetricSample{
			Labels: map[string]string{"host": state.HostID},
			Value:  float64(state.ActiveJobs),
		})
		capacityFamily.Metrics = append(capacityFamily.Metrics, MetricSample{
			Labels: map[string]string{"host": state.HostID},
			Value:  float64(state.Capacity),
		})
	}

	*families = append(*families, activeFamily, capacityFamily)
}

func (c *Collector) collectStatsMetrics(families *[]MetricFamily) {
	if c.Stats == nil {
		return
	}
	allStats := c.Stats.SnapshotAll()

	tpsFamily := MetricFamily{
		Name: "proxy_backend_model_tps",
		Help: "Learned tokens per second per backend and model",
		Type: MetricTypeGauge,
	}

	coldLoadFamily := MetricFamily{
		Name: "proxy_backend_model_cold_load_seconds",
		Help: "Learned cold load time in seconds per backend and model",
		Type: MetricTypeGauge,
	}

	failuresFamily := MetricFamily{
		Name: "proxy_backend_failures_total",
		Help: "Backend model failure count by scope",
		Type: MetricTypeGauge,
	}

	// Sort keys by backend ID then model name for deterministic output.
	keys := make([]scheduler.BackendModelKey, 0, len(allStats))
	for k := range allStats {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].BackendID != keys[j].BackendID {
			return keys[i].BackendID < keys[j].BackendID
		}
		return keys[i].ModelName < keys[j].ModelName
	})

	for _, key := range keys {
		stat := allStats[key]

		tpsFamily.Metrics = append(tpsFamily.Metrics, MetricSample{
			Labels: map[string]string{
				"backend": key.BackendID,
				"model":   key.ModelName,
			},
			Value: stat.AvgTokensPerSecond,
		})

		coldLoadFamily.Metrics = append(coldLoadFamily.Metrics, MetricSample{
			Labels: map[string]string{
				"backend": key.BackendID,
				"model":   key.ModelName,
			},
			Value: stat.AvgColdLoadTime,
		})

		failuresFamily.Metrics = append(failuresFamily.Metrics, MetricSample{
			Labels: map[string]string{
				"backend": key.BackendID,
				"model":   key.ModelName,
				"scope":   "consecutive",
			},
			Value: float64(stat.ConsecutiveFailures),
		})
	}

	*families = append(*families, tpsFamily, coldLoadFamily, failuresFamily)
}
