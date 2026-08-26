# Project Context

Last updated: 2026-08-26

---

## Active Tasks

(none)

## Open Questions

(none)

## Resolved Questions

- **Authority document split** — AGENTS.md = agent rules + git policy + constitution.
  ARCHITECTURE.md = implementation source of truth. DESIGN.md = product behavior + debug UI.
  Each carries its own Boundaries section with cross-references.

## Major Changes

- **2026-05-21 — M1 skeleton**: config, logging, storage, httpapi, main entrypoint.
- **2026-05-22 — M2–M10**: backend discovery, non-streaming chat, scheduler, streaming,
  Responses API, Anthropic Messages, documents, image generation, observability.
  + M11 mock E2E harness (19 scenarios).
- **2026-05-24..25 — Config overhaul**: removed global Options → per-backend
  Options/KeepAlive/Think; alias-level num_ctx/top_k/repeat_penalty/num_predict; alias
  inheritance (extends); config auto-generation on first run; StateAborted + exploration bonus.
- **2026-05-27..29 — Hardening**: multimodal chat content, Docker-only runtime rule, debug UI favicon.
- **2026-06-16 — Client-option + image handling**: forward harmless client options (alias wins);
  strip data-URL prefix from images; use_mmap option; include Ollama error body in non-streaming errors.
- **2026-08-25 — Debug host status, api_key backends, model removal**: /debug host status now
  derived from backend health (down/degraded/disabled instead of available); api_key config for
  openai-compatible image/audio backends sent as Bearer; models deleted from Ollama dropped from
  the proxy list via 5-min staleness expiry + learned-stats pruning (no extra backend polling).
- **2026-08-26 — Tool/function calling wired end-to-end**: Chat Completions, Responses, and
  Anthropic Messages now forward tool definitions to Ollama, translate assistant tool calls and
  tool results in history, and map tool-call responses back to each API's native shape, in both
  streaming and non-streaming mode. Requests carrying tools schedule at tool priority;
  `tool_choice` is accepted and ignored. Verified with full go test + go vet in golang:1.22.

## Live Stream

- Live E2E against qwen3.6:35b (backend 5950): tool calling confirmed on all three APIs, non-streaming + streaming. Found + fixed streaming bug: Chat Completions finish_reason was "stop" on tool-call streams (Ollama sends tool_calls in a separate chunk from done); now "tool_calls" via stream-level tracking. Test updated to model real chunking.

