package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"llm-go-proxy/internal/mock"
	"llm-go-proxy/internal/mock/scenarios"
)

func main() {
	verbose := flag.Bool("v", false, "Verbose output")
	filter := flag.String("scenarios", "", "Run only scenarios matching filter (comma-separated)")
	flag.Parse()

	filters := strings.Split(*filter, ",")
	if *filter == "" {
		filters = nil
	}

	// Create the shared harness with base backends.
	h, err := mock.NewHarness()
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness: %v\n", err)
		os.Exit(1)
	}
	defer h.Shutdown()

	err = h.StartFakeBackends([]mock.FakeOllamaConfig{
		{
			ID:           "b1",
			Models:       []string{"llama3"},
			LoadedModels: []string{"llama3"},
		},
		{
			ID:           "b2",
			Models:       []string{"llama3"},
			LoadedModels: []string{"llama3"},
		},
	}, 1) // 1 image backend
	if err != nil {
		fmt.Fprintf(os.Stderr, "start backends: %v\n", err)
		os.Exit(1)
	}

	if err := h.WriteConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "write config: %v\n", err)
		os.Exit(1)
	}

	if err := h.StartProxy(); err != nil {
		fmt.Fprintf(os.Stderr, "start proxy: %v\n", err)
		os.Exit(1)
	}

	if err := h.WaitReady(); err != nil {
		fmt.Fprintf(os.Stderr, "wait ready: %v\n", err)
		os.Exit(1)
	}

	if *verbose {
		fmt.Printf("Proxy listening on %s\n\n", h.ProxyURL)
	}

	// Register all scenarios.
	reg := mock.NewScenarioRegistry()
	reg.Register("HealthyCluster", "chat", scenarios.HealthyCluster)
	reg.Register("StreamingChat", "chat", scenarios.StreamingChat)
	reg.Register("StreamingRetryBeforeToken", "retry", scenarios.StreamingRetryBeforeToken)
	reg.Register("NoRetryAfterToken", "retry", scenarios.NoRetryAfterToken)
	reg.Register("NonStreamingRetrySuccess", "retry", scenarios.NonStreamingRetrySuccess)
	reg.Register("RetryExhausted", "retry", scenarios.RetryExhausted)
	reg.Register("HostCapacitySerialization", "capacity", scenarios.HostCapacitySerialization)
	reg.Register("QueueAging", "capacity", scenarios.QueueAging)
	reg.Register("BackendDisable", "capacity", scenarios.BackendDisable)
	reg.Register("QueueFull", "capacity", scenarios.QueueFull)
	reg.Register("AliasFallback", "alias", scenarios.AliasFallback)
	reg.Register("FullAPISurface", "api", scenarios.FullAPISurface)
	reg.Register("ImageGeneration", "api", scenarios.ImageGeneration)
	reg.Register("MetricsEndpoint", "observe", scenarios.MetricsEndpoint)
	reg.Register("DebugEndpoints", "observe", scenarios.DebugEndpoints)
	reg.Register("DocumentProcessing", "document", scenarios.DocumentProcessing)
	reg.Register("MultiBackendChaos", "chaos", scenarios.MultiBackendChaos)
	reg.Register("ColdModelPreference", "chaos", scenarios.ColdModelPreference)
	reg.Register("ClientCancellation", "chaos", scenarios.ClientCancellation)

	fmt.Println("=== Mock E2E Tests ===")
	fmt.Printf("Proxy: %s\n\n", h.ProxyURL)

	passed, failed := 0, 0

	if filters == nil {
		passed, failed = reg.RunAll(h, *verbose)
	} else {
		fmt.Printf("Filter: %v\n\n", filters)
		for _, filter := range filters {
			filter = strings.TrimSpace(filter)
			if filter == "" {
				continue
			}
			matches := reg.Filter(filter)
			for _, info := range matches {
				if *verbose {
					fmt.Printf("  %-50s ", info.Name)
				}
				err := info.Fn(h)
				if err != nil {
					failed++
					fmt.Printf("FAIL %s: %v\n", info.Name, err)
				} else {
					passed++
					fmt.Printf("PASS %s\n", info.Name)
				}
			}
		}
	}

	fmt.Printf("\n=== Results: %d passed, %d failed ===\n", passed, failed)

	if failed > 0 {
		os.Exit(1)
	}
}
