# LLM Gateway (Go)

Go-based proxy server for LLM API backends. Exposes OpenAI-compatible and Anthropic-compatible
APIs while routing inference through native Ollama backends.

## Quick Start

```bash
docker compose up
# → http://localhost:4000
```

## Documentation

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — Implementation source of truth: code structure, components, data flow, design decisions.
- **[DESIGN.md](DESIGN.md)** — Product behavior: API reference, debug UI, behavior contracts, env var reference.

## Env Reference

| Variable | Default | Description |
|----------|---------|-------------|
| `PROXY_LISTEN` | `0.0.0.0:4000` | HTTP listen address |
| `PROXY_STORAGE_DIR` | `/data` | Config and temp storage root |

## Dev

```bash
go run ./cmd/proxy
go test ./...
go vet ./...
```
