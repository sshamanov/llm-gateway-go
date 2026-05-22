package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// RenderPrometheus writes MetricFamily slices in Prometheus exposition format.
func RenderPrometheus(w io.Writer, families []MetricFamily) error {
	// Sort families by name for deterministic output.
	sort.Slice(families, func(i, j int) bool {
		return families[i].Name < families[j].Name
	})

	for _, f := range families {
		// Sort metrics within a family by canonical label string.
		sort.Slice(f.Metrics, func(i, j int) bool {
			return labelString(f.Metrics[i].Labels) < labelString(f.Metrics[j].Labels)
		})

		typeStr := "unknown"
		switch f.Type {
		case MetricTypeCounter:
			typeStr = "counter"
		case MetricTypeGauge:
			typeStr = "gauge"
		}

		if _, err := fmt.Fprintf(w, "# HELP %s %s\n", f.Name, f.Help); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "# TYPE %s %s\n", f.Name, typeStr); err != nil {
			return err
		}

		for _, m := range f.Metrics {
			labels := formatLabels(m.Labels)
			if _, err := fmt.Fprintf(w, "%s%s %g\n", f.Name, labels, m.Value); err != nil {
				return err
			}
		}
	}

	return nil
}

// labelString creates a canonical string representation of a label set for
// deterministic sorting. Returns "" for nil/empty label sets.
func labelString(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}

// formatLabels converts a label map to Prometheus label format.
// Returns "" if labels is nil or empty.
// Output format: {key1="value1",key2="value2"}
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(labels[k])
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}
