package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultConfig returns a fully populated Config with all default values
// as specified in the architecture document section 5.1 example config.
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			RequestTimeoutSeconds: 900,
			MaxRequestBodyBytes:   10_485_760,
			EnableRequestLogging:  true,
			EnableDebugLogging:    false,
		},
		Hosts: []HostConfig{
			{
				ID:            "default",
				MaxActiveJobs: 1,
			},
		},
		OllamaBackends: []OllamaBackendConfig{
			{
				ID:                   "ollama-main",
				URL:                  "http://127.0.0.1:11434",
				Host:                 "default",
				MaxConcurrentRequests: 1,
				Enabled:              true,
			},
		},
		ImageBackends: []ImageBackendConfig{
			{
				ID:                   "image-main",
				Type:                 "openai_compatible",
				URL:                  "http://127.0.0.1:8080/v1",
				Host:                 "default",
				MaxConcurrentRequests: 1,
				Enabled:              false,
			},
		},
		AudioBackends: []AudioBackendConfig{},
		OllamaDefaults: OllamaDefaultsConfig{
			KeepAlive: "10m",
			Think:     false,
			Options: OllamaOptions{
				NumThread:   8,
				NumCtx:      8192,
				Temperature: 0.2,
				TopP:        0.9,
			},
		},
		Models: ModelsConfig{
			ExposeNativeOllamaModels: true,
			Aliases: []AliasConfig{
				{
					Name:         "qwen-instruct",
					PrimaryModel: "qwen:30b",
					BackupModels: []string{"qwen:14b", "qwen:72b"},
					Overrides: AliasOverrides{
						Think:   ptr(false),
						Options: &OllamaOptions{Temperature: 0.2},
					},
				},
				{
					Name:         "qwen-thinking",
					PrimaryModel: "qwen:30b",
					BackupModels: []string{"qwen:14b", "qwen:72b"},
					Overrides: AliasOverrides{
						Think:   ptr(true),
						Options: &OllamaOptions{Temperature: 0.2},
					},
				},
			},
		},
		Policy: PolicyConfig{
			IgnoreUnsupportedFields:        true,
			LogUnsupportedFields:           true,
			AllowClientOverrideOptions:     false,
			AllowAnthropicThinkingOverride: false,
		},
		Documents: DocumentsConfig{
			Enabled:            true,
			DefaultMode:        "auto",
			OCREnabled:         true,
			OCRMode:            "auto",
			AllowURLInput:      false,
			AllowBase64Input:   true,
			PreparationWorkers: 1,
		},
		Scheduler: SchedulerConfig{
			Strategy:                        "priority_host_model_affinity",
			TopNLookahead:                   64,
			QueueMaxPending:                 100,
			AgingPerSecond:                  0.05,
			UnknownTokensPerSecond:          5.0,
			UnknownColdLoadPenaltySeconds:   30.0,
			AliasSubstitutionPenaltySeconds: 8.0,
			DisruptionFactor:                0.25,
			Retry: RetryConfig{
				MaxAttempts:                    2,
				StreamingRetryBeforeFirstToken:  true,
				StreamingRetryAfterFirstToken:   false,
			},
		},
	}
}

// ptr returns a pointer to a copy of v.
func ptr[T any](v T) *T {
	return &v
}

// SaveConfig marshals cfg as indented JSON and writes it to path.
// The parent directory is created if it does not exist.
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}
