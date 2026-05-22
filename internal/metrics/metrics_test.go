package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCounter(t *testing.T) {
	c := &Counter{}
	if c.Value() != 0 {
		t.Fatalf("expected 0, got %f", c.Value())
	}
	c.Inc()
	if c.Value() != 1 {
		t.Fatalf("expected 1, got %f", c.Value())
	}
	c.Add(2.5)
	if c.Value() != 3.5 {
		t.Fatalf("expected 3.5, got %f", c.Value())
	}
}

func TestGauge(t *testing.T) {
	g := &Gauge{}
	if g.Value() != 0 {
		t.Fatalf("expected 0, got %f", g.Value())
	}
	g.Set(42)
	if g.Value() != 42 {
		t.Fatalf("expected 42, got %f", g.Value())
	}
	g.Add(-10)
	if g.Value() != 32 {
		t.Fatalf("expected 32, got %f", g.Value())
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()

	c := r.RegisterCounter("test_counter", "A test counter")
	if c == nil {
		t.Fatal("expected non-nil counter")
	}
	c.Inc()

	g := r.RegisterGauge("test_gauge", "A test gauge")
	if g == nil {
		t.Fatal("expected non-nil gauge")
	}
	g.Set(100)

	// GetCounter retrieves a registered counter.
	if got := r.GetCounter("test_counter"); got == nil {
		t.Fatal("expected to find counter")
	}
	if got := r.GetCounter("nonexistent"); got != nil {
		t.Fatal("expected nil for nonexistent counter")
	}

	// GetGauge retrieves a registered gauge.
	if got := r.GetGauge("test_gauge"); got == nil {
		t.Fatal("expected to find gauge")
	}
	if got := r.GetGauge("nonexistent"); got != nil {
		t.Fatal("expected nil for nonexistent gauge")
	}

	// Collect returns all families with current values.
	families := r.Collect()
	if len(families) != 2 {
		t.Fatalf("expected 2 families, got %d", len(families))
	}

	for _, f := range families {
		switch f.Name {
		case "test_counter":
			if f.Type != MetricTypeCounter {
				t.Fatalf("expected counter type")
			}
			if f.Help != "A test counter" {
				t.Fatalf("expected help text 'A test counter', got %q", f.Help)
			}
			if len(f.Metrics) != 1 {
				t.Fatalf("expected 1 metric, got %d", len(f.Metrics))
			}
			if f.Metrics[0].Value != 1 {
				t.Fatalf("expected value 1, got %f", f.Metrics[0].Value)
			}
		case "test_gauge":
			if f.Type != MetricTypeGauge {
				t.Fatalf("expected gauge type")
			}
			if f.Help != "A test gauge" {
				t.Fatalf("expected help text 'A test gauge', got %q", f.Help)
			}
			if len(f.Metrics) != 1 {
				t.Fatalf("expected 1 metric, got %d", len(f.Metrics))
			}
			if f.Metrics[0].Value != 100 {
				t.Fatalf("expected value 100, got %f", f.Metrics[0].Value)
			}
		default:
			t.Fatalf("unexpected family name: %s", f.Name)
		}
	}
}

func TestRegistryPanicOnTypeMismatch(t *testing.T) {
	r := NewRegistry()
	r.RegisterCounter("dup", "first")

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic on type mismatch")
		}
	}()
	r.RegisterGauge("dup", "second")
}

func TestRenderPrometheus(t *testing.T) {
	r := NewRegistry()
	c := r.RegisterCounter("proxy_requests_total", "Total requests")
	c.Inc()
	c.Inc()
	g := r.RegisterGauge("proxy_queue_depth", "Queue depth")
	g.Set(5)

	families := r.Collect()
	var buf strings.Builder
	err := RenderPrometheus(&buf, families)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "# HELP proxy_queue_depth Queue depth") {
		t.Fatalf("missing HELP for queue depth:\n%s", output)
	}
	if !strings.Contains(output, "# HELP proxy_requests_total Total requests") {
		t.Fatalf("missing HELP for requests total:\n%s", output)
	}
	if !strings.Contains(output, "# TYPE proxy_queue_depth gauge") {
		t.Fatalf("missing TYPE gauge:\n%s", output)
	}
	if !strings.Contains(output, "# TYPE proxy_requests_total counter") {
		t.Fatalf("missing TYPE counter:\n%s", output)
	}
	if !strings.Contains(output, "proxy_queue_depth 5") {
		t.Fatalf("missing queue depth value:\n%s", output)
	}
	if !strings.Contains(output, "proxy_requests_total 2") {
		t.Fatalf("missing requests total value:\n%s", output)
	}
}

func TestRenderPrometheusWithLabels(t *testing.T) {
	families := []MetricFamily{
		{
			Name: "proxy_backend_up",
			Help: "Backend health",
			Type: MetricTypeGauge,
			Metrics: []MetricSample{
				{Labels: map[string]string{"backend": "ollama-main"}, Value: 1},
				{Labels: map[string]string{"backend": "ollama-fallback"}, Value: 0},
			},
		},
	}

	var buf strings.Builder
	err := RenderPrometheus(&buf, families)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `proxy_backend_up{backend="ollama-main"} 1`) {
		t.Fatalf("missing ollama-main metric:\n%s", output)
	}
	if !strings.Contains(output, `proxy_backend_up{backend="ollama-fallback"} 0`) {
		t.Fatalf("missing ollama-fallback metric:\n%s", output)
	}
}

func TestHandler(t *testing.T) {
	reg := NewRegistry()
	c := reg.RegisterCounter("test_counter", "Test counter")
	c.Inc()
	c.Inc()
	c.Inc()

	collector := &Collector{Reg: reg}
	handler := Handler(collector)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	expectedCT := "text/plain; version=0.0.4; charset=utf-8"
	if ct != expectedCT {
		t.Fatalf("expected Content-Type %q, got %q", expectedCT, ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "test_counter 3") {
		t.Fatalf("expected counter value 3 in body:\n%s", body)
	}
}

func TestCollectorNilFieldsNoPanic(t *testing.T) {
	collector := NewCollector(nil, nil, nil, nil, nil, nil, nil)
	families := collector.Collect()
	// Nil slice is acceptable — range over nil slices works in Go.
	if len(families) != 0 {
		t.Fatalf("expected 0 families, got %d", len(families))
	}
}
