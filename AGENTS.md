# Project Authority

CLAUDE.md is a symlink to this file. This is the single canonical authority document for all
AI agents (Claude Code, Codex, etc.) working in this repository.

---

## Boundaries

**This document owns:**
- How agents behave in this repo — startup sequence, live streaming, task completion, compaction
- Project rules: commit style, git policy, branch conventions, what is committed
- Build rules: stack, commands, naming conventions, directory layout
- Project constitution: scope, goals, work style
- The Source-of-Truth Map — which document covers which concern

**This document does NOT contain:**
- Code structure, component design, data flow, implementation decisions → **[ARCHITECTURE.md](ARCHITECTURE.md)**
- API reference, product behavior, debug UI specification, UX rules → **[DESIGN.md](DESIGN.md)**
- Active tasks, open questions, decision history, live action stream → **[CONTEXT.md](CONTEXT.md)**
- Repo overview, quick start, env reference → **[README.md](README.md)**
- Immutable change history → **`git log`**

**Rewrite policy:** User request only. This document defines the rules for all other documents.

---

## Source-of-Truth Map

| Concern | Authority | Rule |
|---------|-----------|------|
| Agent operational rules, git policy, project constitution | `AGENTS.md` | Rewrites only by user request |
| Implementation architecture, code structure, component design, data flow, design decisions | `ARCHITECTURE.md` | Rewrites on design/architecture changes |
| Product behavior, API UX, read-only debug UI | `DESIGN.md` | Rewrites on API/UX/behavior changes |
| Current state, active tasks, open questions, compacted decision history, live action stream | `CONTEXT.md` | Agent writes detailed present; user confirms outcomes; only resolved+confirmed entries compacted; **live stream at end of file: strictly one-line entries.** |
| Immutable action ledger — one intent per commit | `git log` | Short, permanent. Subject = intent, body = brief validation. No file lists, no metadata trailers. |
| Repo overview, quick start, env reference | `README.md` | Brief, kept current. Points to ARCHITECTURE.md and DESIGN.md for detail. |
| Runtime state (data, secrets, artifacts) | volume mounts, env files | Never committed |

### What goes where

- **AGENTS.md**: How agents behave. Project rules. Git policy. Document boundaries.
  No architecture descriptions, no temporal state, no ledger entries.

- **ARCHITECTURE.md**: Implementation source of truth. Code layout, components, data flow,
  design decisions, stack details, naming conventions, directory structure.
  No product behavior, no API UX, no debug UI, no temporal state.

- **DESIGN.md**: Product behavior specification. API reference, UI/UX details,
  read-only debug UI, behavior contracts, env var reference.
  No code structure, no implementation details, no temporal state.

- **README.md**: Front door of the repo. Project name, one-line description,
  quick start, links to ARCHITECTURE.md and DESIGN.md, env reference.
  No architecture narrative, no API reference, no temporal state.

- **CONTEXT.md**: A living, shrinkable project journal with a termination-proof
  live stream tail.
  **Structured sections**: Active tasks, in-progress work, open questions, recent
  decisions, implementation notes — no compaction while unresolved.
  **Compacted on the past**: only entries that are both resolved AND user-confirmed
  get compressed. Recent resolved entries keep reversibility detail; older resolved
  entries collapse to concise decision summaries. Both agent and user positions
  are always preserved. Status words are precise: executed, implemented, resolved,
  confirmed — not "Done" or "Fixed."
  **Live Stream**: The `## Live Stream` section at the very end of the file is an
  append-only action log. Each entry is strictly one line describing what was done.
  No timestamps in stream entries — they go into the compacted structured section.
  If the session is terminated mid-task, the next session can read the stream and
  resume. Before committing, compact stream entries into a structured
  section above, then clear the stream.

- **git log**: The immutable action ledger. Each commit is one logical change —
  short, permanent, intent-driven. Subject = why. Body = what was validated, briefly.
  Not a project journal, not a status tracker. Compact by nature; never edited after push.

---

## Agent Operational Rules

0. **No autonomous actions without a task**: Do not start, stop, build, run, or modify
   anything unless the user has explicitly asked for it. Exploration (reading files,
   searching code, asking questions) is fine. Actions (running servers, editing files,
   creating commits, executing commands that change state) require an explicit task
   from the user. A bare `@` or empty prompt is not a task — ask, don't guess.

1. **Read all authority sources on startup**: `AGENTS.md`, `ARCHITECTURE.md`, `DESIGN.md`,
   `CONTEXT.md`, recent `git log`. Check for missing parts, misalignments, and the last tasks
   worked on.
   **If Live Stream entries exist**: the previous session was interrupted —
   read the stream to understand what was in progress and pick up from the last
   incomplete task.

2. **Session start**: Write the first Live Stream entry describing the task.

3. **Live Stream every action**: After every meaningful action (file edit, test run,
   decision made, error encountered and resolved, discovery), append a single-line
   entry to the `## Live Stream` section at the end of `CONTEXT.md`.
   Strictly one line per message. No timestamps in stream entries — timestamps go
   into the compacted structured section. Stream immediately — do not batch.

4. **Before implementing**: Check `CONTEXT.md` for active tasks and open questions.
   Check `ARCHITECTURE.md` for code structure and design decisions.
   Check `DESIGN.md` for API contracts and behavior specs.

5. **After completing a logical task**: Structure the accumulated Live Stream
   entries into a proper timestamped section (e.g. `## YYYY-MM-DD — Task Name`),
   place it above the Live Stream section (before `## Open Questions`),
   then clear the stream. Record the outcome using precise status
   (executed, implemented, resolved). Only the user marks a task as
   confirmed/closed. Do NOT compact the stream before the task is complete —
   an interrupted task leaves its stream for the next agent.

6. **CONTEXT.md compaction**: Only compact entries that are both resolved AND
   user-confirmed. Active, in-progress, and executed-but-unconfirmed entries
   stay detailed — no compaction. When compacting:
   - Recently resolved: preserve enough detail to reverse the decision if needed.
   - Older resolved: aggressive summarization — e.g. a multi-step investigation
     compresses to "user decided: use X approach for Y."
   - Always keep both agent and user positions in the entry.
   - Use precise status words: executed, implemented, resolved, confirmed —
     avoid generic "Done" or "Fixed".

7. **Commit style**: One logical change per commit. Subject = intent (short).
   Body = brief validation result. The commit is a permanent ledger entry,
   not a project journal — keep it compact.

---

## Git Policy

### Commit format

```
<imperative summary of intent, max 72 chars>

<Brief validation: what was tested, what was observed.
Keep it short — this is a permanent ledger entry, not a journal.
Wrap at 72.>
```

### Rules

- Subject = intent, not mechanics. Compact.
- Body = what was validated and observed, briefly. No file lists (visible in diff).
- No metadata trailers.
- One logical change per commit — no bundling.
- Never amend published commits. Never skip hooks. Never force-push to main/master.
- Stage specific files, not `git add -A`.

### Branch convention

- `main`: stable, deployable.
- Feature/fix branches: descriptive short names.

---

## Build Rules

### Stack

- **Language**: Go
- **Lint/format**: `go vet`, `gofmt`
- **Docker**: multi-stage build, always use `--network host` for build and `network_mode: host` for run
- **Runtime**: Docker-only. The binary is never run directly on the host. Docker Compose is the sole supported runtime entrypoint.

### Commands

| Command | Description |
|---------|-------------|
| `docker compose up -d` | Start the service (canonical) |
| `docker compose logs -f` | Follow service logs |
| `docker compose down` | Stop the service |
| `go test ./...` | Run all tests |
| `go vet ./...` | Run vet linter |
| `gofmt -w .` | Format all Go files |

### What is committed

- Source code, config files, Docker definitions, documentation.
- Never: binaries, data files, `.env`, secrets, temporary artifacts.

---

## Project Constitution

### Scope

LLM Go Proxy — a Go-based proxy server for LLM API backends.

### Goals (priority order)

1. Reliable proxying: streaming, error handling, backend routing.
2. Observability: structured logging, metrics, health checks.
3. Simple operations: single binary, containerized, easy to configure.

### Work Style

- Commit every meaningful move with a short, intent-driven message (action ledger).
- Docker Compose is the canonical runtime entrypoint.
- Favor simplicity over features. No premature abstraction.
