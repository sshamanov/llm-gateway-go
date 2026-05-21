package config

import (
	"math"
	"testing"
)

func TestDefaultConfigValues(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name string
		got  any
		want any
	}{
		// Server
		{"server.request_timeout_seconds", cfg.Server.RequestTimeoutSeconds, 900},
		{"server.max_request_body_bytes", cfg.Server.MaxRequestBodyBytes, 10_485_760},
		{"server.enable_request_logging", cfg.Server.EnableRequestLogging, true},
		{"server.enable_debug_logging", cfg.Server.EnableDebugLogging, false},

		// Hosts
		{"hosts[0].id", cfg.Hosts[0].ID, "default"},
		{"hosts[0].max_active_jobs", cfg.Hosts[0].MaxActiveJobs, 1},

		// Ollama backends
		{"ollama_backends[0].id", cfg.OllamaBackends[0].ID, "ollama-main"},
		{"ollama_backends[0].url", cfg.OllamaBackends[0].URL, "http://127.0.0.1:11434"},
		{"ollama_backends[0].host", cfg.OllamaBackends[0].Host, "default"},
		{"ollama_backends[0].max_concurrent_requests", cfg.OllamaBackends[0].MaxConcurrentRequests, 1},
		{"ollama_backends[0].enabled", cfg.OllamaBackends[0].Enabled, true},

		// Image backends
		{"image_backends[0].id", cfg.ImageBackends[0].ID, "image-main"},
		{"image_backends[0].type", cfg.ImageBackends[0].Type, "openai_compatible"},
		{"image_backends[0].url", cfg.ImageBackends[0].URL, "http://127.0.0.1:8080/v1"},
		{"image_backends[0].host", cfg.ImageBackends[0].Host, "default"},
		{"image_backends[0].max_concurrent_requests", cfg.ImageBackends[0].MaxConcurrentRequests, 1},
		{"image_backends[0].enabled", cfg.ImageBackends[0].Enabled, false},

		// Audio backends (empty)
		{"len(audio_backends)", len(cfg.AudioBackends), 0},

		// Ollama defaults
		{"ollama_defaults.keep_alive", cfg.OllamaDefaults.KeepAlive, "10m"},
		{"ollama_defaults.think", cfg.OllamaDefaults.Think, false},
		{"ollama_defaults.options.num_thread", cfg.OllamaDefaults.Options.NumThread, 8},
		{"ollama_defaults.options.num_ctx", cfg.OllamaDefaults.Options.NumCtx, 8192},
		{"ollama_defaults.options.temperature", cfg.OllamaDefaults.Options.Temperature, 0.2},
		{"ollama_defaults.options.top_p", cfg.OllamaDefaults.Options.TopP, 0.9},

		// Models
		{"models.expose_native_ollama_models", cfg.Models.ExposeNativeOllamaModels, true},

		// Aliases
		{"models.aliases[0].name", cfg.Models.Aliases[0].Name, "qwen-instruct"},
		{"models.aliases[0].primary_model", cfg.Models.Aliases[0].PrimaryModel, "qwen:30b"},
		{"models.aliases[0].backup_models[0]", cfg.Models.Aliases[0].BackupModels[0], "qwen:14b"},
		{"models.aliases[0].backup_models[1]", cfg.Models.Aliases[0].BackupModels[1], "qwen:72b"},
		{"models.aliases[0].overrides.think", *cfg.Models.Aliases[0].Overrides.Think, false},
		{"models.aliases[0].overrides.options.temperature", cfg.Models.Aliases[0].Overrides.Options.Temperature, 0.2},
		{"models.aliases[1].name", cfg.Models.Aliases[1].Name, "qwen-thinking"},
		{"models.aliases[1].primary_model", cfg.Models.Aliases[1].PrimaryModel, "qwen:30b"},
		{"models.aliases[1].overrides.think", *cfg.Models.Aliases[1].Overrides.Think, true},
		{"models.aliases[1].overrides.options.temperature", cfg.Models.Aliases[1].Overrides.Options.Temperature, 0.2},

		// Policy
		{"policy.ignore_unsupported_fields", cfg.Policy.IgnoreUnsupportedFields, true},
		{"policy.log_unsupported_fields", cfg.Policy.LogUnsupportedFields, true},
		{"policy.allow_client_override_options", cfg.Policy.AllowClientOverrideOptions, false},
		{"policy.allow_anthropic_thinking_override", cfg.Policy.AllowAnthropicThinkingOverride, false},

		// Documents
		{"documents.enabled", cfg.Documents.Enabled, true},
		{"documents.default_mode", cfg.Documents.DefaultMode, "auto"},
		{"documents.ocr_enabled", cfg.Documents.OCREnabled, true},
		{"documents.ocr_mode", cfg.Documents.OCRMode, "auto"},
		{"documents.allow_url_input", cfg.Documents.AllowURLInput, false},
		{"documents.allow_base64_input", cfg.Documents.AllowBase64Input, true},
		{"documents.preparation_workers", cfg.Documents.PreparationWorkers, 1},

		// Scheduler
		{"scheduler.strategy", cfg.Scheduler.Strategy, "priority_host_model_affinity"},
		{"scheduler.top_n_lookahead", cfg.Scheduler.TopNLookahead, 64},
		{"scheduler.queue_max_pending", cfg.Scheduler.QueueMaxPending, 100},
		{"scheduler.aging_per_second", cfg.Scheduler.AgingPerSecond, 0.05},
		{"scheduler.unknown_tokens_per_second", cfg.Scheduler.UnknownTokensPerSecond, 5.0},
		{"scheduler.unknown_cold_load_penalty_seconds", cfg.Scheduler.UnknownColdLoadPenaltySeconds, 30.0},
		{"scheduler.alias_substitution_penalty_seconds", cfg.Scheduler.AliasSubstitutionPenaltySeconds, 8.0},
		{"scheduler.disruption_factor", cfg.Scheduler.DisruptionFactor, 0.25},

		// Retry
		{"scheduler.retry.max_attempts", cfg.Scheduler.Retry.MaxAttempts, 2},
		{"scheduler.retry.streaming_retry_before_first_token", cfg.Scheduler.Retry.StreamingRetryBeforeFirstToken, true},
		{"scheduler.retry.streaming_retry_after_first_token", cfg.Scheduler.Retry.StreamingRetryAfterFirstToken, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch want := tt.want.(type) {
			case float64:
				got, ok := tt.got.(float64)
				if !ok {
					t.Errorf("expected float64, got %T", tt.got)
					return
				}
				if math.Abs(got-want) > 1e-9 {
					t.Errorf("got %v, want %v", got, want)
				}
			default:
				if tt.got != tt.want {
					t.Errorf("got %v, want %v", tt.got, tt.want)
				}
			}
		})
	}
}

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := DefaultConfig()

	// Timeout must be positive.
	if cfg.Server.RequestTimeoutSeconds <= 0 {
		t.Errorf("request_timeout_seconds must be positive, got %d", cfg.Server.RequestTimeoutSeconds)
	}

	// Max body bytes must be positive.
	if cfg.Server.MaxRequestBodyBytes <= 0 {
		t.Errorf("max_request_body_bytes must be positive, got %d", cfg.Server.MaxRequestBodyBytes)
	}

	// Strategy must be a known value.
	validStrategies := map[string]bool{
		"priority_host_model_affinity": true,
	}
	if !validStrategies[cfg.Scheduler.Strategy] {
		t.Errorf("unknown scheduler strategy %q", cfg.Scheduler.Strategy)
	}

	// Lookahead must be at least 1.
	if cfg.Scheduler.TopNLookahead < 1 {
		t.Errorf("top_n_lookahead must be >= 1, got %d", cfg.Scheduler.TopNLookahead)
	}

	// Max pending must be at least 1.
	if cfg.Scheduler.QueueMaxPending < 1 {
		t.Errorf("queue_max_pending must be >= 1, got %d", cfg.Scheduler.QueueMaxPending)
	}

	// Aging per second must be non-negative.
	if cfg.Scheduler.AgingPerSecond < 0 {
		t.Errorf("aging_per_second must be >= 0, got %f", cfg.Scheduler.AgingPerSecond)
	}

	// Max attempts must be at least 1.
	if cfg.Scheduler.Retry.MaxAttempts < 1 {
		t.Errorf("retry.max_attempts must be >= 1, got %d", cfg.Scheduler.Retry.MaxAttempts)
	}

	// Must have at least one host.
	if len(cfg.Hosts) == 0 {
		t.Error("default config must have at least one host")
	}

	// Must have at least one Ollama backend.
	if len(cfg.OllamaBackends) == 0 {
		t.Error("default config must have at least one Ollama backend")
	}
}
