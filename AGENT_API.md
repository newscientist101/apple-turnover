# Agent and listener contract

This is the authoritative description of the wire contract: what an agent may
ask the server to do over HTTP, and what a listener receives over the
WebSocket. It is not maintained by hand.

* Every route in `routes()` is checked against this file by
  `TestReadmeAPITableMatchesRoutes`, in both directions — a documented endpoint
  the server does not mount fails, and so does a mounted one missing here.
* Every frame kind in `srv/event.go` is checked by
  `TestAgentAPIDocCoversEveryEventKind`, in both directions, and that check
  refuses to pass if it cannot parse the kinds out of the source at all.
* Every payload shape below is checked against a **running server** by
  `TestAgentAPIDocPayloadShapesMatchARunningServer`, which drives the real
  handler tree and compares live JSON against the shapes printed here. Adding,
  renaming or removing a field breaks that test rather than this file quietly.

So a change to the contract is a change to this file *and* the code, in the same
commit. The HTTP surface is stable; the listener event vocabulary is the part
that grows, and every addition to it is a reviewed change here.

## The performance

There is one live performance, held in memory by a single `Conductor`. It is
ephemeral: nothing is persisted, there are no saved sets, and restarting the
server starts an empty performance at version 0, already playing.

## HTTP API

Bodies are JSON. Every `/api` request body is capped at 64 KiB
(`APIMaxBodyBytes`); anything larger is rejected with 413 before it is parsed,
so an oversized AND malformed body is a 413 rather than a 400. Failures are
JSON too, always exactly one field:

<!-- shape:error -->
```json
{"error": "..."}
```

| Endpoint | Request | Success response | Bumps version |
|---|---|---|---|
| `GET /api/state` | none | [snapshot](#the-snapshot) | no |
| `POST /api/code` | [codeRequest](#post-apicode) | snapshot | **yes, by one** |
| `POST /api/message` | [messageRequest](#post-apimessage) | snapshot | no |
| `POST /api/hush` | [no body](#post-apihush--post-apiplay) | snapshot | no |
| `POST /api/play` | [no body](#post-apihush--post-apiplay) | snapshot | no |
| `POST /api/eval-result` | [evalResultRequest](#post-apieval-result) | [evalAck](#post-apieval-result) | no |
| `GET /ws` | WebSocket upgrade | one JSON frame per state change | no |
| `GET /` | none | the performance page (HTML) | no |
| `/static/` | none | the page's assets | no |

Every accepted write returns the resulting snapshot, except
`POST /api/eval-result`, which answers `{accepted, version}`. A write never
returns 201: a push does not create a resource at a new URL, it updates the one
live document.

### `POST /api/code`

The agent's only way to change what is playing. The server stores the document,
bumps the version by exactly one, and never parses or evaluates the JavaScript.

<!-- shape:codeRequest -->
```json
{"code": "s(\"bd*2, ~ cp\")", "message": "four on the floor"}
```

`code` is required and must be non-blank. `message` is optional narration shown
to listeners beside the code; omit it to keep the previous narration. Any other
field is a 400 — a typo like `messge` is never silently ignored.

### `POST /api/message`

Narration only. It does not touch the code document, the version or the
history, so the music keeps playing while the words change.

<!-- shape:messageRequest -->
```json
{"message": "four on the floor"}
```

`message` is required and must be non-blank. An empty message is a 400 rather
than a silent no-op, because a no-op is indistinguishable in the agent's loop
from a lost request.

### `POST /api/hush` / `POST /api/play`

Transport intent, and nothing else: there is no audio in Go. Both take **no
body**. An absent body, or `{}`, is fine; any field at all is a 400, so a client
cannot smuggle a `code` field through these endpoints believing it changed the
music. Both are idempotent — re-asserting the current state succeeds, because
the agent may not have read `/api/state` first.

### `POST /api/eval-result`

A browser reports whether a published version actually evaluated in its sandbox
repl. This is what closes the agent's feedback loop, and it is the one endpoint
whose response is not a snapshot:

<!-- shape:evalAck -->
```json
{"accepted": true, "version": 7}
```

`accepted` is deliberately not named `ok`, so it cannot be mistaken for the
verdict itself. The verdict is the request's own `ok`, read back from
`/api/state`.

<!-- shape:evalResultRequest -->
```json
{"version": 7, "ok": false, "error": "x is not a function", "stats": {"haps": 0}}
```

`version` is required and must name a version that was actually published.
`ok` is the verdict. `error` and `stats` are optional; `stats` is opaque
client-supplied JSON (hap counts and similar), stored and echoed back verbatim.
The Go server never evaluates JavaScript, so this is pure bookkeeping.

`accepted: true` means *the report was well-formed*, not that it was stored. A
report naming a version OLDER than the verdict already held is understood and
deliberately discarded: it answers `200 {"accepted":true,...}` and changes
nothing. See [validate-then-commit](#validate-then-commit) below.

### The snapshot

The response to `GET /api/state` and to every accepted write except
`/api/eval-result`. It is produced by one encoder, so the body and the
`snapshot` object inside a WebSocket frame are byte-identical.

<!-- shape:snapshot -->
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

| Field | Meaning |
|---|---|
| `version` | the live code document's version; starts at 0 |
| `code` | the live strudel document |
| `lastAgentMessage` | the most recent non-empty narration |
| `anchor` | the shared timeline: `epochMs` and `cps` (cycles per second, 0.5) |
| `history` | recent code versions, oldest first; `[]` when empty, never `null` |
| `playing` | transport intent, not audio |
| `listenerCount` | live `/ws` listeners; see [listener-count](#listener-count-is-not-a-change) |
| `lastEvalResult` | the stored verdict, or `null` before any is stored |

`anchor.cps` is 0.5 unless a future change says otherwise: clients derive their
scheduler position from `epochMs + cps` so listeners sharing a page do not drift
apart. `history` holds the most recent 32 versions.

A stored `lastEvalResult` has the shape of the eval request plus an `epochMs`
the server stamps on it:

<!-- shape:storedEvalResult -->
```json
{"version": 7, "ok": false, "error": "x is not a function", "stats": {"haps": 0}, "epochMs": 1757000000123}
```

`error` and `stats` are omitted when empty, so a passing verdict is
`{"version":7,"ok":true,"epochMs":...}`. A client must tolerate their absence
rather than assuming `error` is always present.

### Errors and status codes

Uniform across the API: 200 for an accepted command, 400 for a malformed or
invalid body, 404 for an unregistered `/api` path, 405 plus an `Allow` header
for the wrong verb, 413 for an oversized body. Every `/api` failure — including
404 and 405, which `net/http` would otherwise answer in plain text — is the
one-field JSON error object above. Errors outside `/api` stay plain text.

| Situation | Status and error |
|---|---|
| unknown field in a body (`"messge"`) | 400 `invalid JSON body: json: unknown field "messge"` |
| valid JSON followed by more data | 400 `unexpected data after JSON body` |
| empty or truncated body | 400 `invalid JSON body: empty or truncated request body` |
| blank `code` | 400 `code must not be empty: send the strudel pattern to play` |
| blank `message` | 400 `message must not be empty: use POST /api/code to change the music, or send narration text` |
| any field on `/api/hush` or `/api/play` | 400 `this endpoint takes no arguments: unexpected field(s) code (use POST /api/code to change the music)` |
| body over 64 KiB (checked before parsing) | 413 `request body too large: limit is 65536 bytes` |
| verdict for a version never published | 400 `unknown version: 99 (latest is 7)` |
| wrong verb on a real endpoint | 405, with `Allow` |
| unregistered path under `/api` | 404 `no such API endpoint: GET /api/nope` |

The messages are specific enough to act on — which field, which limit, which
version — so an agent can branch on them rather than only on the status.

## The agent loop

1. `GET /api/state` — read the version you last pushed and the verdict for it.
2. Decide what to change, and `POST /api/code` (optionally with a `message`).
3. Read `POST /api/code`'s response: it is the snapshot the listeners were just
   sent, so the agent and the listeners can never disagree about what is live.
4. Wait for `POST /api/eval-result` to land (any listener may report it), read
   it back from `GET /api/state.lastEvalResult`, and iterate.

The loop is closed by polling `/api/state`, not by the push response: a push
returns the state the moment it is stored, which says nothing about whether the
code actually ran.

## Validate-then-commit

Every write is fully validated before anything is stored, and a change is
published to listeners only after it has been committed. Three outcomes, and the
middle one is the one to be careful with:

| Outcome | HTTP | Stored? | Broadcast? |
|---|---|---|---|
| accepted | 200 | yes | yes |
| **accepted but ignored** | 200 | **no** | **no** |
| rejected | 400 / 405 / 413 | no | no |

The middle row exists for exactly one request: an eval-result naming a version
older than the verdict already held. It is well-formed, so it is accepted; it is
stale, so it is dropped. The agent cannot tell the first two rows apart from the
response alone — both are `200 {"accepted":true}` — which is why the loop reads
the verdict back from `/api/state` rather than trusting the ack.

Nothing is ever broadcast for the middle or last row. A frame carries no
"nothing changed" marker, so announcing a change that did not happen would be
indistinguishable on the wire from a real one. Silence is the honest answer.

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

<!-- shape:frame -->
```json
{"kind": "<what happened>", "snapshot": { ... }}
```

`snapshot` is the same object `GET /api/state` returns, produced by the same
encoder. A client therefore never merges a delta: it replaces what it holds.
The complete vocabulary of `kind` is:

<!-- frame-kinds -->
| kind | sent when |
|---|---|
| `snapshot` | the listener connects — the catch-up, nothing changed |
| `code` | a new code document was published (the version bumped) |
| `message` | new agent narration (same version, same music) |
| `transport` | a hush or a play changed the `playing` flag |
| `eval-result` | a browser reported a verdict, and it was STORED |
| `listener-count` | a listener connected or left, or the hub dropped one |

`hush` and `play` are deliberately one kind, not two: what a listener needs is
the resulting `playing` flag, which the snapshot carries, and hush-then-play is a
transition rather than two distinct facts about the performance.

### Subscribe, then snapshot

The order is load-bearing, and it is the server's order — a listener subscribes
first and is snapshotted second, so no change can slip through the gap between
reading the state and joining the fan-out. The reverse order would let a push
land in that gap, and a listener already displaying the state from before it
would never learn about the version it missed.

A consequence worth stating plainly: a connecting listener's own
`listener-count` change is published *before* its snapshot is sent, so its
catch-up frame already reports a count that includes itself. That is precisely
why it is never also sent a count frame about its own arrival.

### `listener-count` is not a change

A count change is not a change to the performance, so this frame **never bumps
the version** and carries no new music. It exists so a listener's "N people are
here" display is live rather than only as fresh as the last code push. Two rules
make it cheap to consume:

* The listener that CAUSED the change is not sent it. One that just connected
  already has that exact count in its `snapshot` frame; one that just left has
  gone.
* Frames are never coalesced, and do not need to be: a burst of connects
  produces one frame per connect, in order, so a client that applies them in
  order ends up exactly where the server is.

A listener that stops reading is dropped: the server keeps a bounded
per-listener queue and drops a client that cannot keep up rather than waiting
for it. The server also pings each listener every 30s and reaps it if it cannot
answer within 5s, which is what reclaims a listener that vanished without
closing — a yanked cable, a killed tab — which the drop-on-overflow rule cannot
catch on a quiet performance, since such a listener is sent nothing at all.
Every write to a listener has a 5s deadline, so a wedged socket can never delay
an API response.

### Handshake failures

`/ws` is the only path matched, so `/ws/` and `/ws/anything` are plain 404s. A
`GET /ws` that is not a valid upgrade is answered explicitly rather than
panicking or 500ing: 426 for a handshake-less `GET`, 400 for a bad
`Sec-WebSocket-Version`, 501 if the connection cannot be hijacked, 403 for a
cross-origin handshake, and 405 with `Allow` for any other verb. A listener
arriving after the hub has shut down gets close code 1001; a normal shutdown
closes it with 1000.

## Worked example: one unprompted evolution step

A browser has just reported that the agent's pattern threw, and the agent
corrects it without being asked. Every line below is real output captured from a
running server, not an illustration.

```console
$ curl -s localhost:8000/api/code -d '{"code":"s(\"bd*2\").x(3)","message":"first attempt"}'
{"version":1,"code":"s(\"bd*2\").x(3)","lastAgentMessage":"first attempt",...}

# A listener's repl reports the failure back to the server.
$ curl -s localhost:8000/api/eval-result \
    -d '{"version":1,"ok":false,"error":"x is not a function","stats":{"haps":0}}'
{"accepted":true,"version":1}

# The agent reads the verdict back and sees the error. It trusts this, not the
# ack above: a stale report is also accepted:true.
$ curl -s localhost:8000/api/state | jq '.lastEvalResult'
{"version":1,"ok":false,"error":"x is not a function","stats":{"haps":0},"epochMs":...}

# It fixes the document — keeping the shape, dropping the undefined function —
# and says what it did.
$ curl -s localhost:8000/api/code \
    -d '{"code":"s(\"bd*2\").slowcat(\"<x2 x3>\")","message":"dropping the broken layer"}'
{"version":2,"code":"s(\"bd*2\").slowcat(\"<x2 x3>\")","lastAgentMessage":"dropping the broken layer",...}

# The browser confirms, and the loop closes.
$ curl -s localhost:8000/api/eval-result -d '{"version":2,"ok":true,"stats":{"haps":64}}'
{"accepted":true,"version":2}
$ curl -s localhost:8000/api/state | jq '.lastEvalResult.ok'
true
```

Note what did **not** happen: the server never evaluated either pattern, and
neither push produced a verdict on its own. The Go process stores documents and
relays verdicts; the browser is the only thing that runs strudel. That is why
step 4 of the loop is a poll rather than a reply.

A worked listener, from a browser:

```js
const ws = new WebSocket(`ws://${location.host}/ws`);
ws.onmessage = ({ data }) => {
  const { kind, snapshot } = JSON.parse(data);
  replaceState(snapshot);          // wholesale, always — never a merge
  const verdict = snapshot.lastEvalResult;
  if (kind === "eval-result" && verdict && !verdict.ok) {
    console.warn("the agent pushed something that did not run:", verdict.error);
  }
};
```

The `verdict &&` guard is not defensive noise: `lastEvalResult` is `null` until
the first verdict is stored, and a listener that connects mid-performance reads
it in its very first `snapshot` frame.

## See also

* `README.md` — how to build and run it, and the invariants this repo holds.
* `AGENTS.md` — the architecture invariants and how they are proved.

A `cmd/agentcli` wrapping these endpoints is planned (issue
`strudel-agent-3vo.9.1`); until it lands, `curl` is the way in.