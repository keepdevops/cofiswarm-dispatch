## 4. Streaming dispatch (SSE)

`POST /api/architect/stream {prompt, session_id?}` runs the same dispatch as
`/api/architect` but emits Server-Sent Events. The event taxonomy depends on
the active mode:

| Mode | Event sequence |
|---|---|
| all | `session` (first) … |
| flat | `session` `token*` `agent_done*` `metrics` `done` |
| cascade | `session` `token*` `agent_done*` `synthesis_start` `token*` `agent_done` `metrics` `done` |
| pipeline | `session` (`stage` `token*` `agent_done`)<sup>×N</sup> [`synthesis_start` `token*` `agent_done`] `metrics` `done` |
| router | `session` `selected` `token*` `agent_done*` `metrics` `done` |

Event payloads:

- `session` — `{session_id}` — fires before the first token; the UI uses this to wire follow-up BROADCASTs to the correct conversation thread.
- `token` — `{agent, delta}`
- `agent_done` — `{agent}`
- `stage` — `{step, total, agent}` (pipeline only)
- `selected` — `{classifier, agents[]}` (router only)
- `synthesis_start` — `{agent}` (pipeline + cascade)
- `metrics` — `{agent: {calls, total_ms, completion_tokens, ...}}` (final)
- `done` — `[DONE]`

The streaming dispatch honours both the per-mode roster filter and the
circuit breaker — tripped agents are silently excluded.

---

