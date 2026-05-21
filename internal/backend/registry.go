package backend

import (
	"context"
	"net/http"
	"sync"
	"time"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/logging"
	"llm-go-proxy/internal/ollama"
)

// BackendState holds the runtime state for a single Ollama backend.
// Fields are protected by the embedded RWMutex for concurrent access from
// poll goroutines and HTTP handler goroutines.
type BackendState struct {
	Config          config.OllamaBackendConfig
	Health          HealthState
	AvailableModels []string
	LoadedModels    []string
	mu              sync.RWMutex
}

// BackendSnapshot is a value-type copy of BackendState for safe external reads.
// It carries no mutex and must not be modified after creation.
type BackendSnapshot struct {
	ID              string    `json:"id"`
	URL             string    `json:"url"`
	Host            string    `json:"host"`
	Enabled         bool      `json:"enabled"`
	Healthy         bool      `json:"healthy"`
	LastError       string    `json:"last_error"`
	LastContact     time.Time `json:"last_contact"`
	AvailableModels []string  `json:"available_models"`
	LoadedModels    []string  `json:"loaded_models"`
}

// Snapshot returns a value-type copy of the backend state (under RLock).
// The returned copy owns its own slice data and is safe to read without locking.
func (b *BackendState) Snapshot() BackendSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()

	healthSnap := b.Health.Snapshot()

	availModels := make([]string, len(b.AvailableModels))
	copy(availModels, b.AvailableModels)
	loadedModels := make([]string, len(b.LoadedModels))
	copy(loadedModels, b.LoadedModels)

	return BackendSnapshot{
		ID:              b.Config.ID,
		URL:             b.Config.URL,
		Host:            b.Config.Host,
		Enabled:         b.Config.Enabled,
		Healthy:         healthSnap.Healthy,
		LastError:       healthSnap.LastError,
		LastContact:     healthSnap.LastContact,
		AvailableModels: availModels,
		LoadedModels:    loadedModels,
	}
}

// Registry is the central orchestrator that manages backend discovery and health
// polling. It polls each enabled Ollama backend for /api/tags (every 60s) and
// /api/ps (every 5s), updating model lists and health state.
type Registry struct {
	backends     map[string]*BackendState
	modelsConfig config.ModelsConfig
	ollamaClient *http.Client
	logger       *logging.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// NewRegistry creates a Registry from config. It creates a BackendState for each
// enabled OllamaBackendConfig and creates the HTTP client via ollama.NewHTTPClient().
func NewRegistry(cfg *config.Config, logger *logging.Logger) *Registry {
	backends := make(map[string]*BackendState)
	for _, bc := range cfg.OllamaBackends {
		if !bc.Enabled {
			continue
		}
		backends[bc.ID] = &BackendState{
			Config: bc,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Registry{
		backends:     backends,
		modelsConfig: cfg.Models,
		ollamaClient: ollama.NewHTTPClient(),
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start launches one polling goroutine per backend. Each goroutine runs
// immediate initial polls and then polls on ticker intervals. Goroutines
// exit when the registry context is cancelled (via Stop).
func (r *Registry) Start() {
	if r == nil {
		return
	}
	for _, backend := range r.backends {
		r.wg.Add(1)
		go r.pollBackend(backend)
	}
}

// Stop cancels the registry context and waits for all poll goroutines to exit.
func (r *Registry) Stop() {
	if r == nil {
		return
	}
	r.cancel()
	r.wg.Wait()
}

// BackendSnapshots returns value-type copies of all backend states (under RLock
// on each). Returns nil if registry is nil.
func (r *Registry) BackendSnapshots() []BackendSnapshot {
	if r == nil {
		return nil
	}
	snapshots := make([]BackendSnapshot, 0, len(r.backends))
	for _, backend := range r.backends {
		snapshots = append(snapshots, backend.Snapshot())
	}
	return snapshots
}

// ModelsConfig returns the stored models configuration.
// Returns zero value if registry is nil.
func (r *Registry) ModelsConfig() config.ModelsConfig {
	if r == nil {
		return config.ModelsConfig{}
	}
	return r.modelsConfig
}

// pollBackend runs the polling loop for a single backend. It performs immediate
// initial polls for both /api/tags and /api/ps, then continues on tickers until
// the registry context is cancelled.
func (r *Registry) pollBackend(backend *BackendState) {
	defer r.wg.Done()

	tagsTicker := time.NewTicker(60 * time.Second)
	psTicker := time.NewTicker(5 * time.Second)
	defer tagsTicker.Stop()
	defer psTicker.Stop()

	pollTags := func() {
		tags, err := ollama.FetchTags(r.ollamaClient, backend.Config.URL)
		if err != nil {
			r.logger.Warn("backend poll tags failed",
				logging.String("backend_id", backend.Config.ID),
				logging.String("error", err.Error()),
			)
			backend.Health.SetUnhealthy(err)
			return
		}
		names := ModelNamesFromTags(tags)

		backend.mu.Lock()
		backend.AvailableModels = names
		backend.mu.Unlock()

		backend.Health.SetHealthy(time.Now())
	}

	pollPS := func() {
		ps, err := ollama.FetchPS(r.ollamaClient, backend.Config.URL)
		if err != nil {
			r.logger.Warn("backend poll ps failed",
				logging.String("backend_id", backend.Config.ID),
				logging.String("error", err.Error()),
			)
			backend.Health.SetUnhealthy(err)
			return
		}
		names := ModelNamesFromPS(ps)

		backend.mu.Lock()
		backend.LoadedModels = names
		backend.mu.Unlock()

		backend.Health.SetHealthy(time.Now())
	}

	// Initial polls run immediately on start.
	pollTags()
	pollPS()

	for {
		select {
		case <-tagsTicker.C:
			pollTags()
		case <-psTicker.C:
			pollPS()
		case <-r.ctx.Done():
			return
		}
	}
}
