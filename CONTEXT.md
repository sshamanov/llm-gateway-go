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

### Milestone 3: non-streaming chat (in progress)

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

### Milestone 4: scheduler

Implement:
- Global job queue with priorities
- Host/backend lease management
- EWMA stats tracking per backend+model
- Assignment scoring (cost formula from ARCHITECTURE.md §9.6)
- Dispatch loop (event-driven, non-preemptive)
- Scheduler metrics

### Milestone 5: streaming chat

Implement:
- Ollama stream reader (JSON lines → channel)
- OpenAI Chat Completions SSE writer
- Retry before first meaningful token
- No retry after first token
- Client disconnect → cancel backend

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

## Open Questions

(none yet)

## Resolved Questions

- **Authority document split**: AGENTS.md = agent rules + git policy + constitution. ARCHITECTURE.md = implementation source of truth. DESIGN.md = product behavior + debug UI. Each carries its own Boundaries section with cross-references.

## Recent Decisions

- 2026-05-21: Adopted ARCHITECTURE.md/DESIGN.md split from image-server's single README.md architecture pattern. README.md stays as simple repo front door.
- 2026-05-21: Implementation follows ARCHITECTURE.md §25 milestone order — skeleton first, each milestone builds on the previous.

---

## Live Stream

Launching M3 Plan subagent for non-streaming chat task breakdown.
M3 plan applied: 5 tasks. Launching T1 (chat types) + T3 (resolution) in parallel.
T1 complete: 19 tests pass, ChatRequest/ChatResponse types added.
T3 complete: 22 tests pass, Resolve + FindFirstBackend + OllamaDefaults/Policy accessors.
Launching T2: SendChat function.
T2 complete: 23 tests pass. SendChat POSTs to /api/chat.
Launching T4: OpenAI chat handler (main M3 task).
T4 complete: 22 tests pass (15 new). ChatCompletionsHandler with buildOllamaRequest merging.
Launching T5: wire /v1/chat/completions into router.
