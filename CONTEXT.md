# Project Context

Last updated: 2026-05-22

---

## Active Tasks

### Milestone 1: skeleton (completed)

### Milestone 2: backend discovery (completed)

**Overview**: 6 ordered tasks. Each task creates specific files, has clear dependencies, and ends with `go vet` and `go test` passing. The milestone implements the Ollama backend registry, polling for `/api/tags` and `/api/ps`, plus three HTTP endpoints: `/debug/backends`, `/debug/models`, `/v1/models`.

**Task 1: Ollama API types and HTTP client** *(no dependencies)*
- Files: `internal/ollama/types.go`, `internal/ollama/client.go`
- `types.go`: `TagsResponse`, `TagsModel`, `PSResponse`, `PSModel`, `ModelDetail` structs matching Ollama API JSON shapes
- `client.go`: `NewHTTPClient() *http.Client` factory (30s timeout)
- Tests: marshal/unmarshal types, client creation

**Task 2: FetchTags and FetchPS** *(depends on Task 1)*
- Files: `internal/ollama/tags.go`, `internal/ollama/ps.go`
- `tags.go`: `FetchTags(client *http.Client, baseURL string) (*TagsResponse, error)`
- `ps.go`: `FetchPS(client *http.Client, baseURL string) (*PSResponse, error)`
- Tests: httptest servers covering valid JSON, 500, non-JSON, unreachable

**Task 3: Backend registry with polling** *(depends on Task 2)*
- Files: `internal/backend/model_state.go`, `internal/backend/health.go`, `internal/backend/registry.go`
- `model_state.go`: `ModelState` struct, helpers to extract model names from tags/ps responses
- `health.go`: `HealthState` with `SetHealthy`, `SetUnhealthy`, `Snapshot()`
- `registry.go`: `Registry` struct, `NewRegistry`, `Start` (poll goroutines: tags/60s, ps/5s), `Stop`, `BackendSnapshots()`, `ModelsConfig()`
- Tests: registry creation, snapshot independence, poll cycle, failure/recovery

**Task 4: /debug/backends and /debug/models handlers** *(depends on Task 3)*
- Files: `internal/httpapi/backends.go`, `internal/httpapi/models.go`
- `DebugBackendsHandler`: returns JSON array of backend states, nil-safe
- `DebugModelsHandler`: returns aliases + native_models + loaded_models JSON
- Tests: nil registry, populated registry, multi-backend

**Task 5: /v1/models OpenAI-compatible endpoint** *(depends on Task 3)*
- Files: `internal/openai/models.go` (new package)
- `ModelsHandler`: OpenAI `/v1/models` response shape with dedup (alias over native), sorting (aliases first)
- Tests: native exposure on/off, dedup, nil registry

**Task 6: Wire into router and main.go** *(depends on Tasks 4 and 5)*
- Modify: `internal/httpapi/router.go`, `cmd/proxy/main.go`, `cmd/proxy/main_test.go`
- `NewRouter(logger, registry)`: register `/debug/backends`, `/debug/models`, `/v1/models`; nil-safe
- `main.go`: create registry, Start/Stop, pass to router
- `main_test.go`: pass nil registry

Dependency order: T1 → T2 → T3 → T4+T5 (parallel) → T6.

### Milestone 3: non-streaming chat (completed)

**Overview**: 5 tasks. T1 (types) and T3 (resolution) parallel → T2 (SendChat, depends T1) → T4 (handler, depends T1+T2+T3) → T5 (wire route). Pre-scheduler: simple first-healthy-backend selection.

**Task 1: Ollama Chat types** *(no dependencies)*
- Files: `internal/ollama/types.go` (add types)
- Add `ChatRequest` (Model, Messages, Stream, Think *bool, KeepAlive, Options ChatOptions, Tools json.RawMessage), `ChatMessage` (Role, Content), `ChatOptions` (NumThread, NumCtx, Temperature, TopP, NumPredict, Stop — omitempty), `ChatResponse` (Model, CreatedAt, Message, Done, durations/counts with omitempty)
- Tests: roundtrip marshal/unmarshal

**Task 2: Ollama SendChat** *(depends on Task 1)*
- Files: `internal/ollama/chat.go`
- `SendChat(client, baseURL, req *ChatRequest) (*ChatResponse, error)` — POST /api/chat, follows FetchTags/FetchPS pattern
- Tests: httptest covering success, HTTP error, invalid JSON, trailing slash

**Task 3: Model resolution + backend selection** *(parallel with T1+T2, no deps on them)*
- Files: `internal/backend/resolve.go` (new), modify `registry.go` (add OllamaDefaults/Policy accessors)
- Types: `ResolutionKind` (ResolutionAlias/ResolutionNative), `ResolvedModel` (Kind, RequestedID, Candidates, AliasConfig)
- `Registry.Resolve(modelID string) (*ResolvedModel, error)` — alias exact match first, then native model check if exposed
- `FindFirstBackend(snapshots, modelName) (*BackendSnapshot, error)` — first enabled+healthy backend with model
- Add `OllamaDefaults()` and `Policy()` nil-safe accessors to Registry
- Tests: alias/native resolution, unknown model, FindFirstBackend skipping unhealthy/disabled

**Task 4: OpenAI chat handler** *(depends on Tasks 1, 2, 3)*
- Files: `internal/openai/chat.go`
- Types: chatCompletionRequest/Response, chatRequestMessage, chatChoice, responseMessage, chatUsage
- `ChatCompletionsHandler(logger, registry) http.Handler`
- Logic: decode → reject stream → resolve → find backend → buildOllamaRequest (defaults→alias→client overrides) → SendChat → map to OpenAI response
- `buildOllamaRequest(modelName, messages, defaults, alias, req, policy)` — merge options
- Tests: basic chat, native model, options injection, model not found (404), no backend (503), stream rejected (400)

**Task 5: Wire route** *(depends on Task 4)*
- Modify: `internal/httpapi/router.go` — add `POST /v1/chat/completions` route in registry non-nil block
- Test: route registered when registry non-nil, 404 when nil

Dependency order: T1 → T2 → T4 → T5; T3 parallel with T1+T2.

### Milestone 5: streaming chat (completed)

**Overview**: 7 tasks implementing streaming chat completions. Ollama JSON-lines stream → SSE writer, scheduler retry before first meaningful token, client disconnect → cancel backend.

**Task 1: Ollama streaming reader** *(no deps)*
- Files: `internal/ollama/stream.go` (new)
- `StreamChunk` (Response *ChatResponse, Err error), `SendChatStream(ctx, client, baseURL, req) (<-chan StreamChunk, error)`
- JSON-lines reader via bufio.Scanner, context cancellation, buffered channel (cap 10)

**Task 2: Job streaming fields** *(no deps, parallel with T1+T4)*
- Files: modify `internal/scheduler/job.go`
- Add `Streaming bool`, `StreamCh chan *ollama.StreamChunk`, `JobCtx context.Context` to Job struct

**Task 3: Scheduler streaming assignment runner** *(depends on T1, T2)*
- Files: modify `internal/scheduler/scheduler.go`
- Branch `runAssignment` on `job.Streaming` → `runStreamingAssignment`
- Retry loop: on failure before first chunk, find alternative backend, retry up to MaxAttempts

**Task 4: SSE types and writer** *(no deps, parallel with T1+T2)*
- Files: `internal/openai/sse_chat.go` (new)
- Types: sseChatChunk, sseChatChoice, sseChatDelta, sseUsage; `writeSSEChatChunk`, `writeSSEDone`

**Task 5: Streaming handler path** *(depends on T3, T4)*
- Files: modify `internal/openai/chat.go`
- Remove stream rejection; branch to `handleStreamChatCompletion` when Stream=true
- Role delta only on first content chunk (ARCH §10.1)

**Task 6: Streaming tests** *(depends on T5)*
- Files: modify `internal/openai/chat_test.go`
- Tests: basic streaming, retry before first token, no retry after first token, client disconnect, model not found, queue full

**Task 7: Wiring** *(implicit — existing route handles both stream=false and stream=true)*

Dependency order: T1+T2+T4 (parallel) → T3 → T5 → T6.

### Milestone 4: scheduler (completed)

### Milestone 6: Responses API

Implement:
- `/v1/responses` non-streaming + streaming
- String input → messages conversion
- Array input → messages conversion
- Instructions → system message mapping
- Base64 inline file handling (spool to disk)

### Milestone 7: Anthropic Messages

Implement:
- `/v1/messages` non-streaming + streaming
- Anthropic message shape → Ollama translation
- `/v1/messages/count_tokens` (bytes/4 approximation)
- Thinking override rule

### Milestone 8: documents

Implement:
- `/proxy/documents/process` multipart handler
- Document preparation queue
- PDF text extraction (poppler-utils)
- PDF page rendering
- Optional OCR (tesseract)
- Low-priority chunk inference jobs
- Result combination

### Milestone 9: image generation

Implement:
- `/v1/images/generations`
- Image backend pool (openai_compatible type)
- Host-aware scheduling for image backends

### Milestone 10: observability

Implement:
- `/metrics` Prometheus endpoint
- `/debug` static HTML dashboard
- Debug JSON endpoints: `/debug/queue`, `/debug/scheduler`, `/debug/hosts`, `/debug/config`
- Embedded static files (index.html, app.js, style.css)

---

---
## 2026-05-21 — Milestone 1: skeleton completed

Implemented the full skeleton: 5 packages, 10 source files, all tests passing.

**Task 1: config** — `internal/config/config.go`, `defaults.go`, `load.go`. Full Config struct hierarchy (15 types) matching ARCH §5.1 JSON schema. `LoadConfig` merges JSON onto `DefaultConfig()`. Sentinels: missing file = defaults, empty file = errEmptyConfig, parse error = zero Config. 56 table-driven assertions + 5 load tests — all pass.

**Task 2: logging** — `internal/logging/logging.go`. Structured JSON logger, 4 levels (debug/info/warn/error), stdout for debug+info, stderr for warn+error. Field constructors: String, Int, Bool, Duration, Any. Level threshold filtering. 12 tests — all pass.

**Task 3: storage** — `internal/storage/paths.go`. `Paths` struct from `PROXY_STORAGE_DIR`, `NewPaths()` computes derived paths, `EnsureDirs()` creates subdirectories. Fixed test assertion from exact error match to `strings.Contains`. 6 tests — all pass.

**Task 4: httpapi** — `internal/httpapi/router.go`, `middleware.go`, `health.go`. `RequestIDMiddleware` (crypto/rand UUID, X-Request-Id header), `LoggingMiddleware` (method/path/status/duration/request_id), `HealthHandler` (200 + `{"status":"ok"}`), `ReadyHandler` (200 + `{"status":"ready"}`). Go 1.22 method patterns. 5 tests — all pass.

**Task 5: main.go** — `cmd/proxy/main.go`. `run() error` pattern. Env vars (PROXY_LISTEN, PROXY_STORAGE_DIR) with defaults. Startup: storage → config → logger → router → http.Server. Graceful shutdown on SIGINT/SIGTERM. Integration test: starts server, hits /healthz and /readyz, triggers shutdown. All pass.

**Global verification:**
- `go vet ./...` — clean
- `go test ./... -count=1` — 5/5 packages pass
- `go build -o bin/proxy ./cmd/proxy/` — succeeds
- Docker build + compose up — `/healthz` returns `{"status":"ok"}` with `X-Request-Id` header

**Design decisions:**
- `AliasOverrides` uses `*bool`/`*OllamaOptions` pointers with omitempty to distinguish nil from zero
- `LoggingMiddleware` reads request ID from response header (set by inner RequestIDMiddleware) since it's outermost
- Dockerfile: `COPY go.mod ./` only (no go.sum needed — stdlib only)
- No external dependencies, stdlib only

---

## 2026-05-22 — Milestone 2: backend discovery completed

Implemented Ollama backend registry, model discovery polling, debug endpoints, and /v1/models. 8 packages, all tests pass.

**Task 1: Ollama types + client** — `internal/ollama/types.go`, `client.go`. TagsResponse, PSResponse, ModelDetail types matching Ollama API. `NewHTTPClient()` factory (30s timeout). 6 tests.

**Task 2: FetchTags + FetchPS** — `internal/ollama/tags.go`, `ps.go`. `FetchTags(client, baseURL)` calls GET /api/tags, `FetchPS` for /api/ps. Handles trailing slashes, non-200, parse errors. httptest-based tests. 8 new tests (14 total).

**Task 3: Backend registry** — `internal/backend/health.go`, `model_state.go`, `registry.go`. `HealthState` with SetHealthy/SetUnhealthy/Snapshot. `ModelNamesFromTags/PS` helpers. `Registry` struct: `NewRegistry(cfg, logger)`, `Start` (goroutine per backend: tags/60s, ps/5s with immediate initial poll), `Stop` (context cancel + WaitGroup), `BackendSnapshots()` and `ModelsConfig()` both nil-safe. Snapshot pattern: write under lock, return value copy. 8 tests.

**Task 4: Debug handlers** — `internal/httpapi/backends.go`, `models.go`. `DebugBackendsHandler` returns `{"data":[BackendSnapshot...]}`. `DebugModelsHandler` returns aliases + native_models + loaded_models. Both nil-safe (return empty arrays on nil registry). 7 new tests.

**Task 5: /v1/models OpenAI endpoint** — `internal/openai/models.go`. `ModelsHandler` returns `{"object":"list","data":[...]}`. Aliases: created=0, owned_by="proxy". Native models: created=LastContact.Unix(), owned_by="ollama". Dedup: alias names win. Ordering: aliases first (config order), natives sorted alphabetically. Nil-safe. 7 tests.

**Task 6: Wiring** — Modified `router.go` (NewRouter accepts *backend.Registry, conditionally registers routes when non-nil), `main.go` (creates/starts/stops registry, passes to router), `main_test.go` and `router_test.go` (pass nil registry). No regressions.

**Global verification:**
- `go vet ./...` — clean (8 packages)
- `go test ./... -count=1` — 8/8 packages pass
- `go build -o bin/proxy ./cmd/proxy/` — succeeds

**Design decisions:**
- Thread safety via snapshot pattern: poll goroutines write under lock, HTTP handlers request value copies under RLock
- NewRouter signature changed from one parameter to two; registry is nullable for graceful degradation
- Poll intervals: /api/tags every 60s, /api/ps every 5s per ARCH §7.1
- Immediate initial polls on Start (not after first ticker tick)
- Ollama client is deliberately thin: just factory + standalone fetch functions; registry owns the client

---

## 2026-05-22 — Milestone 3: non-streaming chat completed

Implemented model resolution, Ollama /api/chat client, /v1/chat/completions endpoint with options merging.

**Task 1: Chat types** — Added ChatRequest, ChatMessage, ChatOptions, ChatResponse to `internal/ollama/types.go`. Options uses `*ChatOptions` pointer for proper omitempty. 5 new tests (19 total).

**Task 2: SendChat** — `internal/ollama/chat.go`. POSTs JSON to `{baseURL}/api/chat`, follows FetchTags pattern. 4 httptest tests (23 total).

**Task 3: Model resolution** — `internal/backend/resolve.go`. `Registry.Resolve(modelID)` — alias exact match → native model discovery on backends → error. `FindFirstBackend(snapshots, modelName)` — first enabled+healthy backend. Added `OllamaDefaults()` and `Policy()` nil-safe accessors. 14 new tests (22 total).

**Task 4: OpenAI chat handler** — `internal/openai/chat.go`. `ChatCompletionsHandler(logger, registry)`. Full pipeline: decode → reject streaming → resolve → find backend → buildOllamaRequest (defaults→alias→client merge per ARCH §8) → SendChat → map to OpenAI response. Error shapes match OpenAI format. 15 new tests (22 total).

**Task 5: Wire route** — Added `POST /v1/chat/completions` to router's registry-non-nil block.

**Global verification:** 8 packages pass, go vet clean, go build succeeds.

**Design decisions:**
- Options merging order: defaults → alias overrides (non-zero fields only) → client overrides (when allowed)
- ChatResponse uses `*ChatOptions` to distinguish nil from zero-value struct for omitempty
- Pre-scheduler backend selection: simple first-match iteration over snapshots
- Stream=true returns 400 until M5 implements streaming

---

## 2026-05-22 — Milestone 4: scheduler completed

Implemented full priority-based job scheduler replacing the inline resolve→send pattern from M3. 9 packages, all tests pass.

**Task 1: Job types + priority queue** — `internal/scheduler/job.go`, `queue.go`. JobKind enum (Chat=100, Tool/Vision=90, Image=60, Audio=50, Document=30), JobState (Pending/Running/Completed/Failed), Job struct with ResultChan. Queue: mutex-protected max-heap keyed by effective priority (base + age * aging_per_second). Enqueue/Dequeue/TopN/Remove/PendingCount. 15 tests.

**Task 2: EWMA stats tracker** — `internal/scheduler/stats.go`. BackendModelStats per backend+model (TPS, cold load time, consecutive failures). StatsTracker with RecordSuccess/RecordFailure, EWMA alpha=0.2. GetTokensPerSecond/GetColdLoadTime with fallbacks. 9 tests.

**Task 3: Lease management** — `internal/scheduler/leases.go`. HostLeaseManager and BackendLeaseManager with Acquire/Release/FreeCapacity. Thread-safe via mutex, unknown host/backend defaults to capacity=1. 13 tests.

**Task 4: Assignment scoring** — `internal/scheduler/scoring.go`. Assignment/Scorer types. ValidAssignments (filters by enabled, healthy, model available, host capacity, backend capacity). Score computes full ARCH §9.6 cost formula (backend_wait + host_load + model_switch + estimated_generation_time + disruption + substitution + failure_penalty - priority_credit - aging_credit). BestAssignment returns lowest-cost valid assignment. 14 tests.

**Task 5: Scheduler dispatch loop** — `internal/scheduler/scheduler.go`. Scheduler struct holding Queue, Scorer, Stats, Client, BackendURLs. NewScheduler constructor. Submit (enqueue + non-blocking wakeup). Start/Stop (context cancel + WaitGroup). dispatchLoop (wakeup-driven event loop). dispatch (TopN → BestAssignment → acquire leases → mark running → go runAssignment). runAssignment (build ChatRequest → SendChat → record stats → result on ResultChan, leases released in defer). 8 tests.

**Task 6: Chat handler integration** — Modified `internal/openai/chat.go`. ChatCompletionsHandler signature changed to accept `*scheduler.Scheduler`. Replaced inline resolve→find backend→send with: resolve → build options → create Job → Submit → await ResultChan → map response. Updated tests to create real scheduler with fake Ollama backend.

**Task 7: Wiring** — Modified `internal/httpapi/router.go` (NewRouter accepts `sched` param, chat route only when both registry and sched non-nil). Modified `cmd/proxy/main.go` (creates HostLeaseManager, BackendLeaseManager, StatsTracker, Scorer, Scheduler; Start/Stop; passes to NewRouter). Updated test files to pass nil scheduler.

**Design decisions:**
- Wakeup channel buffered cap 1 with non-blocking send — prevents dispatch stalls
- Leases released in defer (even on panic) — prevents capacity leaks
- Chat handler passes nil scheduler for tests that fail before dispatch (model not found, stream rejected)
- Chat route nil-safe: only registered when both registry and sched non-nil
- TPS computed from ChatResponse.EvalCount / TotalDuration seconds; cold load from LoadDuration nanoseconds
- Disruption cost estimated via DisruptionFactor * cold_load_time when model not loaded

**Global verification:**
- `go vet ./...` — clean (9 packages)
- `go test ./... -count=1` — 9/9 packages pass
- `go build -o bin/proxy ./cmd/proxy/` — succeeds

---

## 2026-05-22 — Milestone 5: streaming chat completed

Implemented streaming chat completions with Ollama JSON-lines stream reader, SSE writer, scheduler retry before first meaningful token, and client disconnect cancellation. 9 packages, all tests pass.

**Task 1: Ollama stream reader** — `internal/ollama/stream.go`. `StreamChunk` (Response *ChatResponse, Err error). `SendChatStream(ctx, client, baseURL, req) (<-chan StreamChunk, error)` — JSON-lines reader via bufio.Scanner (1MB max token), context cancellation, buffered channel (cap 10). Forces `stream: true`. 6 tests.

**Task 2: Job streaming fields** — Modified `internal/scheduler/job.go`. Added `Streaming bool`, `StreamCh chan *ollama.StreamChunk`, `JobCtx context.Context` to Job struct.

**Task 3: Scheduler streaming assignment runner** — Modified `internal/scheduler/scheduler.go`. `runAssignment` branches to `runStreamingAssignment` when `job.Streaming`. Retry loop: on failure before first chunk, finds alternative backend via `findStreamingBackend` (excludes failed backend IDs), retries up to `MaxAttempts`. First chunk held before forwarding to `StreamCh` (ARCH §10.1: retry before first meaningful token). Leases held for full streaming duration. 56 tests pass.

**Task 4: SSE types and writer** — `internal/openai/sse_chat.go`. Types: sseChatChunk, sseChatChoice, sseChatDelta, sseUsage. `writeSSEChatChunk(w, id, model, created, role, content, finishReason, usage)` and `writeSSEDone(w)` — fmt.Fprintf + Flush.

**Task 5: Streaming handler path** — Modified `internal/openai/chat.go`. Removed stream rejection. Added `handleStreamChatCompletion`: SSE headers → streaming Job with StreamCh → Submit → goroutine for client disconnect cancellation → read StreamCh → write SSE events. Role delta only on first content chunk. [DONE] marker at end.

**Task 6: Streaming tests** — Modified `internal/openai/chat_test.go`. 6 new tests: RetryBeforeFirstToken (server1 500, server2 succeeds), RetryExhausted (both 500 → stream closes), ClientDisconnect (context cancellation verified via atomic), ModelNotFound (404 before SSE), QueueFull (SSE error event), NonStreamingStillWorks (regression).

**Design decisions:**
- Streaming jobs go through scheduler queue (not bypass) — fair queuing, priority aging, lease management
- First chunk held in `runStreamingAssignment` before `StreamCh` forward — enables retry before first meaningful output per ARCH §10.1
- After first chunk forwarded: no more retries
- `StreamCh` channel (cap 20) bridges scheduler goroutine → handler goroutine
- `JobCtx` derived from request context; client disconnect cancels it, which cancels the Ollama HTTP request
- `findStreamingBackend` temporarily sets job state to Pending because `ValidAssignments` only considers pending jobs
- SSE headers sent before scheduler Submit; queue full is reported as SSE error event (not HTTP error, since headers already sent)

**Global verification:**
- `go vet ./...` — clean (9 packages)
- `go test ./... -count=1` — 9/9 packages pass
- `go build -o bin/proxy ./cmd/proxy/` — succeeds

---

## Open Questions

(none yet)

## Resolved Questions

- **Authority document split**: AGENTS.md = agent rules + git policy + constitution. ARCHITECTURE.md = implementation source of truth. DESIGN.md = product behavior + debug UI. Each carries its own Boundaries section with cross-references.

## Recent Decisions

- 2026-05-21: Adopted ARCHITECTURE.md/DESIGN.md split from image-server's single README.md architecture pattern. README.md stays as simple repo front door.
- 2026-05-21: Implementation follows ARCHITECTURE.md §25 milestone order — skeleton first, each milestone builds on the previous.

---

## Live Stream
