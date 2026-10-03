# Agent and listener contract

This file is the authoritative wire contract for external agents and browser listeners. Contract changes must be changed here and in the implementation in the same commit.

The contract is checked against the route table, frame kinds, and a running server. Keep the documented endpoint table and JSON shapes exact.

## The performance

There is one live performance, stored in memory by one `Conductor`. Nothing is persisted. Restarting the server starts an empty performance at version `0`, already playing.

## HTTP API

Bodies are JSON. `/api` bodies are capped at 64 KiB. Oversized bodies are rejected before JSON parsing.

All `/api` failures use exactly:

<!-- shape:error -->
```json
{"error":"..."}
```

| Endpoint | Request | Success response | Bumps version |
|---|---|---|---|
| `GET /api/state` | none | [snapshot](#the-snapshot) | no |
| `POST /api/code` | [codeRequest](#post-apicode) | snapshot | **yes, by one** |
| `POST /api/message` | [messageRequest](#post-apimessage) | snapshot | no |
| `POST /api/anchor` | [anchorRequest](#post-apianchor) | snapshot | no |
| `POST /api/hush` | [no body](#post-apihush--post-apiplay) | snapshot | no |
| `POST /api/play` | [no body](#post-apihush--post-apiplay) | snapshot | no |
| `POST /api/eval-result` | [evalResultRequest](#post-apieval-result) | [evalAck](#post-apieval-result) | no |
| `POST /api/heartbeat` | [no body](#post-apiheartbeat) | snapshot | no |
| `GET /ws` | WebSocket upgrade | JSON listener frames | no |
| `GET /` | none | HTML page | no |
| `/static/` | none | static assets | no |

Every accepted write returns the resulting snapshot except `/api/eval-result`, which returns `{accepted,version}`. A successful write does not create a new URL, so responses are `200`, not `201`.

### `POST /api/code`

The only endpoint that changes the code document.

<!-- shape:codeRequest -->
```json
{"code":"s(\"bd*2, ~ cp\")","message":"four on the floor"}
```

- `code` is required and must be non-blank.
- `message` is optional narration. If omitted, the previous message remains.
- Unknown fields are rejected.
- The server does not parse or evaluate the Strudel source.
- The version increments exactly once.

### `POST /api/message`

Narration only.

<!-- shape:messageRequest -->
```json
{"message":"four on the floor"}
```

`message` is required and non-blank. It does not change the code, version, or history.

### `POST /api/anchor`

Replaces the shared timeline.

<!-- shape:anchorRequest -->
```json
{"epochMs":1757000000000,"cps":0.5}
```

Both fields are required.

- `cps` must be `> 0` and `<= 1000`.
- `epochMs` must be within five minutes of the server clock.
- Invalid anchors change nothing and are not broadcast.
- Anchors do not bump the code version.

The shared cycle grid is:

```text
cycle i begins at epochMs + i * 1000 / cps milliseconds
```

### What the anchor does not do

This system provides **bar-aligned, not sample-accurate, cross-machine synchronization**.

Strudel's `NeoCyclist` does not provide a cross-machine audio clock. The application's synchronization mechanism is the shared anchor plus a client commit deferred to the next cycle boundary. Re-anchoring is the recovery mechanism for drift.

The browser reports observed drift. Without a usable anchor it reports `unscheduled` rather than inventing a bar position.

Do not describe the system as lockstep or sample-accurate.

### `POST /api/hush` / `POST /api/play`

Transport intent only; the server produces no audio.

Both endpoints accept **no fields**. An empty body or `{}` is valid. Any field is rejected. Both are idempotent.

### `POST /api/eval-result`

A browser reports the result of evaluating a published version in its sandbox REPL.

<!-- shape:evalAck -->
```json
{"accepted":true,"version":7}
```

<!-- shape:evalResultRequest -->
```json
{"version":7,"ok":false,"error":"x is not a function","stats":{"haps":0}}
```

- `version` is required and must name a published version.
- `ok` is the browser's verdict.
- `error` is optional.
- `stats` is optional opaque JSON supplied by the browser and echoed back.

`accepted:true` means the report was understood, not necessarily stored. A valid report older than the stored verdict is accepted but discarded as stale.

### `POST /api/heartbeat`

Renews the agent's liveness lease. Argument-free and idempotent.

An agent speaks plain request/response HTTP and holds **no persistent connection**, so the server cannot observe a socket and cannot otherwise know an agent exists. This lease is the whole of what it knows.

- Send it periodically — more often than the TTL — to be counted as present.
- It never bumps the version and never changes the music.
- A heartbeat broadcasts **nothing** to listeners: it renews a lease they already believe is held.

**The lease decays.** A heartbeat counts the agent as present for **15 seconds** (`AgentTTL`). Stop sending and the server reports the agent absent 15s after the last beat, exactly once.

`lastSeenMs` is `0` when no agent has *ever* connected, which is distinct from a heartbeat that landed at the Unix epoch — `0` is reserved as the "never" marker. The cost is that a heartbeat landing at exactly epoch millisecond `0` is stored as `0` and is therefore permanently indistinguishable from never-seen. This is unreachable in practice and cheaper than a second "has ever been seen" flag that could disagree with this one.

Decay is a **server-side decision**, made against the server's own clock. A client must not infer presence from its own clock against `lastSeenMs`; it inherits clock skew and will disagree with the server near the boundary. Read `agent.active` as the server states it.

This is liveness, not a session. The server cannot distinguish an agent that crashed from one that finished cleanly and exited — both stop beating and both decay.

## The snapshot

`GET /api/state` and accepted writes return this object. WebSocket frames embed the same snapshot value.

<!-- shape:snapshot -->
```json
{
  "version":7,
  "code":"s(\"bd*2, ~ cp\")",
  "lastAgentMessage":"four on the floor",
  "anchor":{"epochMs":1757000000000,"cps":0.5},
  "history":[],
  "playing":true,
  "listenerCount":3,
  "lastEvalResult":null,
  "agent":{"active":true,"lastSeenMs":1757000000123}
}
```

| Field | Meaning |
|---|---|
| `version` | Current code version; starts at `0` |
| `code` | Current Strudel source |
| `lastAgentMessage` | Most recent non-empty narration |
| `anchor` | Shared timeline (`epochMs`, `cps`) |
| `history` | Most recent 32 code versions, oldest first; `[]` when empty |
| `playing` | Transport intent, not audio state |
| `listenerCount` | Current `/ws` listeners |
| `lastEvalResult` | Stored browser verdict, or `null` before one is stored |
| `agent.active` | Whether an agent heartbeat landed within the TTL |
| `agent.lastSeenMs` | Server timestamp of the last heartbeat; `0` if no agent has ever connected |

`anchor.cps` defaults to `0.5` cycles per second. `history` is bounded at 32 versions.

A stored verdict adds the server timestamp:

<!-- shape:storedEvalResult -->
```json
{"version":7,"ok":false,"error":"x is not a function","stats":{"haps":0},"epochMs":1757000000123}
```

Empty `error` and `stats` fields are omitted.

## Errors and status codes

| Situation | Status | `error` |
|---|---|---|
| Unknown JSON field | `400` | `invalid JSON body: json: unknown field "messge"` |
| Trailing JSON data | `400` | `invalid JSON body: unexpected data after JSON body` |
| Empty or truncated body | `400` | `invalid JSON body: empty or truncated request body` |
| Blank `code` | `400` | `code must not be empty: send the strudel pattern to play` |
| Blank `message` | `400` | `message must not be empty: use POST /api/code to change the music, or send narration text` |
| Body supplied to `/api/hush` or `/api/play` | `400` | `invalid JSON body: this endpoint takes no arguments: unexpected field(s) code (use POST /api/code to change the music)` |
| Body supplied to `/api/heartbeat` | `400` | `invalid JSON body: this endpoint takes no arguments: unexpected field(s) agentId (use POST /api/code to change the music)` |
| Body over 64 KiB | `413` | `request body too large: limit is 65536 bytes` |
| Verdict for an unpublished version | `400` | `unknown version: 9999 (latest is 0)` |
| Wrong method on an API endpoint | `405` + `Allow` | `method GET not allowed on /api/code, use POST` |
| Unknown `/api` endpoint | `404` | `no such API endpoint: GET /api/nope` |

Every `/api` error response is the single-field JSON error object above. Errors outside `/api` use ordinary `net/http` behavior. Messages name the offending field, limit or version so an agent can branch on them, not only on the status.

## The agent loop

1. `GET /api/state` to read the current version and last verdict.
2. Decide what to change.
3. `POST /api/code` (optionally with `message`).
4. Wait for a browser to submit `POST /api/eval-result`.
5. Read `GET /api/state` and inspect `lastEvalResult`.
6. Iterate.

While doing this, beat on `POST /api/heartbeat` more often than every 15 seconds — including while waiting in step 4, which is the step most likely to outlast the lease. An agent that heartbeats only when it has something to push will be reported absent during exactly the wait it is most idle through.

The push response proves only that the document was stored. It is not an evaluation result. With no connected listener, no verdict will arrive.

## Validate-then-commit

Every write follows:

```text
validate -> commit -> broadcast
```

| Outcome | HTTP | Stored | Broadcast |
|---|---|---|---|
| accepted | `200` | yes | yes |
| accepted but stale | `200` | no | no |
| rejected | `400` / `405` / `413` | no | no |

The stale case applies to older `eval-result` reports. Because both accepted cases return `accepted:true`, the agent must read the stored verdict rather than treating the acknowledgement as the verdict.

## Versioning guarantees

- Versions are contiguous, unique, monotonic, and never skipped, including under concurrent pushes.
- Only `POST /api/code` increments the version.
- Message, anchor, transport, listener-count, agent-presence, and stored evaluation-result changes do not increment it.

## Listener WebSocket

`GET /ws` upgrades to a WebSocket. A new listener receives an immediate full snapshot.

There is exactly one frame shape:

<!-- shape:frame -->
```json
{"kind":"<what happened>","snapshot":{...}}
```

The client replaces its entire local snapshot; it does not merge deltas.

<!-- frame-kinds -->
| kind | sent when |
|---|---|
| `snapshot` | Listener connects |
| `code` | New code version published |
| `message` | Narration changed |
| `transport` | `playing` changed via play/hush |
| `eval-result` | A browser verdict was stored |
| `listener-count` | A listener connected, left, or was reaped |
| `anchor` | Shared timeline changed |
| `agent` | The agent lease lapsed |

`hush` and `play` intentionally share `transport` because listeners care about the resulting `playing` state.

`agent` is sent on exactly one occasion: a **held** lease lapsing. A heartbeat broadcasts nothing, and a server that has never seen an agent never announces one going away, because never-seen is the initial state rather than a transition. A client therefore treats `agent` as "the agent you were watching has gone", and re-reads `agent.active` from the frame rather than inferring the timing itself.

### Subscribe, then snapshot

A listener is subscribed before its initial snapshot is generated. That ordering prevents a state change from landing in the gap between subscribing and receiving the snapshot.

The new listener's own count change is not sent as a separate `listener-count` event because its snapshot already includes itself.

### Listener-count behavior

`listenerCount` is not a performance revision and never bumps the code version.

- The causing listener does not receive its own count event.
- Existing listeners receive count changes in order.
- Per-listener queues are bounded at 64 messages.
- Slow listeners are dropped rather than blocking the hub.
- The server pings listeners every 30 seconds and reaps those that cannot respond within 5 seconds.
- Every WebSocket write has a 5-second deadline.

### Handshake behavior

`/ws` is the exact path. `/ws/` and `/ws/anything` are not listeners.

Expected upgrade/error behavior:

- `426` — handshake-less `GET`
- `400` — bad WebSocket version
- `501` — connection cannot be hijacked
- `403` — cross-origin handshake
- `405` + `Allow` — wrong method
- `1001` — listener arrives after hub shutdown
- `1000` — normal hub shutdown

## Worked example: feedback loop

```console
$ curl -s localhost:8000/api/code -d '{"code":"s(\"bd*2\").x(3)","message":"first attempt"}'
{"version":1,...}

# Browser evaluates version 1 and reports failure.
$ curl -s localhost:8000/api/eval-result \
    -d '{"version":1,"ok":false,"error":"x is not a function","stats":{"haps":0}}'
{"accepted":true,"version":1}

# Agent reads the stored verdict.
$ curl -s localhost:8000/api/state | jq '.lastEvalResult.ok'
false

# Agent publishes a correction.
$ curl -s localhost:8000/api/code \
    -d '{"code":"s(\"bd*2\")","message":"dropping the broken layer"}'
{"version":2,...}

# The agent proves it is alive; stop beating and this decays in 15s.
$ curl -s localhost:8000/api/heartbeat -d ''
{"version":2,...,"agent":{"active":true,"lastSeenMs":1757000000456}}
```

The server never evaluated either pattern. The browser did.

## See also

- `README.md` — build, run, deploy, and operational limits.
- `AGENTS.md` — architecture invariants, task workflow, and verification rules.
