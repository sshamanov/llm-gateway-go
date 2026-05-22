package mock

import (
	"fmt"
	"sort"
	"time"
)

// Scenario is a function that executes an E2E test against the harness.
type Scenario func(h *Harness) error

// ScenarioInfo holds metadata for a registered scenario.
type ScenarioInfo struct {
	Name     string
	Fn       Scenario
	Category string
}

// ScenarioRegistry holds all registered scenarios.
type ScenarioRegistry struct {
	scenarios []ScenarioInfo
}

// NewScenarioRegistry creates an empty scenario registry.
func NewScenarioRegistry() *ScenarioRegistry {
	return &ScenarioRegistry{}
}

// Register adds a scenario to the registry.
func (r *ScenarioRegistry) Register(name, category string, fn Scenario) {
	r.scenarios = append(r.scenarios, ScenarioInfo{
		Name:     name,
		Fn:       fn,
		Category: category,
	})
}

// RunAll executes all registered scenarios sequentially, respecting category order.
// Returns the number of passed and failed scenarios.
func (r *ScenarioRegistry) RunAll(h *Harness, verbose bool) (passed, failed int) {
	sorted := make([]ScenarioInfo, len(r.scenarios))
	copy(sorted, r.scenarios)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	for _, info := range sorted {
		if verbose {
			fmt.Printf("  %-50s ", info.Name)
		}

		start := time.Now()
		err := info.Fn(h)
		elapsed := time.Since(start)

		if err != nil {
			failed++
			if verbose {
				fmt.Printf("FAIL (%.2fs)\n", elapsed.Seconds())
				fmt.Printf("        reason: %v\n", err)
			} else {
				fmt.Printf("FAIL %s: %v\n", info.Name, err)
			}
		} else {
			passed++
			if verbose {
				fmt.Printf("PASS (%.2fs)\n", elapsed.Seconds())
			} else {
				fmt.Printf("PASS %s\n", info.Name)
			}
		}
	}

	return
}

// Filter returns scenarios whose names contain filter (case-insensitive contains).
// If filter is empty, all scenarios are returned.
func (r *ScenarioRegistry) Filter(filter string) []ScenarioInfo {
	if filter == "" {
		return r.scenarios
	}
	var result []ScenarioInfo
	for _, s := range r.scenarios {
		if containsFold(s.Name, filter) {
			result = append(result, s)
		}
	}
	return result
}

func containsFold(s, substr string) bool {
	return len(s) >= len(substr) && len(substr) > 0 &&
		containsSubstring(s, substr)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			sc := s[i+j]
			tc := substr[j]
			if sc >= 'A' && sc <= 'Z' {
				sc += 'a' - 'A'
			}
			if tc >= 'A' && tc <= 'Z' {
				tc += 'a' - 'A'
			}
			if sc != tc {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
