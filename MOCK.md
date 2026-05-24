# MOCK.md — End-to-End Mock Testing System

## Boundaries

**This document owns:**
- Architecture of the mock testing system (FakeOllama, Harness, Scenarios)
- Component design, lifecycle, and configuration
- Scenario catalog with coverage mapping
- Test binary structure and execution model
- Verification strategy (response content, metrics, debug endpoints)

**This document does NOT contain:**
- Production code architecture → **[ARCHITECTURE.md](ARCHITECTURE.md)**
- API reference or product behavior → **[DESIGN.md](DESIGN.md)**
- Active tasks, decision history, live stream → **[CONTEXT.md](CONTEXT.md)**
- Agent rules, git policy → **[AGENTS.md](AGENTS.md)**

---

## 1. Overview

The proxy is a standalone Go binary. To test it end-to-end without a real Ollama cluster, we build a **mock test binary** (`cmd/mocktest`) that runs the full proxy stack against fake Ollama backends using Go's `httptest.Server`.

**Core principle**: The proxy never knows the backends are fake. `httptest` gives us a real TCP listener, real HTTP, real JSON. The production `http.DefaultClient` talks to it identically to how it talks to a physical Ollama node.

```
┌──────────────────────────────────────────────────────────────┐
│  mocktest binary                                             │
│                                                              │
│  ┌──────────┐   ┌──────────┐   ┌────────────┐              │
│  │FakeOllama│   │FakeOllama│   │FakeOllama  │              │
│  │ :54321   │   │ :54322   │   │ (image)    │              │
│  └────┬─────┘   └────┬─────┘   └─────┬──────┘              │
│       │              │               │                       │
│       └──────────────┼───────────────┘                      │
│                      │                                      │
│              ┌───────▼───────┐                               │
│              │    Proxy      │  in-process, real goroutines  │
│              │  :4000        │  scheduler, handlers, metrics │
│              └───────┬───────┘                               │
│                      │                                      │
│       ┌──────────────┼──────────────┐                       │
│       │              │              │                        │
│  ┌────▼────┐   ┌─────▼────┐  ┌─────▼─────┐                 │
│  │scenario │   │scenario  │  │scenario   │                  │
│  │chat     │   │retry     │  │capacity   │  ...             │
│  └─────────┘   └──────────┘  └───────────┘                 │
│                                                              │
│  Harness: temp dirs, config gen, lifecycle, assertions       │
└──────────────────────────────────────────────────────────────┘
```

No interfaces. No dependency injection. No adapter pattern. The "swap" is the URL string in `BackendURLs`.

---

## 2. Components

### 2.1 FakeOllama

Wraps `httptest.Server`. Each instance is a fully independent fake Ollama node.

**File**: `internal/mock/ollama.go`

```go
type FakeOllama struct {
    Server *httptest.Server
    URL    string  // "http://127.0.0.1:54321"
    ID     string  // for logging and debug
}
```

**Constructor**:

```go
func NewFakeOllama(cfg FakeOllamaConfig) *FakeOllama
```

**Configuration** (`FakeOllamaConfig`):

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ID` | `string` | `"default"` | Backend identifier |
| `Models` | `[]string` | `["llama3"]` | Models returned by `/api/tags` |
| `LoadedModels` | `[]string` | same as Models | Models returned by `/api/ps` (loaded=warm) |
| `ChatLatency` | `time.Duration` | `0` | Artificial delay per chat response |
| `StreamChunkLatency` | `time.Duration` | `5ms` | Delay between streaming chunks |
| `FailureRate` | `float64` | `0.0` | Probability (0..1) of returning 500 |
| `FailCount` | `int` | `0` | Deterministic: fail first N requests, then succeed (per-backend request counter) |
| `FirstChunkGarbage` | `bool` | `false` | First streaming line is invalid JSON (triggers decode-error retry) |
| `MidStreamFailAfter` | `int` | `0` | Send N good chunks then garbage (0 = disabled) |
| `ChatResponseContent` | `string` | `"Hello from FakeOllama"` | Content in chat response |
| `LoadDuration` | `int64` | `0` | Fake model load time in nanoseconds (used for cold-load scoring) |
| `TotalDuration` | `int64` | `100ms` | Fake total inference time in nanoseconds (used for TPS scoring) |
| `EvalCount` | `int` | `5` | Fake eval token count (used for TPS calculation) |

**Endpoints served**:

| Path | Method | Response |
|------|--------|----------|
| `/api/tags` | GET | `{"models": [{"name":"llama3"}, ...]}` |
| `/api/ps` | GET | `{"models": [{"name":"llama3","model":"llama3"}, ...]}` |
| `/api/chat` | POST | Non-streaming: one `ChatResponse` JSON. Streaming: newline-delimited `ChatResponse` lines (or garbage per config) |

**Garbage injection**: Invalid JSON on the first streaming line triggers a decode error in `SendChatStream`, which the scheduler interprets as "backend failed before first token" and triggers a retry. Garbage mid-stream (after N good chunks) simulates a connection drop after output has reached the client — the scheduler must NOT retry.

---

### 2.2 FakeImageBackend

Minimal fake image generation endpoint.

**File**: `internal/mock/image.go`

```go
type FakeImageBackend struct {
    Server *httptest.Server
    URL    string
}
```

Serves a single endpoint that returns an OpenAI-compatible images response with a placeholder `b64_json` field.

---

### 2.3 Harness

Orchestrates the entire test environment.

**File**: `internal/mock/harness.go`

**Lifecycle**:

```
NewHarness()
  → StartFakeBackends()     // creates FakeOllama instances
  → WriteConfig()           // generates config.json pointing at fakes
  → StartProxy()            // starts proxy in-process (goroutine)
  → WaitReady()             // polls /healthz until 200

RunScenario(fn)
  → fn(h)                   // scenario makes HTTP calls to proxy

Verify()
  → GET /metrics            // parse Prometheus text
  → GET /debug/queue        // assert queue state
  → GET /debug/scheduler    // assert stats
  → GET /debug/hosts        // assert capacity
  → GET /debug/config       // assert config shape

Shutdown()
  → cancel context           // proxy shutdown
  → close fake servers
  → remove temp dir
```

**Fields**:

| Field | Type | Description |
|-------|------|-------------|
| `TempDir` | `string` | Temp directory for storage and config |
| `ProxyURL` | `string` | `"http://127.0.0.1:PORT"` (random port) |
| `Ollamas` | `[]*FakeOllama` | Running fake Ollama instances |
| `Images` | `[]*FakeImageBackend` | Running fake image backends |
| `Cancel` | `context.CancelFunc` | Cancels the proxy |
| `ErrCh` | `<-chan error` | Proxy errors |
| `Config` | `*config.Config` | Generated config |

---

### 2.4 Scenario

A scenario is a function signature:

```go
type Scenario func(h *Harness) error
```

Each scenario:
1. Makes HTTP requests to `h.ProxyURL`
2. Asserts on response status, JSON shape, content
3. Optionally checks `/metrics`, `/debug/*` endpoints
4. Returns `nil` on success, `error` describing the failure on failure

**Helper functions** provided by the harness:

```go
func (h *Harness) Post(path string, body any) (*http.Response, []byte, error)
func (h *Harness) Get(path string) (*http.Response, []byte, error)
func (h *Harness) GetStream(path string, body any) ([]string, error) // returns SSE data lines
func (h *Harness) AssertStatus(want int, resp *http.Response) error
func (h *Harness) AssertJSONField(path, field, want string, body []byte) error
func (h *Harness) AssertMetrics(metric string, labels map[string]string, want float64) error
func (h *Harness) AssertQueueDepth(want int) error
func (h *Harness) AssertHostCapacity(hostID string, wantActive, wantCapacity int) error
```

---

## 3. Scenario Catalog

### 3.1 `chat.go` — Basic chat

| # | Scenario | Description |
|---|----------|-------------|
| 1 | `HealthyCluster` | 2 backends, 2 models. Non-streaming chat. Verify response shape (id, choices, model). |
| 2 | `StreamingChat` | Streaming request, verify SSE chunks arrive with `data:` prefix, verify `[DONE]` sentinel. |

### 3.2 `retry.go` — Streaming and non-streaming retry

| # | Scenario | Description |
|---|----------|-------------|
| 3 | `StreamingRetryBeforeToken` | Backend 1 configured with `FirstChunkGarbage=true`. Backend 2 healthy. Verify request succeeds on backend 2. |
| 4 | `NoRetryAfterToken` | Backend 1 configured with `MidStreamFailAfter=1`. Verify job fails (no retry after first token reached client). |
| 5 | `NonStreamingRetrySuccess` | Backend 1 configured with `FailCount=1` (first request fails). Backend 2 healthy. Verify non-streaming request retries on b2 and succeeds. |
| 6 | `RetryExhausted` | Both backends configured with `FailureRate=1.0`. Verify non-streaming request fails with 502 after all retry attempts exhausted. |

### 3.3 `capacity.go` — Capacity and queuing

| # | Scenario | Description |
|---|----------|-------------|
| 7 | `HostCapacitySerialization` | Host max=1. Submit 3 non-streaming jobs simultaneously. Verify all 3 complete (2 queued, 1 runs at a time). |
| 8 | `QueueAging` | Submit low-priority document job, wait, submit high-priority chat job. Verify chat dequeues first despite submitting later. |
| 9 | `BackendDisable` | Backend fails N consecutive times. Verify backend-model removed from valid assignments. |
| 10 | `QueueFull` | Fill queue to QueueMaxPending. Verify 503 is returned. |

### 3.4 `alias.go` — Alias fallback

| # | Scenario | Description |
|---|----------|-------------|
| 11 | `AliasFallback` | Primary model not loaded (not in `/api/ps`), backup model loaded. Request via alias. Verify backup is used. |

### 3.5 `api.go` — Full API surface

| # | Scenario | Description |
|---|----------|-------------|
| 12 | `FullAPISurface` | One request each: /v1/chat/completions, /v1/responses, /v1/messages, /v1/messages/count_tokens, /v1/models. Verify each returns correct response shape. |
| 13 | `ImageGeneration` | With fake image backend. Verify response has `created` and `data` fields. |

### 3.6 `observe.go` — Observability

| # | Scenario | Description |
|---|----------|-------------|
| 14 | `MetricsEndpoint` | After running chat requests, GET /metrics. Verify key metrics exist. |
| 15 | `DebugEndpoints` | GET /debug/queue, /debug/scheduler, /debug/hosts, /debug/config. Verify JSON shape. |

### 3.7 `chaos.go` — Chaos and failure modes

| # | Scenario | Description |
|---|----------|-------------|
| 16 | `MultiBackendChaos` | 3 backends with mixed failure modes (random 30%, deterministic FailCount, clean), varying latencies. Send 10 concurrent non-streaming requests. All complete via retries. |
| 17 | `ColdModelPreference` | Two backends: one cold (high LoadDuration/TotalDuration, low TPS), one warm (fast). Send 5 requests — all complete, scheduler learns warm preference. |
| 18 | `ClientCancellation` | Backend with 5s ChatLatency. Client cancels after 100ms. Verify request does not return 200 (context cancellation propagates). |

### 3.8 `document.go` — Document processing

| # | Scenario | Description |
|---|----------|-------------|
| 19 | `DocumentProcessing` | Upload a minimal valid PDF via multipart form. Verify 200 or 422 (no extractable text). Verify response has `id`, `model`, `content` fields. |

---

## 4. Directory Layout

```
internal/mock/
  ollama.go           FakeOllama type, FakeOllamaConfig, NewFakeOllama
  image.go            FakeImageBackend (minimal)
  harness.go          Harness: lifecycle, helpers, assertions
  scenarios.go        Scenario type, registry, runner
  scenarios/
    chat.go           HealthyCluster, StreamingChat
    retry.go          StreamingRetryBeforeToken, NoRetryAfterToken, NonStreamingRetrySuccess, RetryExhausted
    capacity.go       HostCapacitySerialization, QueueAging, BackendDisable, QueueFull
    alias.go          AliasFallback
    api.go            FullAPISurface, ImageGeneration
    observe.go        MetricsEndpoint, DebugEndpoints
    chaos.go          MultiBackendChaos, ColdModelPreference, ClientCancellation
    document.go       DocumentProcessing

cmd/mocktest/
  main.go             Test binary: creates harness, registers scenarios, runs all
```

---

## 5. Execution Model

```bash
# Run all scenarios
go run ./cmd/mocktest

# Run specific scenarios
go run ./cmd/mocktest -scenarios chat,retry

# With verbose output
go run ./cmd/mocktest -v

# Exit code: 0 = all pass, 1 = at least one failure
```

The mocktest binary:
1. Creates harness
2. Registers all scenarios in dependency order
3. Runs each scenario sequentially (shared proxy instance)
4. Each scenario starts from a clean state (empty queue, zero stats)
5. Reports: `PASS scenario_name (0.42s)` or `FAIL scenario_name: reason`
6. Exits with summary

Shared proxy instance means scenarios can run faster (no startup cost per scenario), but each scenario's assertions must account for accumulated state (e.g., stats grow across scenarios).

---

## 6. Verification Strategy

Each scenario verifies at **three levels**:

| Level | How | Examples |
|-------|-----|----------|
| HTTP response | Status code, JSON body fields | `assertStatus(200)`, `assertJSONField("id", starts "chatcmpl-")` |
| Semantic correctness | Response content matches request | Response model = requested model, content is non-empty |
| System state | `/metrics`, `/debug/*` endpoints | Queue depth = N, host active_jobs = 0, backend_up = 1 |

For streaming scenarios, additionally verify:
- At least one SSE `data:` line arrived
- Final `data: [DONE]` sentinel
- Chunk JSON has `choices[0].delta` structure

---

## 7. Design Decisions

**Why in-process instead of subprocess?** Faster (no fork), single-binary (no separate build step), easier debugging (same process). The proxy runs in a goroutine with a real `http.Server` — indistinguishable from production networking.

**Why real time instead of simulated time?** The scheduler uses `time.Now()` and `time.Since()` internally. Mocking time would require changing the scheduler, which defeats the purpose of E2E testing. FakeOllama's `ChatLatency` knob controls response timing.

**Why scenarios share a proxy instance?** Startup cost is high (config load, health polling, backend discovery). Scenarios run faster when sharing. Accumulated state is a feature — it validates that metrics/stats accumulate correctly across requests.

**Why `httptest.Server` instead of raw `net.Listener`?** `httptest` handles cleanup (`Close()`), provides a ready-to-use `*http.Client` via `Server.Client()`, and is battle-tested. One less thing to maintain.

**Why no DI/interfaces?** Go's `httptest.Server` makes them unnecessary. The "injection point" is the URL string. The production code uses `http.DefaultClient` to talk to HTTP servers — fake or real, it's the same code path.
