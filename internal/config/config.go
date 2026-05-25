package config

// Config is the top-level proxy configuration loaded from config.json.
type Config struct {
	Server         ServerConfig           `json:"server"`
	Hosts          []HostConfig           `json:"hosts"`
	OllamaBackends []OllamaBackendConfig  `json:"ollama_backends"`
	ImageBackends  []ImageBackendConfig   `json:"image_backends"`
	AudioBackends  []AudioBackendConfig   `json:"audio_backends"`
	OllamaDefaults OllamaDefaultsConfig   `json:"ollama_defaults"`
	Models         ModelsConfig           `json:"models"`
	Policy         PolicyConfig           `json:"policy"`
	Documents      DocumentsConfig        `json:"documents"`
	Scheduler      SchedulerConfig        `json:"scheduler"`
}

// ServerConfig controls HTTP server behaviour.
type ServerConfig struct {
	RequestTimeoutSeconds int  `json:"request_timeout_seconds"`
	MaxRequestBodyBytes   int  `json:"max_request_body_bytes"`
	EnableRequestLogging  bool `json:"enable_request_logging"`
	EnableDebugLogging    bool `json:"enable_debug_logging"`
}

// HostConfig represents a physical or logical host that backends run on.
type HostConfig struct {
	ID            string `json:"id"`
	MaxActiveJobs int    `json:"max_active_jobs"`
}

// OllamaBackendConfig describes a single Ollama backend instance.
type OllamaBackendConfig struct {
	ID                    string         `json:"id"`
	URL                   string         `json:"url"`
	Host                  string         `json:"host"`
	MaxConcurrentRequests int            `json:"max_concurrent_requests"`
	Enabled               bool           `json:"enabled"`
	KeepAlive             string         `json:"keep_alive,omitempty"`
	Think                 *bool          `json:"think,omitempty"`
	Options               *OllamaOptions `json:"options,omitempty"`
}

// ImageBackendConfig describes a single image-generation backend.
type ImageBackendConfig struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Host                 string `json:"host"`
	MaxConcurrentRequests int    `json:"max_concurrent_requests"`
	Enabled              bool   `json:"enabled"`
}

// AudioBackendConfig describes a single audio backend.
type AudioBackendConfig struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Host                 string `json:"host"`
	MaxConcurrentRequests int    `json:"max_concurrent_requests"`
	Enabled              bool   `json:"enabled"`
}

// OllamaDefaultsConfig holds default parameters applied to every Ollama request.
// These are fallbacks; per-backend and per-alias overrides take precedence.
type OllamaDefaultsConfig struct {
	KeepAlive string `json:"keep_alive"`
	Think     *bool  `json:"think,omitempty"`
}

// OllamaOptions maps to the "options" object in the Ollama /api/chat request.
type OllamaOptions struct {
	NumThread   int     `json:"num_thread"`
	NumCtx      int     `json:"num_ctx"`
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
}

// ModelsConfig controls model exposure and aliases.
type ModelsConfig struct {
	ExposeNativeOllamaModels bool          `json:"expose_native_ollama_models"`
	Aliases                  []AliasConfig `json:"aliases"`
}

// AliasConfig maps a user-facing model name to concrete model candidates.
type AliasConfig struct {
	Name         string         `json:"name"`
	PrimaryModel string         `json:"primary_model"`
	BackupModels []string       `json:"backup_models"`
	Overrides    AliasOverrides `json:"overrides"`
}

// AliasOverrides optionally overrides Ollama defaults for a specific alias.
// Pointer fields distinguish "not set" (nil) from "explicitly set to zero value".
type AliasOverrides struct {
	KeepAlive string         `json:"keep_alive,omitempty"`
	Think     *bool          `json:"think,omitempty"`
	Options   *OllamaOptions `json:"options,omitempty"`
}

// PolicyConfig controls how the proxy handles client inputs.
type PolicyConfig struct {
	IgnoreUnsupportedFields        bool `json:"ignore_unsupported_fields"`
	LogUnsupportedFields           bool `json:"log_unsupported_fields"`
	AllowClientOverrideOptions     bool `json:"allow_client_override_options"`
	AllowAnthropicThinkingOverride bool `json:"allow_anthropic_thinking_override"`
}

// DocumentsConfig controls proxy-native document processing.
type DocumentsConfig struct {
	Enabled            bool   `json:"enabled"`
	DefaultMode        string `json:"default_mode"`
	OCREnabled         bool   `json:"ocr_enabled"`
	OCRMode            string `json:"ocr_mode"`
	AllowURLInput      bool   `json:"allow_url_input"`
	AllowBase64Input   bool   `json:"allow_base64_input"`
	PreparationWorkers int    `json:"preparation_workers"`
}

// SchedulerConfig controls the job scheduling behaviour.
type SchedulerConfig struct {
	Strategy                        string      `json:"strategy"`
	TopNLookahead                   int         `json:"top_n_lookahead"`
	QueueMaxPending                 int         `json:"queue_max_pending"`
	AgingPerSecond                  float64     `json:"aging_per_second"`
	UnknownTokensPerSecond          float64     `json:"unknown_tokens_per_second"`
	UnknownColdLoadPenaltySeconds   float64     `json:"unknown_cold_load_penalty_seconds"`
	ExplorationBonus                float64     `json:"exploration_bonus"`
	AliasSubstitutionPenaltySeconds float64     `json:"alias_substitution_penalty_seconds"`
	DisruptionFactor                float64     `json:"disruption_factor"`
	Retry                           RetryConfig `json:"retry"`
}

// RetryConfig controls retry behaviour for failed requests.
type RetryConfig struct {
	MaxAttempts                    int  `json:"max_attempts"`
	StreamingRetryBeforeFirstToken bool `json:"streaming_retry_before_first_token"`
	StreamingRetryAfterFirstToken  bool `json:"streaming_retry_after_first_token"`
}
