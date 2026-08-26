# DESIGN.md

# Ollama Native Proxy — Product and Debug UI Design

---

## Boundaries

**This document owns:**
- Product summary and capabilities
- User personas, goals, and journeys
- API endpoint behavior — what each endpoint accepts and returns, status codes, error shapes
- Debug UI specification — layout, panels, polling behavior, what is shown and redacted
- UX principles: compatibility first, clean responses, read-only observability, disk-backed safety
- Interaction flows: debug UI polling, error presentation, model name visibility
- Edge cases and error states: no backend, backend disabled, queue full, disk full, client disconnect
- Accessibility and security from the user's perspective
- Explicit out-of-scope UX list

**This document does NOT contain:**
- Code structure, package layout, scheduler algorithm, implementation details → **[ARCHITECTURE.md](ARCHITECTURE.md)**
- Agent rules, git policy, build commands, project constitution → **[AGENTS.md](AGENTS.md)**
- Active tasks, open questions, decision history, live action stream → **[CONTEXT.md](CONTEXT.md)**
- Quick start, env reference for operators → **[README.md](README.md)**

**Rewrite policy:** On API/UX/behavior changes. Keep in sync with ARCHITECTURE.md — endpoint behavior here, translation logic there.

---

## 1. Product Summary

Ollama Native Proxy is a Docker-first local gateway that exposes practical OpenAI-compatible and Anthropic-compatible APIs while routing work to local or LAN backends.

The product is intended to sit between tools such as LiteLLM, Open WebUI, OpenClaw, Codex CLI, Claude Code, OpenAI SDK clients, and local inference services.

Primary capabilities:

- OpenAI-compatible chat API
- OpenAI Responses API compatibility
- Anthropic Messages API compatibility
- Ollama-native chat and vision routing
- external image generation routing
- optional audio routing when a backend exists
- document ingestion and processing
- learned host/backend/model-aware scheduling
- Prometheus metrics
- simple read-only debug UI

The proxy is not a general agent runtime. It does not execute tools itself in v1. It translates and forwards tool schemas, tool calls, and tool results where supported.

---

## 2. Users and Roles

## 2.1 Primary user: local AI power user / developer

Goals:

- Run local Ollama models behind OpenAI/Anthropic-compatible clients.
- Use LiteLLM, Open WebUI, OpenClaw, Codex CLI, Claude Code, and OpenAI SDK clients without custom client plugins.
- Force Ollama-native runtime options such as `think`, `num_ctx`, `num_thread`, `num_predict`, `keep_alive`, and sampling options.
- Route requests intelligently across multiple Ollama instances.
- Avoid unnecessary model switching.
- Keep RAM usage low by spooling files and documents to disk.
- Observe backend health, queue state, loaded models, and learned scheduler stats.

## 2.2 Secondary user: coding agent

Goals:

- Use the proxy as a stable local OpenAI-compatible or Anthropic-compatible provider.
- Receive clean API-compatible responses.
- Avoid caring which backend, model, or host is selected internally.
- Stream responses safely.

## 2.3 Secondary user: observability/debug operator

Goals:

- Open `/debug` and quickly see what the scheduler is doing.
- Confirm which models are loaded.
- Confirm which backends are disabled or cooling down.
- Check whether document jobs are blocking chat jobs.
- Check learned speed and latency stats per backend/model.

---

## 3. User Journeys

## 3.1 Configure proxy

1. User creates a Docker volume or bind mount at `/data`.
2. User writes config at `/data/config/config.json`.
3. User starts the proxy container with:
   - `PROXY_LISTEN=0.0.0.0:4000`
   - `PROXY_STORAGE_DIR=/data`
4. Proxy creates missing subdirectories:
   - `/data/tmp`
   - `/data/cache`
   - `/data/uploads`
   - `/data/documents`
   - `/data/images`
   - `/data/audio`
5. Proxy discovers Ollama backends and models.
6. User points LiteLLM, Open WebUI, OpenClaw, Codex CLI, Claude Code, or an SDK client at the proxy.

## 3.2 Use OpenAI-compatible chat

1. Client sends `POST /v1/chat/completions`.
2. User-selected model may be:
   - configured alias, for example `qwen-thinking`
   - native Ollama model name, for example `qwen:30b`
3. Proxy resolves model.
4. Scheduler chooses backend/model assignment.
5. Proxy calls Ollama native `/api/chat`.
6. Client receives OpenAI-compatible response or stream.

## 3.3 Use OpenAI Responses API

1. Client sends `POST /v1/responses`.
2. Input may be simple text, array input, instructions, image input, or base64 inline file input.
3. Proxy converts input into Ollama-compatible chat messages and/or document jobs.
4. Proxy returns OpenAI Responses-compatible output.

## 3.4 Use Anthropic Messages API

1. Client sends `POST /v1/messages`.
2. Proxy accepts Anthropic headers and message shape.
3. Proxy maps messages/system/tool declarations to Ollama-native request.
4. Proxy returns Anthropic-compatible non-streaming or streaming response.

## 3.5 Process document through proxy-native endpoint

1. User/client sends multipart request to `POST /proxy/documents/process`.
2. Proxy spools upload to disk.
3. Document preparation queue extracts text, renders pages, or optionally OCRs.
4. Prepared chunks enter inference scheduler as low-priority jobs.
5. Proxy combines chunk outputs.
6. Client receives one final document-processing response.

## 3.6 Observe runtime state

1. User opens `/debug`.
2. Page loads static HTML/CSS/JS.
3. JavaScript polls:
   - `/debug/backends`
   - `/debug/queue`
   - `/debug/scheduler`
   - `/debug/models`
   - `/debug/hosts`
   - `/debug/config`
4. User sees backend health, loaded models, queue depth, host load, learned stats, cooldowns, and recent errors.

---

## 4. UX Principles

## 4.1 Compatibility first

Clients should configure the proxy as a normal OpenAI-compatible or Anthropic-compatible provider whenever possible.

Do not require custom Open WebUI/OpenClaw plugins for core chat, vision, image generation, or Responses-style document input.

## 4.2 Clean client responses

Unsupported harmless fields should be ignored and logged, not surfaced to clients as noisy warnings.

Clients should receive clean OpenAI-compatible or Anthropic-compatible responses.

## 4.3 Observability without control risk

The debug UI is read-only in v1.

No buttons that mutate runtime state.

No backend disable/enable controls.

No queue delete/retry controls.

No config editing.

This avoids needing an admin authentication model in v1.

## 4.4 Disk-backed safety

Large payloads should not be kept in memory.

The user should not need to understand internal spool paths, but debug UI may show aggregate temp storage and document job status.

## 4.5 Scheduling should be explainable

The debug UI should make it understandable why a backend/model was chosen:

- model already loaded
- backend learned speed
- host capacity
- queue state
- alias fallback substitution
- backend/model cooldown

---

## 5. Screen Map

The only UI screen in v1 is:

```text
/debug
```

It is a static read-only dashboard.

Supporting JSON endpoints:

```text
/debug/backends
/debug/queue
/debug/scheduler
/debug/models
/debug/hosts
/debug/config
```

Metrics endpoint:

```text
/metrics
```

Prometheus endpoint is machine-facing, not user-facing.

---

## 6. Debug UI Layout

## 6.1 Header

Show:

- service name
- proxy uptime
- config path
- storage path
- current time
- overall status:
  - healthy
  - degraded
  - no backend available

## 6.2 Host panel

Show one row per host:

| Field | Meaning |
|---|---|
| Host ID | logical host ID from config or derived hostname |
| Active jobs | current active jobs |
| Capacity | `max_active_jobs` |
| Queued jobs targeting host | if available |
| Status | available/full/degraded/down/disabled |

Host status reflects backend health, not just job load:

- `available` — at least one backend up, capacity free
- `full` — at least one backend up, host at capacity
- `degraded` — some backends up, at least one down
- `down` — backends exist but none of the enabled ones is healthy
- `disabled` — all configured backends on the host are disabled

A host with no tracked backends falls back to capacity-only status.

Example:

```text
desktop-gpu  1/1 active  full
mini-pc      0/1 active  available
worker-x     0/1 active  down
```

## 6.3 Backend panel

Show one row per backend:

| Field | Meaning |
|---|---|
| Backend ID | configured backend ID |
| Type | Ollama/image/audio |
| URL | backend URL |
| Host | associated host |
| Health | healthy/degraded/disabled/recovering |
| Active jobs | current active jobs |
| Loaded models | from `/api/ps` where applicable |
| Last error | short redacted error |
| Cooldown | remaining cooldown if disabled |

## 6.4 Models panel

Show:

- exposed aliases
- native Ollama models
- alias primary model
- alias backup models
- alias `extends` parent (when inherited)
- backend availability
- disabled backend/model pairs

A model deleted from Ollama disappears from the panel and from `GET /v1/models`
within ~60s while its backend is reachable. If the backend is unreachable at
removal time, its native models linger at most ~5 minutes (staleness window),
then drop from the list and from the learned per-model scheduler stats. No extra
backend polling is performed for this.

Example:

```text
qwen-thinking
  primary: qwen:30b
  backups: qwen:14b, qwen:72b
  overrides: think=true, temperature=0.2

qwen:30b
  available on: ollama-main, ollama-fallback
```

## 6.5 Queue panel

Show:

- total pending jobs
- pending by kind:
  - chat
  - vision
  - document
  - image_generation
  - audio
- oldest wait time
- currently running jobs
- low-priority document chunk count

Do not show prompt contents.

## 6.6 Scheduler panel

Show:

- scheduler strategy
- top-N lookahead
- recent assignments
- recent alias substitutions
- recent model switches
- recent retries before first token
- failures after first token

Example assignment row:

```text
chat qwen-balanced → ollama-a / qwen:14b
reason: qwen:14b loaded, qwen:30b cold, substitution cost lower than reload cost
```

## 6.7 Learned stats panel

Show per backend/model:

| Field | Meaning |
|---|---|
| Backend | backend ID |
| Model | model name |
| Samples | number of completed requests |
| TPS | learned tokens/sec |
| TTFT | learned time to first token |
| Cold load | learned cold load time |
| Failures | recent failures |
| Disabled | yes/no |

## 6.8 Error panel

Show recent errors only.

Redact:
- authorization headers
- API keys
- cookies
- prompts
- full document text
- base64 content

---

## 7. Interaction Flows

## 7.1 Debug UI polling

The UI should poll every 2-5 seconds.

Recommended:

```text
/debug/backends   every 2s
/debug/queue      every 2s
/debug/scheduler  every 3s
/debug/models     every 10s
/debug/hosts      every 2s
/debug/config     once on load or every 30s
```

If an endpoint fails, show stale data with a visible error indicator.

## 7.2 Error presentation

Use simple status labels:

- healthy
- degraded
- disabled
- recovering
- full
- cooling down
- unknown

Do not show raw stack traces in UI.

---

## 8. User-Facing API Behavior

## 8.1 Unsupported fields

For harmless unsupported request fields:

- ignore
- log if debug or unsupported-field logging is enabled
- do not add warning headers
- do not fail the request

For impossible or unsafe features:

- return clean compatible error
- examples:
  - malformed required fields
  - requested model not found
  - no backend available
  - no image backend available for image endpoint
  - file input shape cannot be parsed

## 8.2 Model names shown to clients

Clients see:

- configured aliases
- native Ollama names if enabled

Clients do not see backend-specific debug model names such as:

```text
qwen:30b@backend1
```

That pattern is intentionally out of scope.

## 8.3 Alias behavior

Aliases may have:

- primary model
- backup models
- override settings
- an `extends` parent alias (inherited primary/backup models and overrides, deep-merged so the child wins)

Clients choose aliases when they want policy-level behavior. A client sees the
alias's resolved result; the `extends` parent is not exposed as a separate model.

Native model names mean exact model request.

## 8.4 Tool and function calling

All three chat APIs (OpenAI Chat Completions, OpenAI Responses, Anthropic
Messages) support function/tool calling in both non-streaming and streaming mode:

- `tools` definitions are forwarded to the Ollama backend (Anthropic
  `input_schema` is translated to the OpenAI/Ollama `parameters` shape).
- Assistant tool calls in message history and tool results (`role:"tool"` for
  OpenAI, `tool_result` blocks for Anthropic) are translated and forwarded.
- Responses with tool calls return the API-native shape: `message.tool_calls`
  with `finish_reason:"tool_calls"` (Chat Completions), `function_call` output
  items (Responses), `tool_use` content blocks with `stop_reason:"tool_use"`
  (Anthropic).
- `tool_choice` is accepted and ignored. Ollama has no native tool choice; the
  backend's default behavior applies. Requests carrying tools are scheduled at
  tool priority.

The proxy does not execute tools; clients do.

---

## 9. States and Edge Cases

## 9.1 No backend available

Client response:

- clean 503-compatible error
- message: no backend available

Debug UI:

- overall status degraded or unavailable
- backend panel shows why

## 9.2 Backend disabled

A backend may be disabled by health logic.

Debug UI must show:
- disabled backend
- reason
- recovery probe/cooldown state

## 9.3 Backend-model disabled

A model may be disabled only on one backend.

Debug UI must show backend/model disabled state.

The model may still be available on other backends.

## 9.4 Queue full

Client response:

- clean overload error
- do not crash
- do not accept more payloads than can be tracked

## 9.5 Disk nearly full

Client response:

- clean storage overload error

Debug UI:

- storage warning if implemented

## 9.6 Client disconnect

Proxy cancels backend request immediately.

No background continuation in v1.

## 9.7 Streaming backend failure

Before first meaningful token/event:
- retry allowed

After first meaningful token/event:
- no retry
- terminate stream with best compatible behavior

## 9.8 Document too complex

No explicit document/vision limits in v1, but disk-backed handling and free-space check must prevent crashes.

If processing fails:
- return clean document processing error
- cleanup temp files

---

## 10. Accessibility Notes

Debug UI should be simple and readable:

- no color-only status indicators
- include text status labels
- table layouts should be readable without animations
- avoid tiny fonts
- auto-refresh should not destroy scroll position where practical
- plain HTML should remain usable without a build step

---

## 11. Permissions and Security from User Perspective

V1 has no authentication.

Assumption:
- proxy is used behind a trusted front proxy or on a trusted local network.

Debug UI is read-only, which reduces risk.

No remote URL file fetching in v1.

Only base64 inline files and multipart uploads are supported.

---

## 12. Out-of-Scope UX for v1

Do not implement:

- admin login
- config editor
- backend enable/disable buttons
- queue mutation controls
- model load/unload buttons
- prompt inspection UI
- persistent file manager
- OpenAI Files API UI
- tool execution UI
- advanced charts
- frontend framework build pipeline

---

## 13. Open Design Decisions

None blocking.

Potential later decisions:

- whether to add admin controls
- whether to add authentication
- whether to add persistent scheduler history
- whether to add full OpenAI Files API
- whether to add remote URL document input
- whether to add model benchmarking UI
