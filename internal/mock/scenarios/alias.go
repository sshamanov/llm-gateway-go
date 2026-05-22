package scenarios

import (
	"encoding/json"
	"fmt"

	"llm-go-proxy/internal/config"
	"llm-go-proxy/internal/mock"
)

// AliasFallback verifies that when the primary model is not loaded but a backup
// model is, the request goes through the backup.
func AliasFallback(h *mock.Harness) error {
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
		},
		{
			ID:           "b2",
			Models:       []string{"llama3:backup"},
			LoadedModels: []string{"llama3:backup"},
		},
	}, 0)
	if err != nil {
		return fmt.Errorf("start backends: %w", err)
	}

	if err := rh.WriteConfig(); err != nil {
		return err
	}

	// Override config with alias before starting proxy.
	rh.Config.Models.ExposeNativeOllamaModels = true
	rh.Config.Models.Aliases = []config.AliasConfig{
		{
			Name:         "my-alias",
			PrimaryModel: "llama3",
			BackupModels: []string{"llama3:backup"},
		},
	}

	// Re-write the config file with our alias.
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
		"model": "my-alias",
		"messages": []map[string]string{
			{"role": "user", "content": "Hi from alias"},
		},
	}

	resp, body, err := rh.Post("/v1/chat/completions", req)
	if err != nil {
		return fmt.Errorf("alias request failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("alias request: expected 200, got %d (body: %s)", resp.StatusCode, string(body))
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("alias: unmarshal: %w", err)
	}
	if data["model"] == nil {
		return fmt.Errorf("alias: missing model field in response")
	}

	return nil
}
