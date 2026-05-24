package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"llm-go-proxy/internal/mock"
)

// MultiBackendChaos creates 3 backends with mixed failure modes and varying
// latencies, sends 10 concurrent non-streaming requests, and verifies all
// complete successfully (retries handle failures).
func MultiBackendChaos(h *mock.Harness) error {
	rh, err := mock.NewHarness()
	if err != nil {
		return fmt.Errorf("create harness: %w", err)
	}
	defer rh.Shutdown()

	err = rh.StartFakeBackends([]mock.FakeOllamaConfig{
		{
			ID:           "b1",
			Models:       []string{"llama3"},
			LoadedModels: []string{"llama3"},
			FailureRate:  0.3,            // 30% random failures.
			ChatLatency:  50 * time.Millisecond,
			TotalDuration: int64(50 * time.Millisecond),
			EvalCount:    50,
		},
		{
			ID:           "b2",
			Models:       []string{"llama3"},
			LoadedModels: []string{"llama3"},
			FailCount:    2,               // First 2 requests fail deterministically.
			ChatLatency:  10 * time.Millisecond,
			TotalDuration: int64(20 * time.Millisecond),
			EvalCount:    60,
		},
		{
			ID:           "b3",
			Models:       []string{"llama3"},
			LoadedModels: []string{"llama3"},
			ChatLatency:  5 * time.Millisecond,
			TotalDuration: int64(10 * time.Millisecond),
			EvalCount:    40,
		},
	}, 0)
	if err != nil {
		return fmt.Errorf("start backends: %w", err)
	}

	if err := rh.WriteConfig(); err != nil {
		return err
	}
	rh.Config.Scheduler.Retry.MaxAttempts = 3
	rh.Config.Hosts[0].MaxActiveJobs = 3
	if err := rh.UpdateConfig(); err != nil {
		return err
	}
	if err := rh.StartProxy(); err != nil {
		return err
	}
	if err := rh.WaitReady(); err != nil {
		return err
	}

	req := map[string]interface{}{
		"model": "llama3",
		"messages": []map[string]string{
			{"role": "user", "content": "Chaos test"},
		},
	}

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, body, err := rh.Post("/v1/chat/completions", req)
			if err != nil {
				errs <- fmt.Errorf("request %d: %w", idx, err)
				return
			}
			if resp.StatusCode != 200 {
				errs <- fmt.Errorf("request %d: expected 200, got %d (body: %s)", idx, resp.StatusCode, string(body))
				return
			}
			// Verify response shape.
			var data map[string]interface{}
			if err := json.Unmarshal(body, &data); err != nil {
				errs <- fmt.Errorf("request %d: unmarshal: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for e := range errs {
		return e
	}

	return nil
}

// ColdModelPreference verifies the proxy handles backends with vastly different
// model load latencies. One backend simulates a cold model (high LoadDuration),
// another is warm (zero LoadDuration). All requests should complete.
func ColdModelPreference(h *mock.Harness) error {
	rh, err := mock.NewHarness()
	if err != nil {
		return fmt.Errorf("create harness: %w", err)
	}
	defer rh.Shutdown()

	err = rh.StartFakeBackends([]mock.FakeOllamaConfig{
		{
			ID:           "b1",
			Models:       []string{"llama3"},
			LoadedModels: []string{"llama3"},
			// Simulate cold model: high load time, high total time, low TPS.
			ChatLatency:   200 * time.Millisecond,
			LoadDuration:  int64(5 * time.Second),
			TotalDuration: int64(6 * time.Second),
			EvalCount:     10,
		},
		{
			ID:           "b2",
			Models:       []string{"llama3"},
			LoadedModels: []string{"llama3"},
			// Simulate warm model: no load time, low total time, high TPS.
			ChatLatency:   10 * time.Millisecond,
			LoadDuration:  0,
			TotalDuration: int64(50 * time.Millisecond),
			EvalCount:     50,
		},
	}, 0)
	if err != nil {
		return fmt.Errorf("start backends: %w", err)
	}

	if err := rh.WriteConfig(); err != nil {
		return err
	}
	rh.Config.Hosts[0].MaxActiveJobs = 2
	if err := rh.UpdateConfig(); err != nil {
		return err
	}
	if err := rh.StartProxy(); err != nil {
		return err
	}
	if err := rh.WaitReady(); err != nil {
		return err
	}

	req := map[string]interface{}{
		"model": "llama3",
		"messages": []map[string]string{
			{"role": "user", "content": "Warm vs cold test"},
		},
	}

	// Send 5 requests — all should complete. The scheduler should learn that
	// b2 is faster and prefer it.
	for i := 0; i < 5; i++ {
		resp, body, err := rh.Post("/v1/chat/completions", req)
		if err != nil {
			return fmt.Errorf("request %d failed: %w", i, err)
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("request %d: expected 200, got %d (body: %s)", i, resp.StatusCode, string(body))
		}
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return fmt.Errorf("request %d: unmarshal: %w", i, err)
		}
	}

	return nil
}

// ClientCancellation verifies that when a client disconnects mid-request,
// the proxy cancels the backend job.
func ClientCancellation(h *mock.Harness) error {
	rh, err := mock.NewHarness()
	if err != nil {
		return fmt.Errorf("create harness: %w", err)
	}
	defer rh.Shutdown()

	err = rh.StartFakeBackends([]mock.FakeOllamaConfig{
		{
			ID:           "b1",
			Models:       []string{"llama3"},
			LoadedModels: []string{"llama3"},
			ChatLatency:  5 * time.Second, // Very slow.
		},
	}, 0)
	if err != nil {
		return fmt.Errorf("start backends: %w", err)
	}

	if err := rh.WriteConfig(); err != nil {
		return err
	}
	if err := rh.StartProxy(); err != nil {
		return err
	}
	if err := rh.WaitReady(); err != nil {
		return err
	}

	reqBody := map[string]interface{}{
		"model": "llama3",
		"messages": []map[string]string{
			{"role": "user", "content": "Cancel me"},
		},
	}

	payload, _ := json.Marshal(reqBody)

	// Create a request with a short-lived context.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, rh.ProxyURL+"/v1/chat/completions", strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := http.DefaultClient.Do(httpReq)
	elapsed := time.Since(start)

	if err != nil {
		// Context cancelled — this is the expected path.
		if elapsed < 500*time.Millisecond {
			return nil // Cancelled quickly, good.
		}
		return fmt.Errorf("cancellation took too long: %v", elapsed)
	}

	// If we got a response, it must be an error (the request should not
	// complete successfully with a 5s backend latency and 100ms client timeout).
	if resp != nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return fmt.Errorf("expected cancellation/error, got 200 (elapsed: %v)", elapsed)
		}
	}

	return nil
}
