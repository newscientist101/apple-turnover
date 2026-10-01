# Agent and listener contract

This is the authoritative description of the wire contract: what an agent may
ask the server to do over HTTP, and what a listener receives over the
WebSocket. It is verified against the source — the route table is checked
against `routes()` by `TestReadmeAPITableMatchesRoutes`, and each event kind
below by the tests named beside it.

The HTTP surface is stable; the listener event vocabulary is the part that
grows, and every addition to it is a reviewed change to this file.

## The performance

There is one live performance, held in memory by a single `Conductor`. It is
ephemeral: nothing is persisted, there are no saved sets, and restarting the
server starts an empty performance at version 0, already playing.

## HTTP API

Bodies are JSON. Every `/api` request body is capped at 64 KiB
(`APIMaxBodyBytes`); anything larger is rejected with 413 before it is parsed,
so an oversized AND malformed body is a 413 rather than a 400. Failures are
JSON too, always `{"error": "..."}`.

| Endpoint | Purpose |
|---|---|
| `GET /api/state` | the current snapshot |
| `POST /api/code` | push new strudel code; bumps the version |
| `POST /api/message` | set the message shown to listeners |
| `POST /api/play`, `POST /api/hush` | start / stop the performance |
| `POST /api/eval-result` | report how the last code evaluated |
| `GET /ws` | listener WebSocket; one JSON frame per state change |
| `GET /` | the performance page |
| `/static/` | the page's assets |

Every accepted write returns the resulting snapshot, except
`POST /api/eval-result`, which answers `{accepted, version}`. A snapshot is:

```json
{
  "version": 7,
  "code": "s(\"bd*2, ~ cp\")",
  "lastAgentMessage": "four on the floor",
  "anchor": {"epochMs": 1757000000000, "cps": 0.5},
  "history": [],
  "playing": true,
  "listenerCount": 3,
  "lastEvalResult": null
}
```

The agent loop this is designed for:

1. `GET /api/state` — read the version you last pushed and the verdict for it.
2. Decide what to change, and `POST /api/code` (optionally with a `message`).
3. Read `POST /api/code`'s response: it is the snapshot the listeners were just
   sent, so the agent and the listeners can never disagree about what is live.
4. Wait for `POST /api/eval-result` to land (any listener may report it), read
   it back from `GET /api/state.lastEvalResult`, and iterate.

Worked example of one unprompted evolution step — a browser has just reported
that the agent's pattern threw, and the agent corrects it without being asked:

```bash
# The browser reported: {"version":7,"ok":false,"error":"x is not a function"}
curl -s localhost:8000/api/state | jq '.lastEvalResult'
# => {"version":7,"ok":false,"error":"x is not a function",...}

# Fix it: keep the shape, drop the undefined function, and say so.
curl -s localhost:8000/api/code \
  -d '{"code":"s(\"bd*2\").slowcat(\"<x2 x3>\")","message":"dropping the broken layer"}'
# => {"version":8, ...}
```

## Versioning guarantees

* The version is contiguous, unique, never reused and never skipped, including
  under concurrent pushes.
* `POST /api/code` bumps it by exactly one. `POST /api/message`, the transport
  endpoints, a listener-count change and a stored verdict never do.

## Listener WebSocket

`GET /ws` upgrades to a WebSocket. A listener that connects receives one frame
immediately — the full snapshot — so it lands mid-performance instead of waiting
for the next change, which may never come.

There is exactly ONE frame shape, and every frame carries the full state:

```json
{"kind": "<what happened>", "snapshot": { ... }}
```

`snapshot` is the same object `GET /api/state` returns, produced by the same
encoder. A client therefore never merges a delta: it replaces what it holds.
The complete vocabulary of `kind` is:

| kind | sent when |
|---|---|
| `snapshot` | the listener connects — the catch-up, nothing changed |
| `code` | a new code document was published (the version bumped) |
| `message` | new agent narration (same version, same music) |
| `transport` | a hush or a play changed the `playing` flag |
| `eval-result` | a browser reported a verdict, and it was STORED |
| `listener-count` | a listener connected or left, or the hub dropped one |

`listener-count` is not a change to the performance: it never bumps the version
and it carries no new music. It exists so a listener's "N people are here"
display is live rather than only as fresh as the last code push. Two rules make
it cheap to consume:

* The listener that CAUSED the change is not sent it. One that just connected
  already has that exact count in its `snapshot` frame; one that just left has
  gone.
* Frames are never coalesced, and do not need to be: a burst of connects
  produces one frame per connect, in order, so a client that applies them in
  order ends up exactly where the server is.

Two kinds of event are deliberately NOT broadcast: a request that was rejected
(empty code, an oversized or malformed body, a verdict for a version that was
never published), and a verdict that was understood but discarded as stale.
Both leave the state untouched, and a frame carries no "nothing changed"
marker, so announcing them would be indistinguishable on the wire from a real
change.

A listener that stops reading is dropped: the server keeps a bounded
per-listener queue and drops a client that cannot keep up rather than waiting
for it. The server also pings each listener every 30s and reaps it if it cannot
answer within 5s, which is what reclaims a listener that vanished without
closing — a yanked cable, a killed tab — which the drop-on-overflow rule cannot
catch on a quiet performance, since such a listener is sent nothing at all.

Worked example, from a browser:

```js
const ws = new WebSocket(`ws://${location.host}/ws`);
ws.onmessage = ({ data }) => {
  const { kind, snapshot } = JSON.parse(data);
  replaceState(snapshot);          // wholesale, always — never a merge
  if (kind === "eval-result" && !snapshot.lastEvalResult.ok) {
    console.warn("the agent pushed something that did not run");
  }
};
```

## See also

* `README.md` — how to build and run it, and the invariants this repo holds.
* `AGENTS.md` — the architecture invariants and how they are proved.