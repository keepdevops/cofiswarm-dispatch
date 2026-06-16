# cofiswarm-dispatch

Orchestration shell: architect dispatch, SSE streaming, sessions, history, RAG inject (extracted from coordinator).

- Migration: Sprint 8 in [MIGRATION-SPRINTS](https://github.com/keepdevops/cofiswarmdev/blob/main/docs/MIGRATION-SPRINTS.md)
- SSE contract: `spec/sse-events.md` (from stream-sdk)
- Legacy C++: `legacy/cpp/`

## HTTP

| Route | Description |
|-------|-------------|
| `GET /healthz` | Liveness |
| `POST /api/architect` | Non-streaming dispatch (stub → full mode wiring sprint 9) |
| `POST /api/architect/stream` | SSE flat-mode stub stream |
| `GET /api/history` | Full history array |
| `GET /api/history/search?q=` | Search history |
| `POST /api/history/entry` | Append history row |

Default listen: `:8010`.

## FHS state

| Path | Purpose |
|------|---------|
| `/var/lib/cofiswarm/dispatch/sessions/sessions.json` | conversation threads |
| `/var/lib/cofiswarm/dispatch/history/history.json` | run history |

## Test

```bash
make test   # layout + SSE gate + session persistence
```
