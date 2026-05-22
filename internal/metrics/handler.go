package metrics

import "net/http"

// Handler returns an HTTP handler that renders Prometheus metrics.
func Handler(collector *Collector) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		families := collector.Collect()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = RenderPrometheus(w, families)
	})
}
