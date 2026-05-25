package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llm-go-proxy/internal/backend"
	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/debugui"
	"llm-go-proxy/internal/documents"
	"llm-go-proxy/internal/httpapi"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/metrics"
	"llm-go-proxy/internal/scheduler"
	"llm-go-proxy/internal/storage"
)

// Harness orchestrates the entire mock E2E test environment.
type Harness struct {
	TempDir  string
	ProxyURL string
	Ollamas  []*FakeOllama
	Images   []*FakeImageBackend
	Cancel   context.CancelFunc
	ErrCh    <-chan error
	Config   *config.Config

	server   *http.Server
	registry *backend.Registry
	sched    *scheduler.Scheduler
	logger   *logging.Logger
	paths    storage.Paths
	ctx      context.Context
}

// NewHarness creates a new Harness with a temp directory.
func NewHarness() (*Harness, error) {
	tempDir, err := os.MkdirTemp("", "mocktest-*")
	if err != nil {
		return nil, fmt.Errorf("harness: create temp dir: %w", err)
	}
	return &Harness{
		TempDir: tempDir,
	}, nil
}

// StartFakeBackends creates and starts fake Ollama instances from configs.
func (h *Harness) StartFakeBackends(ollamaConfigs []FakeOllamaConfig, imageBackends int) error {
	for _, cfg := range ollamaConfigs {
		fo := NewFakeOllama(cfg)
		h.Ollamas = append(h.Ollamas, fo)
	}

	for i := 0; i < imageBackends; i++ {
		fi := NewFakeImageBackend()
		h.Images = append(h.Images, fi)
	}

	return nil
}

// WriteConfig generates config.json in the temp directory pointing at fake backends.
func (h *Harness) WriteConfig() error {
	configDir := filepath.Join(h.TempDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("harness: create config dir: %w", err)
	}

	cfg := config.DefaultConfig()

	// Point hosts at fake capacity.
	cfg.Hosts = []config.HostConfig{
		{ID: "h1", MaxActiveJobs: 4},
	}

	// Point Ollama backends at fake instances.
	cfg.OllamaBackends = nil
	for i, fo := range h.Ollamas {
		cfg.OllamaBackends = append(cfg.OllamaBackends, config.OllamaBackendConfig{
			ID:                   fo.ID,
			URL:                  fo.URL,
			Host:                 "h1",
			MaxConcurrentRequests: 3,
			Enabled:              true,
		})
		_ = i
	}

	// Point image backends at fake instances.
	cfg.ImageBackends = nil
	for i, fi := range h.Images {
		cfg.ImageBackends = append(cfg.ImageBackends, config.ImageBackendConfig{
			ID:                   fmt.Sprintf("img-%d", i),
			Type:                 "openai_compatible",
			URL:                  fi.URL,
			Host:                 "h1",
			MaxConcurrentRequests: 1,
			Enabled:              true,
		})
	}

	// Scheduler config tuned for testing.
	cfg.Scheduler.QueueMaxPending = 10
	cfg.Scheduler.TopNLookahead = 64
	cfg.Scheduler.Retry.MaxAttempts = 2

	// Documents config.
	cfg.Documents.Enabled = true
	cfg.Documents.PreparationWorkers = 1

	// Expose native models so FakeOllama models are visible.
	cfg.Models.ExposeNativeOllamaModels = true

	// Simple aliases for alias tests.
	cfg.Models.Aliases = []config.AliasConfig{
		{
			Name:         "my-alias",
			PrimaryModel: "llama3",
			BackupModels: []string{"llama3:backup"},
		},
	}

	h.Config = &cfg

	configPath := filepath.Join(configDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("harness: marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("harness: write config: %w", err)
	}

	return nil
}

// UpdateConfig re-writes the current h.Config to the config file.
// Use this to modify the config after WriteConfig but before StartProxy.
func (h *Harness) UpdateConfig() error {
	configPath := filepath.Join(h.TempDir, "config", "config.json")
	data, err := json.MarshalIndent(h.Config, "", "  ")
	if err != nil {
		return fmt.Errorf("harness: marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("harness: write config: %w", err)
	}
	return nil
}

// StartProxy starts the full proxy stack in-process (goroutine) using
// the generated config. It mirrors cmd/proxy/main.go exactly.
func (h *Harness) StartProxy() error {
	h.paths = storage.NewPaths(h.TempDir)
	if err := h.paths.EnsureDirs(); err != nil {
		return fmt.Errorf("harness: ensure dirs: %w", err)
	}

	cfg, err := config.LoadConfig(h.paths.ConfigFilePath())
	if err != nil {
		return fmt.Errorf("harness: load config: %w", err)
	}
	h.Config = &cfg

	h.logger = logging.NewLogger(logging.LevelInfo, "proxy")

	// Backend registry.
	h.registry = backend.NewRegistry(h.Config, h.logger)
	h.registry.Start()

	// Scheduler.
	backendURLs := make(map[string]string, len(cfg.OllamaBackends))
	backendConfigs := make(map[string]config.OllamaBackendConfig, len(cfg.OllamaBackends))
	for _, bc := range cfg.OllamaBackends {
		if bc.Enabled {
			backendURLs[bc.ID] = bc.URL
			backendConfigs[bc.ID] = bc
		}
	}
	schedHosts := scheduler.NewHostLeaseManager(cfg.Hosts)
	schedBackends := scheduler.NewBackendLeaseManager(cfg.OllamaBackends)
	for _, ib := range cfg.ImageBackends {
		schedBackends.AddBackend(ib.ID, int32(ib.MaxConcurrentRequests))
	}
	schedStats := scheduler.NewStatsTracker()
	schedScorer := scheduler.NewScorer(schedStats, schedHosts, schedBackends, cfg.Scheduler)
	h.sched = scheduler.NewScheduler(
		scheduler.NewQueue(cfg.Scheduler.AgingPerSecond),
		schedScorer,
		schedStats,
		http.DefaultClient,
		backendURLs,
		backendConfigs,
		h.logger,
		h.registry.BackendSnapshots,
	)
	h.sched.Start()

	// Document preparation queue.
	prepareQueue := documents.NewPrepareQueue(cfg.Documents.PreparationWorkers)
	prepareQueue.Start()

	// Document inference coordinator.
	docCoordinator := documents.NewCoordinator(h.sched, h.logger)

	// Metrics.
	metricsReg := metrics.NewRegistry()
	metricsCollector := metrics.NewCollector(metricsReg, schedStats, h.sched.Queue, schedHosts, schedBackends, h.sched, h.registry)
	metricsHandler := metrics.Handler(metricsCollector)

	// Debug UI.
	debugUIHandler := debugui.Handler()

	router := httpapi.NewRouter(
		h.logger, h.registry, h.sched, h.paths,
		prepareQueue, docCoordinator,
		cfg.Documents, cfg.ImageBackends,
		metricsHandler, debugUIHandler, h.Config,
	)

	// Listen on random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("harness: listen: %w", err)
	}
	h.ProxyURL = "http://" + listener.Addr().String()

	h.server = &http.Server{
		Handler:        router,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   time.Duration(cfg.Server.RequestTimeoutSeconds) * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	h.ctx, h.Cancel = context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	h.ErrCh = errCh

	go func() {
		if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	return nil
}

// WaitReady polls /healthz until it returns 200 or times out.
func (h *Harness) WaitReady() error {
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return fmt.Errorf("harness: proxy did not become ready within 10s")
		case err := <-h.ErrCh:
			if err != nil {
				return fmt.Errorf("harness: proxy failed to start: %w", err)
			}
		case <-ticker.C:
			resp, err := http.Get(h.ProxyURL + "/healthz")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

// Shutdown tears down the entire test environment.
func (h *Harness) Shutdown() {
	if h.Cancel != nil {
		h.Cancel()
	}

	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(ctx)
	}

	if h.sched != nil {
		h.sched.Stop()
	}

	if h.registry != nil {
		h.registry.Stop()
	}

	for _, fo := range h.Ollamas {
		fo.Server.Close()
	}
	for _, fi := range h.Images {
		fi.Server.Close()
	}

	if h.TempDir != "" {
		os.RemoveAll(h.TempDir)
	}
}

// Post sends a POST request with a JSON body to the proxy.
func (h *Harness) Post(path string, body interface{}) (*http.Response, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("harness: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, h.ProxyURL+path, bodyReader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	return resp, respBody, err
}

// Get sends a GET request to the proxy.
func (h *Harness) Get(path string) (*http.Response, []byte, error) {
	resp, err := http.Get(h.ProxyURL + path)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	return resp, respBody, err
}

// GetStream sends a POST request and returns SSE data lines from the response.
func (h *Harness) GetStream(path string, body interface{}) ([]string, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("harness: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, h.ProxyURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(bodyBytes), "\n")
	var dataLines []string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, line)
		}
	}
	return dataLines, nil
}

// AssertStatus checks the response status code.
func (h *Harness) AssertStatus(want int, resp *http.Response) error {
	if resp.StatusCode != want {
		return fmt.Errorf("expected status %d, got %d", want, resp.StatusCode)
	}
	return nil
}

// AssertJSONField checks a JSON field path in the response body.
// Path is a dot-separated key like "model" or "choices.0.message.content".
// Want is the expected string value.
func (h *Harness) AssertJSONField(path, want string, body []byte) error {
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("harness: unmarshal body: %w", err)
	}

	parts := strings.Split(path, ".")
	cur := data
	for _, part := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return fmt.Errorf("harness: field %q not found in response (expected map at %s)", path, part)
		}
		cur = m[part]
		if cur == nil {
			return fmt.Errorf("harness: field %q is null", path)
		}
	}

	got := fmt.Sprintf("%v", cur)
	if got != want {
		return fmt.Errorf("harness: field %q: expected %q, got %q", path, want, got)
	}
	return nil
}

// AssertMetrics checks that a Prometheus metric exists with the given value.
func (h *Harness) AssertMetrics(metric string, labels map[string]string, want float64) error {
	_, body, err := h.Get("/metrics")
	if err != nil {
		return fmt.Errorf("harness: get metrics: %w", err)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, metric) {
		return fmt.Errorf("harness: metric %q not found in /metrics output", metric)
	}

	// Build a simple label matcher.
	labelStr := ""
	if len(labels) > 0 {
		parts := make([]string, 0, len(labels))
		for k, v := range labels {
			parts = append(parts, fmt.Sprintf(`%s="%s"`, k, v))
		}
		labelStr = "{" + strings.Join(parts, ",") + "}"
	}

	// Search for the metric line with labels.
	lines := strings.Split(bodyStr, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, metric+labelStr) || (labelStr == "" && strings.HasPrefix(line, metric+" ")) {
			// Parse the value.
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				var got float64
				if _, err := fmt.Sscanf(fields[len(fields)-1], "%f", &got); err == nil {
					if got != want {
						return fmt.Errorf("harness: metric %q: expected %f, got %f", metric, want, got)
					}
					return nil
				}
			}
		}
		// Also check with label match
		if labelStr != "" && strings.HasPrefix(line, metric+"{") {
			if strings.Contains(line, labelStr) {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					var got float64
					if _, err := fmt.Sscanf(fields[len(fields)-1], "%f", &got); err == nil {
						if got != want {
							return fmt.Errorf("harness: metric %q: expected %f, got %f", metric, want, got)
						}
						return nil
					}
				}
			}
		}
	}

	return fmt.Errorf("harness: metric %q with labels %v not found in /metrics", metric, labels)
}

// AssertMetricsExists checks that a Prometheus metric name exists in /metrics output.
func (h *Harness) AssertMetricsExists(metric string) error {
	_, body, err := h.Get("/metrics")
	if err != nil {
		return fmt.Errorf("harness: get metrics: %w", err)
	}
	if !strings.Contains(string(body), metric) {
		return fmt.Errorf("harness: metric %q not found in /metrics output", metric)
	}
	return nil
}

// AssertQueueDepth checks the queue depth from /debug/queue.
func (h *Harness) AssertQueueDepth(want int) error {
	_, body, err := h.Get("/debug/queue")
	if err != nil {
		return fmt.Errorf("harness: get queue: %w", err)
	}

	var snap scheduler.QueueSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		return fmt.Errorf("harness: unmarshal queue: %w", err)
	}

	if snap.PendingCount != want {
		return fmt.Errorf("harness: queue depth: expected %d, got %d", want, snap.PendingCount)
	}
	return nil
}

// AssertHostCapacity checks host capacity from /debug/hosts.
func (h *Harness) AssertHostCapacity(hostID string, wantActive, wantCapacity int) error {
	_, body, err := h.Get("/debug/hosts")
	if err != nil {
		return fmt.Errorf("harness: get hosts: %w", err)
	}

	var hosts []scheduler.HostState
	var wrapper struct {
		Hosts []scheduler.HostState `json:"hosts"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		// Try direct array format.
		if err2 := json.Unmarshal(body, &hosts); err2 != nil {
			return fmt.Errorf("harness: unmarshal hosts: %w (wrapper: %w)", err2, err)
		}
	} else {
		hosts = wrapper.Hosts
	}

	for _, host := range hosts {
		if host.HostID == hostID {
			if host.ActiveJobs != wantActive {
				return fmt.Errorf("harness: host %q active: expected %d, got %d", hostID, wantActive, host.ActiveJobs)
			}
			if host.Capacity != wantCapacity {
				return fmt.Errorf("harness: host %q capacity: expected %d, got %d", hostID, wantCapacity, host.Capacity)
			}
			return nil
		}
	}

	return fmt.Errorf("harness: host %q not found in /debug/hosts", hostID)
}
