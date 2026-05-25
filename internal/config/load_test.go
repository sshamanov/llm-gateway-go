package config

import (
	"testing"
)

func TestLoadConfig_Full(t *testing.T) {
	cfg, err := LoadConfig("testdata/full_config.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Server
	if cfg.Server.RequestTimeoutSeconds != 900 {
		t.Errorf("request_timeout_seconds = %d, want 900", cfg.Server.RequestTimeoutSeconds)
	}
	if cfg.Server.MaxRequestBodyBytes != 10_485_760 {
		t.Errorf("max_request_body_bytes = %d, want 10485760", cfg.Server.MaxRequestBodyBytes)
	}
	if !cfg.Server.EnableRequestLogging {
		t.Error("enable_request_logging should be true")
	}
	if cfg.Server.EnableDebugLogging {
		t.Error("enable_debug_logging should be false")
	}

	// Hosts
	if len(cfg.Hosts) != 1 {
		t.Fatalf("len(hosts) = %d, want 1", len(cfg.Hosts))
	}
	if cfg.Hosts[0].ID != "default" {
		t.Errorf("hosts[0].id = %q, want %q", cfg.Hosts[0].ID, "default")
	}
	if cfg.Hosts[0].MaxActiveJobs != 1 {
		t.Errorf("hosts[0].max_active_jobs = %d, want 1", cfg.Hosts[0].MaxActiveJobs)
	}

	// Ollama backends
	if len(cfg.OllamaBackends) != 1 {
		t.Fatalf("len(ollama_backends) = %d, want 1", len(cfg.OllamaBackends))
	}
	ob := cfg.OllamaBackends[0]
	if ob.ID != "ollama-main" {
		t.Errorf("ollama_backends[0].id = %q, want %q", ob.ID, "ollama-main")
	}
	if ob.URL != "http://127.0.0.1:11434" {
		t.Errorf("ollama_backends[0].url = %q, want %q", ob.URL, "http://127.0.0.1:11434")
	}
	if ob.Host != "default" {
		t.Errorf("ollama_backends[0].host = %q, want %q", ob.Host, "default")
	}
	if ob.MaxConcurrentRequests != 1 {
		t.Errorf("ollama_backends[0].max_concurrent_requests = %d, want 1", ob.MaxConcurrentRequests)
	}
	if !ob.Enabled {
		t.Error("ollama_backends[0].enabled should be true")
	}

	// Image backends
	if len(cfg.ImageBackends) != 1 {
		t.Fatalf("len(image_backends) = %d, want 1", len(cfg.ImageBackends))
	}
	ib := cfg.ImageBackends[0]
	if ib.ID != "image-main" {
		t.Errorf("image_backends[0].id = %q, want %q", ib.ID, "image-main")
	}
	if ib.Type != "openai_compatible" {
		t.Errorf("image_backends[0].type = %q, want %q", ib.Type, "openai_compatible")
	}
	if ib.URL != "http://127.0.0.1:8080/v1" {
		t.Errorf("image_backends[0].url = %q, want %q", ib.URL, "http://127.0.0.1:8080/v1")
	}
	if ib.Host != "default" {
		t.Errorf("image_backends[0].host = %q, want %q", ib.Host, "default")
	}
	if ib.MaxConcurrentRequests != 1 {
		t.Errorf("image_backends[0].max_concurrent_requests = %d, want 1", ib.MaxConcurrentRequests)
	}
	if ib.Enabled {
		t.Error("image_backends[0].enabled should be false")
	}

	// Audio backends (empty)
	if len(cfg.AudioBackends) != 0 {
		t.Errorf("len(audio_backends) = %d, want 0", len(cfg.AudioBackends))
	}

	// Ollama defaults
	od := cfg.OllamaDefaults
	if od.KeepAlive != "10m" {
		t.Errorf("ollama_defaults.keep_alive = %q, want %q", od.KeepAlive, "10m")
	}
	if od.Think == nil || *od.Think {
		t.Error("ollama_defaults.think should be false")
	}

	// Models
	if !cfg.Models.ExposeNativeOllamaModels {
		t.Error("expose_native_ollama_models should be true")
	}
	if len(cfg.Models.Aliases) != 2 {
		t.Fatalf("len(aliases) = %d, want 2", len(cfg.Models.Aliases))
	}

	// Alias 0
	a0 := cfg.Models.Aliases[0]
	if a0.Name != "qwen-instruct" {
		t.Errorf("aliases[0].name = %q, want %q", a0.Name, "qwen-instruct")
	}
	if a0.PrimaryModel != "qwen:30b" {
		t.Errorf("aliases[0].primary_model = %q, want %q", a0.PrimaryModel, "qwen:30b")
	}
	if len(a0.BackupModels) != 2 || a0.BackupModels[0] != "qwen:14b" || a0.BackupModels[1] != "qwen:72b" {
		t.Errorf("aliases[0].backup_models = %v, want [qwen:14b qwen:72b]", a0.BackupModels)
	}
	if a0.Overrides.Think == nil || *a0.Overrides.Think {
		t.Error("aliases[0].overrides.think should be false")
	}
	if a0.Overrides.Options == nil || a0.Overrides.Options.Temperature != 0.2 {
		t.Errorf("aliases[0].overrides.options.temperature = %v, want 0.2", a0.Overrides.Options)
	}

	// Alias 1
	a1 := cfg.Models.Aliases[1]
	if a1.Name != "qwen-thinking" {
		t.Errorf("aliases[1].name = %q, want %q", a1.Name, "qwen-thinking")
	}
	if a1.PrimaryModel != "qwen:30b" {
		t.Errorf("aliases[1].primary_model = %q, want %q", a1.PrimaryModel, "qwen:30b")
	}
	if a1.Overrides.Think == nil || !*a1.Overrides.Think {
		t.Error("aliases[1].overrides.think should be true")
	}

	// Policy
	if !cfg.Policy.IgnoreUnsupportedFields {
		t.Error("policy.ignore_unsupported_fields should be true")
	}
	if !cfg.Policy.LogUnsupportedFields {
		t.Error("policy.log_unsupported_fields should be true")
	}
	if cfg.Policy.AllowClientOverrideOptions {
		t.Error("policy.allow_client_override_options should be false")
	}
	if cfg.Policy.AllowAnthropicThinkingOverride {
		t.Error("policy.allow_anthropic_thinking_override should be false")
	}

	// Documents
	doc := cfg.Documents
	if !doc.Enabled {
		t.Error("documents.enabled should be true")
	}
	if doc.DefaultMode != "auto" {
		t.Errorf("documents.default_mode = %q, want %q", doc.DefaultMode, "auto")
	}
	if !doc.OCREnabled {
		t.Error("documents.ocr_enabled should be true")
	}
	if doc.OCRMode != "auto" {
		t.Errorf("documents.ocr_mode = %q, want %q", doc.OCRMode, "auto")
	}
	if doc.AllowURLInput {
		t.Error("documents.allow_url_input should be false")
	}
	if !doc.AllowBase64Input {
		t.Error("documents.allow_base64_input should be true")
	}
	if doc.PreparationWorkers != 1 {
		t.Errorf("documents.preparation_workers = %d, want 1", doc.PreparationWorkers)
	}

	// Scheduler
	sched := cfg.Scheduler
	if sched.Strategy != "priority_host_model_affinity" {
		t.Errorf("scheduler.strategy = %q, want %q", sched.Strategy, "priority_host_model_affinity")
	}
	if sched.TopNLookahead != 64 {
		t.Errorf("scheduler.top_n_lookahead = %d, want 64", sched.TopNLookahead)
	}
	if sched.QueueMaxPending != 100 {
		t.Errorf("scheduler.queue_max_pending = %d, want 100", sched.QueueMaxPending)
	}
	if sched.AgingPerSecond != 0.05 {
		t.Errorf("scheduler.aging_per_second = %f, want 0.05", sched.AgingPerSecond)
	}
	if sched.UnknownTokensPerSecond != 5.0 {
		t.Errorf("scheduler.unknown_tokens_per_second = %f, want 5.0", sched.UnknownTokensPerSecond)
	}
	if sched.UnknownColdLoadPenaltySeconds != 30.0 {
		t.Errorf("scheduler.unknown_cold_load_penalty_seconds = %f, want 30.0", sched.UnknownColdLoadPenaltySeconds)
	}
	if sched.AliasSubstitutionPenaltySeconds != 8.0 {
		t.Errorf("scheduler.alias_substitution_penalty_seconds = %f, want 8.0", sched.AliasSubstitutionPenaltySeconds)
	}
	if sched.DisruptionFactor != 0.25 {
		t.Errorf("scheduler.disruption_factor = %f, want 0.25", sched.DisruptionFactor)
	}

	// Retry
	ret := cfg.Scheduler.Retry
	if ret.MaxAttempts != 2 {
		t.Errorf("scheduler.retry.max_attempts = %d, want 2", ret.MaxAttempts)
	}
	if !ret.StreamingRetryBeforeFirstToken {
		t.Error("scheduler.retry.streaming_retry_before_first_token should be true")
	}
	if ret.StreamingRetryAfterFirstToken {
		t.Error("scheduler.retry.streaming_retry_after_first_token should be false")
	}
}

func TestLoadConfig_Partial(t *testing.T) {
	cfg, err := LoadConfig("testdata/partial_config.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The partial file overrides request_timeout_seconds.
	if cfg.Server.RequestTimeoutSeconds != 600 {
		t.Errorf("request_timeout_seconds = %d, want 600", cfg.Server.RequestTimeoutSeconds)
	}

	// Everything else should remain at default.
	def := DefaultConfig()

	if cfg.Server.MaxRequestBodyBytes != def.Server.MaxRequestBodyBytes {
		t.Errorf("max_request_body_bytes = %d, want default %d",
			cfg.Server.MaxRequestBodyBytes, def.Server.MaxRequestBodyBytes)
	}
	if cfg.Server.EnableRequestLogging != def.Server.EnableRequestLogging {
		t.Errorf("enable_request_logging = %v, want default %v",
			cfg.Server.EnableRequestLogging, def.Server.EnableRequestLogging)
	}

	if cfg.OllamaDefaults.KeepAlive != def.OllamaDefaults.KeepAlive {
		t.Errorf("keep_alive = %q, want default %q",
			cfg.OllamaDefaults.KeepAlive, def.OllamaDefaults.KeepAlive)
	}

	if cfg.Models.ExposeNativeOllamaModels != def.Models.ExposeNativeOllamaModels {
		t.Errorf("expose_native_ollama_models = %v, want default %v",
			cfg.Models.ExposeNativeOllamaModels, def.Models.ExposeNativeOllamaModels)
	}

	if cfg.Policy.IgnoreUnsupportedFields != def.Policy.IgnoreUnsupportedFields {
		t.Errorf("ignore_unsupported_fields = %v, want default %v",
			cfg.Policy.IgnoreUnsupportedFields, def.Policy.IgnoreUnsupportedFields)
	}

	if cfg.Documents.Enabled != def.Documents.Enabled {
		t.Errorf("documents.enabled = %v, want default %v",
			cfg.Documents.Enabled, def.Documents.Enabled)
	}

	if cfg.Scheduler.Strategy != def.Scheduler.Strategy {
		t.Errorf("scheduler.strategy = %q, want default %q",
			cfg.Scheduler.Strategy, def.Scheduler.Strategy)
	}

	if cfg.Scheduler.Retry.MaxAttempts != def.Scheduler.Retry.MaxAttempts {
		t.Errorf("retry.max_attempts = %d, want default %d",
			cfg.Scheduler.Retry.MaxAttempts, def.Scheduler.Retry.MaxAttempts)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	// Use a temp directory so the auto-generated default config is cleaned up.
	path := t.TempDir() + "/nonexistent.json"
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}

	def := DefaultConfig()
	if cfg.Server.RequestTimeoutSeconds != def.Server.RequestTimeoutSeconds {
		t.Errorf("expected defaults, got different request_timeout_seconds")
	}
	if cfg.OllamaDefaults.KeepAlive != def.OllamaDefaults.KeepAlive {
		t.Errorf("expected defaults, got different keep_alive")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	_, err := LoadConfig("testdata/bad.json")
	if err == nil {
		t.Fatal("expected error for bad JSON, got nil")
	}
}

func TestLoadConfig_EmptyFile(t *testing.T) {
	_, err := LoadConfig("testdata/empty.json")
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}
