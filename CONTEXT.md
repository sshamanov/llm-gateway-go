# Project Context

Last updated: 2026-05-22

---

## Active Tasks

### Milestone 1: skeleton (completed)

### Milestone 2: backend discovery (in progress)

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

### Milestone 3: non-streaming chat

Implement:
- Model resolution (alias → candidates, native → exact)
- Alias override application (think, keep_alive, options)
- Ollama `/api/chat` client call
- `/v1/chat/completions` endpoint
- OpenAI-compatible response shaping

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

## Open Questions

(none yet)

## Resolved Questions

- **Authority document split**: AGENTS.md = agent rules + git policy + constitution. ARCHITECTURE.md = implementation source of truth. DESIGN.md = product behavior + debug UI. Each carries its own Boundaries section with cross-references.

## Recent Decisions

- 2026-05-21: Adopted ARCHITECTURE.md/DESIGN.md split from image-server's single README.md architecture pattern. README.md stays as simple repo front door.
- 2026-05-21: Implementation follows ARCHITECTURE.md §25 milestone order — skeleton first, each milestone builds on the previous.

---

## Live Stream

Launched M2 Plan subagent to create task breakdown for backend discovery.
M2 plan applied: 6 tasks, T1→T2→T3→T4+T5(parallel)→T6.
Launching M2 T1: Ollama API types + HTTP client.
T1 complete: 6 tests pass, types.go + client.go + tests. Launching T2: FetchTags + FetchPS.
T2 complete: 14 tests pass (8 new). tags.go, ps.go with httptest-based tests. Launching T3: backend registry.
T3 complete: 8 tests pass. health.go, model_state.go, registry.go with poll goroutines + httptest. Launching T4+T5 in parallel.
T4 complete: 7 new tests pass. DebugBackendsHandler + DebugModelsHandler with nil-safe semantics.
T5 complete: 7 new tests pass. OpenAI /v1/models endpoint: aliases, dedup, native exposure control.
Launching T6: wiring registry into router.go and main.go.
T3 complete: 8 tests pass, 4 files (health.go, model_state.go, registry.go, registry_test.go), go vet + go test clean. Backend registry with polling, health tracking, nil-safe API.
T5 complete: internal/openai/models.go + models_test.go, ModelsHandler serves OpenAI /v1/models with aliases (created=0, owned_by=proxy) and optional native Ollama models (created=LastContact, owned_by=ollama), dedup by alias name, aliases first then sorted native models, nil-safe. 7 tests pass, go vet clean.
