# ARCHITECTURE.md

# Ollama Native Proxy — Architecture and Implementation Specification

---

## Boundaries

**This document owns:**
- System overview and core architectural rules
- Stack, runtime, and toolchain
- Package layout and directory structure
- Component design: scheduler, backend discovery, API translation, document processing, streaming, metrics
- Data flow: how requests move through the system
- Configuration schema and loading rules
- Model resolution, alias logic, scheduling algorithm
- Implementation milestones and acceptance criteria
- Testing requirements

**This document does NOT contain:**
- Agent rules, git policy, build commands, project constitution → **[AGENTS.md](AGENTS.md)**
- API reference (endpoint shapes and responses), user journeys, debug UI layout, UX principles → **[DESIGN.md](DESIGN.md)**
- Active tasks, open questions, decision history, live action stream → **[CONTEXT.md](CONTEXT.md)**
- Quick start, env reference for operators → **[README.md](README.md)**

**Rewrite policy:** On design/architecture changes. Keep in sync with DESIGN.md — API translation logic here, endpoint behavior there.

---

## 1. System Overview

Ollama Native Proxy is a Docker-first Go HTTP gateway that exposes practical OpenAI-compatible and Anthropic-compatible APIs while routing actual local inference work through Ollama native APIs.

The core rule is:

```text
OpenAI/Anthropic-compatible ingress
→ proxy translation/scheduling
→ Ollama native /api/chat
```

Do not call Ollama `/v1/*` compatibility endpoints for chat. Use native Ollama endpoints because the proxy must enforce backend runtime controls such as:

- `think`
- `keep_alive`
- `options.num_thread`
- `options.num_ctx`
- `options.num_predict`
- sampling options
- model alias mapping
- backend scheduling policy

The original uploaded specification establishes this baseline and the required OpenAI Responses, Chat Completions, and Anthropic Messages ingress APIs.

---

## 2. Goals

## 2.1 Primary goals

- Expose OpenAI-compatible APIs useful to LiteLLM, Open WebUI, OpenClaw, OpenAI SDKs, and Codex-style clients.
- Expose Anthropic Messages-compatible APIs useful to Claude Code-style clients.
- Route chat and vision requests to Ollama native `/api/chat`.
- Support multiple Ollama backends.
- Learn backend/model runtime performance continuously in memory.
- Schedule work using host/backend/model-aware logic.
- Avoid unnecessary model switching.
- Support aliases with primary and backup models.
- Keep large payloads on disk, not in RAM.
- Support proxy-native document processing.
- Support OpenAI Responses inline base64 file input.
- Support external image generation backends through `/v1/images/generations`.
- Provide Prometheus metrics from day 0.
- Provide simple read-only debug UI from day 0.

## 2.2 Non-goals for v1

Do not implement:

- authentication
- client rate limiting
- hot config reload
- persistent scheduler stats
- database
- remote URL file input
- OpenAI Files API
- OpenAI Assistants API
- OpenAI Batches API
- embeddings
- proxy-side tool execution
- distributed scheduling
- image edits/variations
- backend-specific debug model names like `qwen:30b@backend1`
- strict validation of harmless unsupported fields
- storing large documents/images/base64 payloads in memory

---

## 3. Implementation Language and Runtime

Use Go.

Recommended standard library foundation:

- `net/http`
- `context`
- `encoding/json`
- `mime/multipart`
- `os`
- `os/exec`
- `embed`
- `sync`
- `time`

Recommended external libraries:

- Prometheus Go client
- optionally a small UUID/id generator

Avoid large web frameworks.

## 3.1 Docker-first runtime

The container must include:

- Go proxy binary
- PDF text extraction tools
- PDF page rendering tools
- OCR tooling
- image conversion/resizing tooling

Document rendering remains inside the proxy container in v1.

Do not split document preparation into a separate service yet.

---

## 4. Environment and Storage

Only support these environment variables:

```bash
PROXY_LISTEN=0.0.0.0:4000
PROXY_STORAGE_DIR=/data
```

Defaults:

```text
PROXY_LISTEN defaults to 0.0.0.0:4000
PROXY_STORAGE_DIR defaults to /data
```

Derived paths:

```text
/data/config/config.json
/data/tmp/
/data/cache/
/data/uploads/
/data/documents/
/data/images/
/data/audio/
```

Logs go to stdout/stderr.

No log files.

On startup:
1. read env vars
2. create derived directories if missing
3. load config from `$PROXY_STORAGE_DIR/config/config.json`
4. start backend discovery
5. start scheduler
6. start HTTP server

No hot config reload.

Config changes require container restart.

---

## 5. Configuration Schema

## 5.1 Example config

```json
{
  "server": {
    "request_timeout_seconds": 900,
    "max_request_body_bytes": 10485760,
    "enable_request_logging": true,
    "enable_debug_logging": false
  },
  "hosts": [
    {
      "id": "default",
      "max_active_jobs": 1
    }
  ],
  "ollama_backends": [
    {
      "id": "ollama-main",
      "url": "http://127.0.0.1:11434",
      "host": "default",
      "max_concurrent_requests": 1,
      "enabled": true
    }
  ],
  "image_backends": [
    {
      "id": "image-main",
      "type": "openai_compatible",
      "url": "http://127.0.0.1:8080/v1",
      "host": "default",
      "max_concurrent_requests": 1,
      "enabled": false
    }
  ],
  "audio_backends": [],
  "ollama_defaults": {
    "keep_alive": "10m",
    "think": false,
    "options": {
      "num_thread": 8,
      "num_ctx": 8192,
      "temperature": 0.2,
      "top_p": 0.9
    }
  },
  "models": {
    "expose_native_ollama_models": true,
    "aliases": [
      {
        "name": "qwen-instruct",
        "primary_model": "qwen:30b",
        "backup_models": ["qwen:14b", "qwen:72b"],
        "overrides": {
          "think": false,
          "options": {
            "temperature": 0.2
          }
        }
      },
      {
        "name": "qwen-thinking",
        "primary_model": "qwen:30b",
        "backup_models": ["qwen:14b", "qwen:72b"],
        "overrides": {
          "think": true,
          "options": {
            "temperature": 0.2
          }
        }
      }
    ]
  },
  "policy": {
    "ignore_unsupported_fields": true,
    "log_unsupported_fields": true,
    "allow_client_override_options": false,
    "allow_anthropic_thinking_override": false
  },
  "documents": {
    "enabled": true,
    "default_mode": "auto",
    "ocr_enabled": true,
    "ocr_mode": "auto",
    "allow_url_input": false,
    "allow_base64_input": true,
    "preparation_workers": 1
  },
  "scheduler": {
    "strategy": "priority_host_model_affinity",
    "top_n_lookahead": 64,
    "queue_max_pending": 100,
    "aging_per_second": 0.05,
    "unknown_tokens_per_second": 5.0,
    "unknown_cold_load_penalty_seconds": 30.0,
    "alias_substitution_penalty_seconds": 8.0,
    "disruption_factor": 0.25,
    "retry": {
      "max_attempts": 2,
      "streaming_retry_before_first_token": true,
      "streaming_retry_after_first_token": false
    }
  }
}
```

## 5.2 Config rules

- Config is loaded once at startup.
- No hot reload.
- `host` on a backend is optional, but strongly preferred.
- If backend host is missing, derive host ID from URL hostname.
- Aliases may override Ollama options.
- Native Ollama model names may be exposed if enabled.
- Native model requests mean exact model only.
- Alias backup models apply only to aliases.
- No global fallback model.
- No backend-specific debug model names in v1.

---

## 6. API Surface

## 6.1 Health

```http
GET /healthz
GET /readyz
```

`/healthz` returns proxy process health.

`/readyz` returns ready only if at least one enabled backend is reachable or otherwise considered usable.

## 6.2 OpenAI-compatible endpoints

Implement:

```http
GET  /v1/models
GET  /v1/models/{model}
POST /v1/chat/completions
POST /v1/responses
POST /v1/images/generations
```

Optional if configured:

```http
POST /v1/audio/transcriptions
POST /v1/audio/speech
```

Do not implement in v1:

```text
/v1/embeddings
/v1/files
/v1/batches
/v1/assistants
/v1/threads
/v1/images/edits
/v1/images/variations
```

## 6.3 Anthropic-compatible endpoints

Implement:

```http
POST /v1/messages
POST /v1/messages/count_tokens
```

## 6.4 Proxy-native endpoint

Implement:

```http
POST /proxy/documents/process
```

## 6.5 Observability

Implement:

```http
GET /metrics
GET /debug
GET /debug/backends
GET /debug/queue
GET /debug/scheduler
GET /debug/models
GET /debug/hosts
GET /debug/config
```

---

## 7. Model Discovery and Resolution

## 7.1 Ollama discovery

For each Ollama backend, poll:

```http
GET /api/tags
GET /api/ps
```

Use `/api/tags` for available models.

Use `/api/ps` for loaded models.

Suggested intervals:

```text
/api/tags every 60 seconds
/api/ps every 5 seconds
```

Refresh immediately after backend recovery.

## 7.2 Exposed models

`GET /v1/models` returns:

- configured aliases
- native Ollama model names, if `expose_native_ollama_models=true`

Do not expose duplicate IDs.

## 7.3 Resolution order

For a request model ID:

1. Exact alias match
2. Native Ollama model name if native exposure is enabled
3. model-not-found error

No global fallback.

## 7.4 Alias candidate models

Alias:

```json
{
  "name": "qwen-balanced",
  "primary_model": "qwen:30b",
  "backup_models": ["qwen:14b", "qwen:72b"]
}
```

resolves to candidates:

```text
qwen:30b
qwen:14b
qwen:72b
```

Only the scheduler decides which candidate to use.

Primary model has no substitution cost.

Backup models have a fixed substitution cost.

Do not distinguish up-model and down-model.

## 7.5 Native model behavior

A native request:

```text
qwen:30b
```

means:

```text
qwen:30b only
```

No backup substitution.

---

## 8. Ollama Request Construction

Every chat/vision/text generation request to Ollama must ultimately call:

```http
POST {backend.url}/api/chat
```

Base Ollama request shape:

```json
{
  "model": "qwen:30b",
  "messages": [
    {
      "role": "user",
      "content": "Hello"
    }
  ],
  "stream": false,
  "think": false,
  "keep_alive": "10m",
  "options": {
    "num_thread": 8,
    "num_ctx": 8192,
    "temperature": 0.2,
    "top_p": 0.9,
    "num_predict": 512
  }
}
```

Apply settings in this order:

1. global `ollama_defaults`
2. alias overrides, if request used an alias
3. safe client overrides only if `allow_client_override_options=true`
4. Anthropic thinking override only if `allow_anthropic_thinking_override=true`

Remove `allow_client_override_keep_alive`; keep-alive is backend/alias config only.

No global `strip_reasoning_from_output` flag.

---

## 9. Scheduler Architecture

## 9.1 Required design

Implement a central, event-driven, non-preemptive scheduler.

The scheduler chooses:

```text
job + backend + concrete model
```

Do not implement simple round-robin.

Do not implement per-backend FIFO queues as the main scheduling mechanism.

The scheduler must be aware of:

- job priority
- job age
- requested alias/native model
- candidate concrete models
- backend available models
- backend loaded models
- host capacity
- backend capacity
- backend/model health
- learned tokens/sec
- learned time-to-first-token
- learned cold load time
- model switch disruption cost

## 9.2 Job types and priorities

Default priorities:

```text
chat:             100
tool:              90
vision:            90
image_generation:  60
audio:             60
document:          30
```

Document chunk jobs are low priority.

Aging:

```text
effective_priority = base_priority + wait_seconds * aging_per_second
```

## 9.3 Runtime-learned stats

Stats are memory-only.

Track per:

```text
backend + model
```

Fields:

```text
samples
avg_tokens_per_second
avg_time_to_first_token
avg_warm_time_to_first_token
avg_cold_load_time
avg_total_latency
avg_failure_rate
last_success
last_failure
```

Use EWMA:

```text
new_avg = alpha * latest + (1 - alpha) * old_avg
```

Suggested:

```text
alpha = 0.2
```

## 9.4 Backend state

Track:

```text
backend_id
kind: ollama | image | audio
url
host_id
enabled
healthy
active_jobs
max_concurrent_requests
available_models
loaded_models
model_stats
model_health
```

## 9.5 Host state

Track:

```text
host_id
max_active_jobs
active_jobs
```

A job must acquire both:
- host lease
- backend lease

before running.

## 9.6 Assignment scoring

On each scheduler pass:

1. inspect top `N` pending jobs, default `64`
2. generate all valid job/backend/model assignments
3. score each assignment
4. run the lowest-cost valid assignment
5. repeat until no capacity remains

Cost formula:

```text
cost =
  backend_wait_cost
+ host_load_cost
+ model_switch_cost
+ estimated_generation_time
+ disruption_cost
+ substitution_cost
+ failure_penalty
- priority_credit
- aging_credit
```

### Priority credit

```text
priority_credit = base_priority / 10
```

### Aging credit

```text
aging_credit = wait_seconds * aging_per_second
```

### Estimated generation time

```text
estimated_generation_time = estimated_output_tokens / learned_tokens_per_second
```

If unknown:

```text
learned_tokens_per_second = unknown_tokens_per_second
```

### Model switch cost

If the model is already loaded on the backend:

```text
model_switch_cost = 0
```

If not loaded:

```text
model_switch_cost = learned cold load time for backend+model
```

If unknown:

```text
model_switch_cost = unknown_cold_load_penalty_seconds
```

### Substitution cost

For alias backup model:

```text
substitution_cost = alias_substitution_penalty_seconds
```

For alias primary model or native model:

```text
substitution_cost = 0
```

### Disruption cost

If backend has model `A` loaded and candidate requires model `B`:

```text
disruption_cost =
  waiting_jobs_compatible_with_loaded_model
  × learned_reload_cost_for_loaded_model
  × disruption_factor
```

If no loaded model or same model:

```text
disruption_cost = 0
```

## 9.7 Dispatch loop pseudocode

```go
func dispatchLoop() {
    for {
        waitForSchedulerEvent()

        for {
            assignment := findBestAssignment()
            if assignment == nil {
                break
            }

            leaseHost(assignment.HostID)
            leaseBackend(assignment.BackendID)
            markJobRunning(assignment.JobID)

            go runAssignment(assignment)
        }
    }
}
```

Scheduler events:

- new job submitted
- job completed
- backend health changed
- backend/model cooldown expired
- host capacity freed
- backend loaded model state changed

---

## 10. Retry Policy

## 10.1 Streaming

Retry only before the first meaningful model output reaches the client.

Do not send initial SSE events too early if doing so would prevent retry.

For OpenAI Chat Completions streaming:
- do not immediately send `delta.role=assistant`
- wait for first backend content/tool delta
- then send role chunk plus first content/tool chunk

If backend fails before first meaningful output:
- record failure
- release leases
- retry another eligible assignment if attempts remain

If backend fails after first output:
- do not retry
- terminate stream with best compatible behavior
- record metric

## 10.2 Non-streaming

Retry is allowed until final response is sent.

Use configured max attempts.

## 10.3 Document jobs

Document chunk inference may be retried internally.

---

## 11. Backend Health

## 11.1 Backend-level health

Disable whole backend when:

- `/api/tags` repeatedly fails
- connection refused
- timeout
- backend-wide 5xx pattern

Recovery:
- cooldown
- periodic probe
- re-enable after successful probe

## 11.2 Backend-model health

Track per:

```text
backend + model
```

Policy:

```text
first consecutive model failure:
  temporary cooldown

second consecutive model failure:
  disable backend+model for current runtime
```

Success resets consecutive failure count.

This state is memory-only.

---

## 12. Disk-backed Payload Handling

Keep RAM free.

Large payloads must be written to disk immediately.

Queue items store:

```text
job id
metadata
model/capability info
priority
temp file paths
```

Queue items must not store:
- entire base64 strings
- decoded images
- full PDFs
- full rendered pages
- large extracted text chunks

Use derived storage paths:

```text
/data/tmp
/data/uploads
/data/documents
/data/images
/data/audio
```

Add:
- cleanup on completion
- cleanup on cancellation
- periodic stale cleanup
- minimum free disk-space check

If disk is unsafe/full, return clean overload error.

---

## 13. Document Processing Architecture

## 13.1 Supported inputs

V1 supports:

- multipart upload to `/proxy/documents/process`
- base64 inline file input in `/v1/responses`
- base64 image/document content where compatible

V1 does not support remote URL input.

## 13.2 Supported file types

V1:

```text
.txt
.md
.pdf
.png
.jpg
.jpeg
.webp
```

Later:

```text
.docx
.html
.csv
.json
```

## 13.3 Modes

Support:

```text
auto
text_only
vision_pages
ocr
hybrid
```

Default:

```text
auto
```

## 13.4 Auto mode

For PDF:

```text
try text extraction
if enough text found:
  use extracted text
else:
  render pages as images
  send images to vision model if available
else:
  OCR if enabled
```

For scanned PDFs/images:

```text
prefer vision model if selected model supports vision
fallback to OCR if no vision support
```

OCR is optional helper. Do not OCR everything by default.

## 13.5 Preparation queue

Use a separate document preparation queue.

Preparation handles:

- base64 decode to disk
- multipart spool to disk
- PDF text extraction
- PDF page rendering
- optional OCR
- image resizing
- chunking

Default workers:

```text
1
```

## 13.6 Inference chunks

Document inference must be split into low-priority jobs.

Do not let one document request hold an Ollama backend for the full document.

Pipeline:

```text
document request
→ prepare chunks/pages
→ low-priority chunk inference jobs
→ combine result
→ final response
```

---

## 14. API Translation

## 14.1 OpenAI Chat Completions

Endpoint:

```http
POST /v1/chat/completions
```

Support:

- `model`
- `messages`
- `stream`
- `max_tokens`
- `temperature`
- `top_p`
- `stop`
- `tools`
- `tool_choice`
- image input where compatible

Mapping:

```text
model → alias/native resolution
messages → Ollama messages
stream → Ollama stream
max_tokens → options.num_predict
temperature → options.temperature if client override allowed
top_p → options.top_p if client override allowed
stop → options.stop if client override allowed
tools → Ollama tools if supported
```

Unsupported harmless fields:
- ignore
- log only
- do not return client warnings

## 14.2 OpenAI Responses

Endpoint:

```http
POST /v1/responses
```

Support:

- `model`
- `input`
- `instructions`
- `stream`
- `temperature`
- `top_p`
- `max_output_tokens`
- base64 inline file input
- image input
- tools pass-through where practical

Convert string input:

```json
{
  "input": "Hello"
}
```

to:

```json
{
  "messages": [
    {
      "role": "user",
      "content": "Hello"
    }
  ]
}
```

Convert array input into chat messages.

Prepend `instructions` as system message.

Stateful features are out of scope:
- `previous_response_id`
- `conversation`
- `background`

Ignore if harmless; return clean 400 only if impossible to handle safely.

## 14.3 Anthropic Messages

Endpoint:

```http
POST /v1/messages
```

Support:

- `model`
- `messages`
- `system`
- `max_tokens`
- `temperature`
- `top_p`
- `top_k`
- `stop_sequences`
- `stream`
- `tools`
- `tool_choice`
- `thinking`

Thinking rule:

```text
backend config controls think by default
Anthropic thinking override only works if allow_anthropic_thinking_override=true
```

## 14.4 Token count

Endpoint:

```http
POST /v1/messages/count_tokens
```

Use approximation in v1:

```text
input_tokens ≈ bytes / 4
```

---

## 15. Tool Handling

Do not execute tools.

In scope:
- pass tool schemas through
- pass tool calls through
- translate tool call shapes where needed
- pass tool results back through

Out of scope:
- tool registry
- HTTP tool execution
- shell execution
- secrets
- sandboxing
- permission model
- audit trail for tool execution

Clients such as Open WebUI, OpenClaw, LiteLLM, or agent runtimes should execute tools.

---

## 16. Image Generation

Endpoint:

```http
POST /v1/images/generations
```

Route to image backend pool.

Image backends:
- are separate from Ollama backends
- may share a host
- must participate in host-aware scheduling

If no image backend is enabled, return clean 503.

Do not implement:
- image edits
- image variations

---

## 17. Audio

Audio support is optional and backend-dependent.

Potential endpoints:

```http
POST /v1/audio/transcriptions
POST /v1/audio/speech
```

Do not force audio through Ollama unless backend supports it.

If unsupported, return clean compatible error.

---

## 18. Streaming

Use SSE.

Implement reusable stream translators:

- OpenAI Chat Completions SSE
- OpenAI Responses SSE
- Anthropic Messages SSE

Ollama native stream is JSON chunks/lines.

Translate backend stream to target API shape.

Required behavior:
- flush after chunks
- detect client disconnect
- cancel backend immediately
- retry only before first meaningful output
- no retry after output has reached client

---

## 19. Cancellation

If client disconnects:

1. cancel request context
2. abort backend HTTP request
3. release backend lease
4. release host lease
5. cleanup temp files when safe
6. record cancellation metric

No background continuation in v1.

---

## 20. Metrics

Expose Prometheus metrics at:

```http
GET /metrics
```

Required metrics:

```text
proxy_requests_total{api,endpoint,status}
proxy_request_duration_seconds{api,endpoint}
proxy_queue_depth{kind,priority}
proxy_queue_wait_seconds{kind}
proxy_jobs_started_total{kind}
proxy_jobs_completed_total{kind}
proxy_jobs_failed_total{kind}
proxy_backend_up{backend}
proxy_backend_active_jobs{backend}
proxy_backend_disabled{backend}
proxy_backend_model_disabled{backend,model}
proxy_backend_model_tps{backend,model}
proxy_backend_model_ttft_seconds{backend,model}
proxy_backend_model_cold_load_seconds{backend,model}
proxy_backend_failures_total{backend,model,scope}
proxy_host_active_jobs{host}
proxy_host_capacity{host}
proxy_scheduler_assignment_total{backend,model,kind}
proxy_scheduler_substitution_total{alias,selected_model}
proxy_scheduler_model_switch_total{backend,from_model,to_model}
proxy_stream_retry_before_first_token_total
proxy_stream_failure_after_first_token_total
proxy_document_jobs_total{status}
proxy_image_jobs_total{status}
```

---

## 21. Debug UI

Serve a read-only debug UI at:

```http
GET /debug
```

Use embedded static files:

```text
index.html
app.js
style.css
```

No frontend framework.

No write actions.

The UI polls:

```http
GET /debug/backends
GET /debug/queue
GET /debug/scheduler
GET /debug/models
GET /debug/hosts
GET /debug/config
```

Show:

- service status
- host load
- backend health
- loaded models
- available models
- queue depth
- learned backend/model stats
- disabled backend/model state
- recent assignments
- recent errors

Redact any secrets if they are added later.

Never show prompt text or document content in debug UI by default.

---

## 22. Error Handling

Return clean API-compatible errors.

Important cases:

## Unknown model

HTTP 404:

```json
{
  "error": {
    "message": "Unknown model: requested-model",
    "type": "invalid_request_error",
    "code": "model_not_found"
  }
}
```

## No backend available

HTTP 503:

```json
{
  "error": {
    "message": "No backend available",
    "type": "server_error",
    "code": "backend_unavailable"
  }
}
```

## Queue full

HTTP 503 or 429-style overload shape:

```json
{
  "error": {
    "message": "Proxy queue is full",
    "type": "server_error",
    "code": "queue_full"
  }
}
```

## Unsupported but harmless field

Ignore and log only.

Do not send warning headers.

## Malformed request

HTTP 400 with clean message.

---

## 23. Logging

Logs go to stdout/stderr.

Log:
- request ID
- endpoint
- model ID requested
- resolved model
- backend selected
- host selected
- queue wait
- time to first token
- total latency
- token counts where available
- error code
- retry count
- alias substitution

Do not log:
- authorization headers
- API keys
- cookies
- full prompts
- full document text
- base64 payloads

Unsupported fields are logged only when configured.

---

## 24. Suggested Go Package Layout

```text
cmd/proxy/main.go

internal/config/
  config.go
  load.go
  defaults.go

internal/httpapi/
  router.go
  middleware.go
  health.go

internal/openai/
  models.go
  chat.go
  responses.go
  images.go
  audio.go
  sse_chat.go
  sse_responses.go

internal/anthropic/
  messages.go
  count_tokens.go
  sse_messages.go

internal/ollama/
  client.go
  chat.go
  tags.go
  ps.go
  types.go

internal/backend/
  registry.go
  health.go
  model_state.go

internal/scheduler/
  job.go
  queue.go
  scheduler.go
  scoring.go
  leases.go
  stats.go
  retry.go

internal/storage/
  paths.go
  spool.go
  cleanup.go

internal/documents/
  handler.go
  prepare_queue.go
  extract_text.go
  render_pdf.go
  ocr.go
  chunk.go
  combine.go

internal/imagesvc/
  client.go
  openai_compatible.go

internal/audiosvc/
  client.go

internal/metrics/
  prometheus.go

internal/debugui/
  handlers.go
  static/
    index.html
    app.js
    style.css

internal/logging/
  logging.go

internal/errors/
  errors.go
  render.go
```

---

## 25. Implementation Milestones

## Milestone 1: skeleton

Implement:
- env var loading
- storage directory creation
- config loading
- HTTP server
- `/healthz`
- stdout logging

Acceptance:
- container starts
- `/healthz` returns OK

## Milestone 2: backend discovery

Implement:
- Ollama backend registry
- `/api/tags` polling
- `/api/ps` polling
- `/debug/backends`
- `/debug/models`
- `/v1/models`

Acceptance:
- aliases visible
- native models visible when enabled
- loaded models visible

## Milestone 3: non-streaming chat

Implement:
- `/v1/chat/completions`
- model resolution
- alias overrides
- call Ollama `/api/chat`
- OpenAI-compatible response

Acceptance:
- OpenAI SDK can call non-streaming chat
- Ollama options are injected

## Milestone 4: scheduler

Implement:
- global queue
- top-N lookahead
- host leases
- backend leases
- backend/model stats
- assignment scoring
- metrics

Acceptance:
- requests prefer already loaded model
- alias can use loaded backup model
- host capacity prevents concurrent backend jobs on same host
- document jobs have lower priority

## Milestone 5: streaming chat

Implement:
- Ollama stream reader
- OpenAI Chat SSE writer
- retry before first meaningful token
- no retry after first token
- cancellation on disconnect

Acceptance:
- streaming works
- pre-token failure retries
- post-token failure does not retry

## Milestone 6: Responses API

Implement:
- `/v1/responses`
- non-streaming response shape
- streaming response shape
- string and array input conversion
- instructions mapping

Acceptance:
- Codex-style Responses calls work

## Milestone 7: Anthropic Messages

Implement:
- `/v1/messages`
- streaming and non-streaming
- `/v1/messages/count_tokens`
- Anthropic thinking override rule

Acceptance:
- Claude Code-style gateway calls work

## Milestone 8: documents

Implement:
- `/proxy/documents/process`
- base64 inline file input in `/v1/responses`
- PDF text extraction
- PDF rendering
- optional OCR
- document preparation queue
- low-priority chunk inference
- combine result

Acceptance:
- text PDF summary works
- scanned PDF works through vision or OCR
- document jobs do not monopolize backend

## Milestone 9: image generation

Implement:
- `/v1/images/generations`
- image backend pool
- host-aware scheduling

Acceptance:
- image requests route to image backend
- host capacity is shared with Ollama backend

## Milestone 10: observability

Implement:
- `/metrics`
- `/debug`
- debug JSON endpoints
- static debug UI

Acceptance:
- UI shows backend health, queue, models, host load, learned stats
- Prometheus metrics are exported

---

## 26. Testing Requirements

## Unit tests

Test:

- config defaults
- model resolution
- alias candidate generation
- scheduler scoring
- host lease logic
- backend lease logic
- streaming retry decision
- backend-model disable logic
- document mode selection
- OpenAI response shaping
- Anthropic response shaping

## Integration tests

Use fake Ollama server with:

```text
/api/tags
/api/ps
/api/chat
```

Test:

- non-streaming chat
- streaming chat
- backend failure before first token retries
- backend failure after first token does not retry
- alias uses loaded backup when primary is cold
- host capacity blocks concurrent jobs
- backend-model disabled after repeated failure
- `/v1/models` contains aliases and native models
- `/metrics` returns Prometheus output

## Smoke scripts

Provide:

```text
scripts/smoke_chat.sh
scripts/smoke_stream_chat.sh
scripts/smoke_responses.sh
scripts/smoke_anthropic.sh
scripts/smoke_document.sh
scripts/smoke_images.sh
scripts/smoke_metrics.sh
```

---

## 27. Acceptance Criteria

The implementation is acceptable when:

1. It runs in Docker with only `PROXY_LISTEN` and `PROXY_STORAGE_DIR`.
2. It reads config from `/data/config/config.json`.
3. It exposes `/v1/models`, `/v1/chat/completions`, `/v1/responses`, `/v1/messages`, and `/v1/messages/count_tokens`.
4. It calls Ollama native `/api/chat`.
5. It supports streaming translation.
6. It retries streaming only before first meaningful output.
7. It cancels backend work immediately on client disconnect.
8. It discovers native Ollama models.
9. It supports aliases with primary and backup models.
10. It schedules based on backend, model, loaded state, host capacity, and learned stats.
11. It stores learned stats in memory only.
12. It spools documents/images/base64 payloads to disk.
13. It supports `/proxy/documents/process`.
14. It supports base64 inline file input in `/v1/responses`.
15. It exports Prometheus metrics.
16. It serves read-only `/debug` UI.
17. It does not implement auth, rate limiting, hot reload, Files API, proxy-side tool execution, or remote URL input.

---

## 28. Most Important Anti-Drift Rules

1. Do not route chat through Ollama `/v1/*`.
2. Do not replace the scheduler with round-robin.
3. Do not store large payloads in RAM.
4. Do not execute tools in the proxy.
5. Do not add auth in v1.
6. Do not add hot reload in v1.
7. Do not add remote URL file input in v1.
8. Do not implement OpenAI Files API in v1.
9. Do not expose backend-specific model IDs in v1.
10. Do not make alias fallback global.
11. Do not persist scheduler stats in v1.
12. Do not let document jobs monopolize an Ollama backend.
13. Do not retry streams after output reached the client.
14. Do not expose prompt/document contents in debug UI.
15. Do not log secrets or payloads.
