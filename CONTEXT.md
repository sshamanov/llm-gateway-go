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
- **2026-08-26 — Streaming tool_calls finish_reason**: Chat Completions streams now report
  finish_reason "tool_calls" when Ollama sends tool calls before the done chunk; live E2E on qwen3.6:35b.
- **2026-10-17 — Code-review fixes**: SSE now flushes through the logging middleware; scheduler
  leases released exactly once (cap enforced); cancelled/unschedulable jobs reaped with a 1s
  dispatch tick and stream readers exit on cancel; PDF parser bounds-checked with panic recovery,
  and classic xref tables now actually parse; child aliases no longer mutate parent options;
  image calls cancel on disconnect/10 min; Anthropic and Responses streams follow their SDK event
  protocols (no `[DONE]`); Responses input accepts string content and prior assistant turns;
  streaming queue-full is a real 503; TPS learned from eval_duration; stats survive outages;
  document page renders kept under storage and cleaned up; tree gofmt-clean. go vet,
  go test -race ./..., and all 19 mock E2E scenarios pass.

## Live Stream

