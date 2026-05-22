package metrics

import "sync"

// MetricType distinguishes between counter and gauge metric types.
type MetricType int

const (
	MetricTypeCounter MetricType = iota
	MetricTypeGauge
)

// Counter is a monotonically increasing cumulative metric.
type Counter struct {
	mu    sync.Mutex
	value float64
}

func (c *Counter) Inc()              { c.mu.Lock(); defer c.mu.Unlock(); c.value++ }
func (c *Counter) Add(delta float64) { c.mu.Lock(); defer c.mu.Unlock(); c.value += delta }
func (c *Counter) Value() float64    { c.mu.Lock(); defer c.mu.Unlock(); return c.value }

// Gauge is a metric that can go up and down.
type Gauge struct {
	mu    sync.Mutex
	value float64
}

func (g *Gauge) Set(v float64)      { g.mu.Lock(); defer g.mu.Unlock(); g.value = v }
func (g *Gauge) Add(delta float64)  { g.mu.Lock(); defer g.mu.Unlock(); g.value += delta }
func (g *Gauge) Value() float64     { g.mu.Lock(); defer g.mu.Unlock(); return g.value }

// MetricSample is a single labeled metric data point.
type MetricSample struct {
	Labels map[string]string
	Value  float64
}

// MetricFamily is a set of metric samples with the same name and type.
type MetricFamily struct {
	Name    string
	Help    string
	Type    MetricType
	Metrics []MetricSample
}

// Registry holds named metrics.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
	help     map[string]string
	types    map[string]MetricType
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		counters: make(map[string]*Counter),
		gauges:   make(map[string]*Gauge),
		help:     make(map[string]string),
		types:    make(map[string]MetricType),
	}
}

// RegisterCounter registers a new counter with the given name and help text.
// Panics if the name is already registered with a different type.
func (r *Registry) RegisterCounter(name, help string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.types[name]; ok && t != MetricTypeCounter {
		panic("metrics: " + name + " already registered with a different type")
	}
	c := &Counter{}
	r.counters[name] = c
	r.help[name] = help
	r.types[name] = MetricTypeCounter
	return c
}

// RegisterGauge registers a new gauge with the given name and help text.
// Panics if the name is already registered with a different type.
func (r *Registry) RegisterGauge(name, help string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.types[name]; ok && t != MetricTypeGauge {
		panic("metrics: " + name + " already registered with a different type")
	}
	g := &Gauge{}
	r.gauges[name] = g
	r.help[name] = help
	r.types[name] = MetricTypeGauge
	return g
}

// GetCounter returns a registered counter or nil.
func (r *Registry) GetCounter(name string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters[name]
}

// GetGauge returns a registered gauge or nil.
func (r *Registry) GetGauge(name string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gauges[name]
}

// Collect returns all registered metric families with their current values.
func (r *Registry) Collect() []MetricFamily {
	r.mu.Lock()
	defer r.mu.Unlock()

	families := make([]MetricFamily, 0, len(r.counters)+len(r.gauges))

	for name, c := range r.counters {
		families = append(families, MetricFamily{
			Name: name,
			Help: r.help[name],
			Type: MetricTypeCounter,
			Metrics: []MetricSample{
				{Value: c.Value()},
			},
		})
	}

	for name, g := range r.gauges {
		families = append(families, MetricFamily{
			Name: name,
			Help: r.help[name],
			Type: MetricTypeGauge,
			Metrics: []MetricSample{
				{Value: g.Value()},
			},
		})
	}

	return families
}
