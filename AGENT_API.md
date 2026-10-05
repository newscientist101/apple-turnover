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
| `POST /api/dry-run` | [dryRunRequest](#post-apidry-run) | [dryRunVerdict](#post-apidry-run) | no |
| `POST /api/dry-run-result` | [dryRunResultRequest](#post-apidry-run-result) | [evalAck](#post-apidry-run-result) | no |
| `POST /api/sync-result` | [syncResultRequest](#post-apisync-result) | [evalAck](#post-apisync-result) | no |
| `GET /ws` | WebSocket upgrade | JSON listener frames | no |
| `GET /` | none | HTML page | no |
| `/static/` | none | static assets | no |

Every accepted write returns the resulting snapshot except `/api/eval-result`, which returns `{accepted,version}`, and the three browser-report endpoints, which acknowledge rather than return state: the two dry-run endpoints (`/api/dry-run` returns the browser's verdict on a candidate it did not publish; `/api/dry-run-result` returns an acknowledgement because nothing changed) and `/api/sync-result`, whose stored observation is read back from `lastSync` on `GET /api/state`. A successful write does not create a new URL, so responses are `200`, not `201`.

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

### `POST /api/dry-run`

Validate a candidate **without publishing it**. This is the one endpoint that blocks: it returns a verdict somebody else produced, not state it stored.

<!-- shape:dryRunRequest -->
```json
{"code":"s(\"bd*2, ~ cp\")"}
```

- `code` is required and must be non-blank. Unknown fields are rejected.
- The candidate is evaluated by a connected browser and **never becomes the live document**.
- `version`, `history` and `lastEvalResult` are all left exactly as they were. A dry-run is not a revision.
- The verdict is **not stored**. `lastEvalResult` continues to describe the published version; read the dry-run's own response instead.
- With no connected listener this is `409`, immediately — the browser is the only evaluator, so the server answers rather than waiting for an answer that cannot arrive.
- If a listener is connected but does not answer, this is `504`. The candidate was not evaluated and nothing was published.

<!-- shape:dryRunVerdict -->
```json
{"id":1,"ok":false,"error":"Unexpected token '}'","stats":{"haps":0}}
```

- `id` is the dry-run's own correlation id, not a version. Versions identify published documents.
- `stats` obeys the same rule as on `/api/eval-result`: it must be a JSON object, and absent, empty and `null` all mean "no stats".
- `samplesResolved` carries the same tri-state as a stored verdict, for the same reason: a dry-run is how an agent checks a candidate *before* publishing it, so a candidate naming an unloaded sample must be catchable here rather than only after it is live.

A `200` with `ok:false` is a successful request that found a broken candidate. Only `409` and `504` mean the dry-run itself did not happen.

### `POST /api/dry-run-result`

How a browser answers a dry-run. An agent does not call this.

<!-- shape:dryRunResultRequest -->
```json
{"dryRunId":1,"ok":true,"stats":{"haps":4},"samplesResolved":true}
```

Answers `{accepted,version}` like `/api/eval-result`. Every connected listener evaluates and reports, so the **first** answer is the one returned to the agent and later ones are refused with `404`; a report for an id that was never registered, has already been answered, or has expired is also `404`.

`samplesResolved` is the same tri-state as on `/api/eval-result` — `true`, `false`, or absent for UNKNOWN — and the two endpoints resolve identically on purpose. A browser that answered on one path and stayed silent on the other would give an agent two answers to one question.

### `POST /api/sync-result`

How a browser reports where its commit of a version actually landed. An agent does not call this; it **reads** the result from `lastSync`.

<!-- shape:syncResultRequest -->
```json
{"version":7,"driftMs":12,"targetMs":1757000002025,"actualMs":1757000002037}
```

A browser with no usable anchor sends instead:

```json
{"version":7,"unscheduled":true}
```

- `version` is required and must name a published version.
- **Exactly one** of `driftMs` or `unscheduled` is required. Sending both, or neither, is `400`.
- `driftMs` is how many milliseconds late the commit landed against the bar line it targeted. It may legitimately be `0`.
- `targetMs` and `actualMs` are optional context: which bar line the measurement was taken against.
- `epochMs` is **not accepted**. The server stamps receipt time itself, because the browser's clock is exactly what disagrees with the server's and freshness is only comparable against the clock `GET /api/state` is read with.
- It never bumps the version and never enters the history. An observation is evidence about a commit, not a new document.

Answers `{accepted,version}` like `/api/eval-result`. Nothing is stored that the report does not earn: a refused report is not kept and **broadcasts nothing**.

#### Why drift has its own endpoint

It is tempting to fold `driftMs` into the existing `/api/eval-result` `stats`, which needs no new route at all. That does not work, and the reason is ordering rather than taste.

The browser POSTs its eval verdict **immediately after arming** the bar-line timer; the drift is not known until that timer **fires**, which is later. So a verdict carrying its own drift could only be sent twice for one version — and the second POST would overwrite the agent's verdict with a report about a different subject. The alternative, delaying the verdict until the commit lands, stalls the agent's feedback loop behind a bar line.

Drift is a property of a **commit at a bar**; an evaluation is not. They are measured at different instants and have different lifetimes: an observation can arrive after the version it describes has been superseded, and it is stored even then. That is also why an observation naming an **older** version is kept where a stale *verdict* would be discarded.

#### Reading drift

Read `lastSync`. All three of these states are distinct, and the third is the one a naive reader collapses:

| `lastSync` | Meaning |
|---|---|
| `null` | No listener has reported an observation **yet**. Nothing is known about drift — which is not the same as everything being fine. |
| `{"unscheduled":true}` | A browser committed with **no usable anchor**, so it claimed no bar position at all. Alignment was not attempted. |
| `{"driftMs":0}` | A commit landed **exactly** on the bar line it targeted. This is the healthy reading. |

Treat `lastSync:null` as unknown, never as zero. A system that has never reported is indistinguishable from a healthy one if you fold the two, and that is how a silently broken audience keeps looking fine.

**It describes one reporting browser, not the audience.** Every listener measures its own commit against the shared anchor and the server keeps the **most recent** report, so `lastSync` is the answer of whichever browser reported last. Listeners can be in materially different states — one machine's event loop may be 200 ms behind another's — so with more than one listener this is an observation, not a guarantee. Re-anchoring on a large single-listener reading is the documented recovery for drift; with several listeners, treat one browser's number as a floor rather than the spread. Listener **identity** now exists — see [`POST /api/samples`](#post-apisamples) — but per-listener *drift* is still not reported: a listener reports its sample registry, not its bar alignment.

Use `epochMs` on the observation to judge freshness: it is the server's receipt time, so compare it against your own reads rather than against the browser's clock.

### `POST /api/samples`

How one listener reports what its own sample registry holds. An agent does not call this; it **reads** the result from `snapshot.samples`.

<!-- shape:samplesRequest -->
```json
{"listenerId":"L2","loaded":false,"count":0}
```

- `listenerId` is required and must be an id the server issued to a **currently connected** listener. It arrives on that listener's `listener` frame (see [Listener identity](#listener-identity)).
- `loaded` is optional and **tri-state**: `true`, `false`, or **absent** for a browser with no registry to check. See [Audience sample state](#audience-sample-state).
- `count` is optional: how many sounds the browser's registry held at report time. A browser observation, never verified by the server.
- `epochMs` is **not accepted**. The server stamps receipt itself, for the reason it does so everywhere else.

Answers `{accepted,version}` like `/api/eval-result`. It never bumps the version and never enters the history: what a browser can hear is evidence about the audience, not a document.

**A refused report broadcasts nothing.** A report naming an id that never connected, or one whose connection has since closed, is `400` — the server has no such listener and stores no claim about it.

**A report that changes nothing is accepted but silent.** The server compares the report against what that listener last said, and broadcasts a `samples` frame only when something actually changed. A browser re-reporting an unchanged registry is understood and deliberately not published — the same rule an accepted-but-stale eval-result follows, and for the same reason: announcing a no-op would put a frame on the wire every time a browser polled, and a listener's queue is bounded.

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

The browser reports observed drift to `POST /api/sync-result`, and you read it from `lastSync` on `GET /api/state`. Without a usable anchor it reports `unscheduled` rather than inventing a bar position. See [Reading drift](#reading-drift) — in particular, `lastSync:null` means nothing has been observed yet, which is not the same as zero drift.

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
{"version":7,"ok":false,"error":"x is not a function","stats":{"haps":0},"samplesResolved":true}
```

- `version` is required and must name a published version.
- `ok` is the browser's verdict.
- `error` is optional.
- `stats` is optional and must be a **JSON object**; its keys are opaque and echoed back verbatim. Omit it, or send `null`, to report no stats. A scalar or an array is rejected with `400` — the server stores and echoes this field, so a non-object would leave `stats.haps` silently `undefined` for every reader.
- `samplesResolved` is optional and **tri-state**. See [Sample resolution](#sample-resolution).

### Sample resolution

`ok:true` means the pattern **parsed, evaluated and was committed**. It does not mean it will be audible, and the difference is not academic: `s("bd totally_not_a_real_sample_xyz")` evaluates perfectly, names a sample that exists in no pack, and is committed to the live document. Before `samplesResolved` existed that was reported to the agent as unqualified success — the system saying SUCCESS for work that provably cannot work.

`samplesResolved` closes that gap. It is a **tri-state**, and all three states are distinct on the wire:

| Value | Meaning |
|---|---|
| `true` | Every sound the pattern named resolved in the **reporting** browser's registry. |
| `false` | At least one named sound did **not** resolve there. |
| *absent* | **Unknown.** The reporting browser had no sample registry to check, or the evaluation failed before the pattern named a sound. |

Read the absent case as its own answer, not as a quiet `false`. A browser that could not check anything has learned nothing about your samples, and a client that folds "unknown" into either `true` or `false` puts a verdict on a check that never ran — the original defect wearing a fix's clothes. That is why the field is optional and tri-state rather than a boolean: a plain `false` cannot say "I looked and it is missing" while a missing key says "I could not look".

The named sounds, when any are unresolved, are listed in `stats.samples.missing` (sorted and capped; `stats.samples.totalMissing` gives the true count). Those keys are opaque like the rest of `stats` — the server stores and echoes them without interpreting them.

**It describes one browser, not the audience.** Every listener evaluates and reports, and the server stores the most recent report for a version, so `samplesResolved` is the answer of whichever browser reported last. Listeners can be in materially different states — one may have loaded a sample pack while another has not — so with more than one listener the stored value is an observation, not a guarantee. Treat `false` as "at least one listener could not resolve this". The question this field cannot answer — whether *any* listener can hear the pattern — is answered by [`snapshot.samples`](#audience-sample-state), which is per-listener because listeners now have identity.

`ok` and `samplesResolved` are stored independently and neither implies the other: a pattern that parses is not thereby audible, and a missing sample is not a parse failure.

`accepted:true` means the report was understood, not necessarily stored. A valid report older than the stored verdict is accepted but discarded as stale.

### Audience sample state

`samplesResolved` answers a question about **one** browser. It cannot answer "can anybody actually hear this?", which is the question an agent needs before it treats a push as working: with two listeners disagreeing, the stored verdict is whichever tab reported last, and an agent reading `true` from the one tab that had the pack learns nothing about the tab that does not.

`snapshot.samples` is that answer. It exists because a listener now has an identity, so the audience is enumerable rather than an opaque `listenerCount`.

<!-- shape:samplesSummary -->
```json
{"reporting":2,"loaded":1,"listeners":[{"id":"L2","loaded":true,"count":412,"epochMs":1757000000200},{"id":"L3","epochMs":1757000000300}]}
```

| Field | Meaning |
|---|---|
| `samples` | `null` until some listener has reported. **Absent is not zero.** |
| `reporting` | How many connected listeners have reported at least once |
| `loaded` | How many of those reported `loaded:true` |
| `listeners[].id` | The opaque connection id, echoing what that listener was told |
| `listeners[].loaded` | Tri-state, per listener: `true`, `false`, or **absent** for a browser that could not check |
| `listeners[].count` | Sounds in that listener's registry at report time |
| `listeners[].epochMs` | Server receipt time for that listener's report |

Read the cases like this:

- `samples:null` — **nobody has said anything yet.** Not "the audience has no samples."
- `reporting:2, loaded:0` — every listener that answered is missing samples. The push will be inaudible everywhere.
- `reporting:2, loaded:1` — **some** listeners can hear it and some cannot. This is the case a single verdict could never show you, and it is the one this field exists for.
- `listeners[].loaded` **absent** — that listener had no registry to check. It is neither a pass nor a failure, and it is not counted in `loaded`.

`loaded` and `reporting` are counts derived from the enumerated `listeners`, not independent claims: with identity, the audience can be listed, so the counts are arithmetic rather than invention. Prefer reading `listeners` when you want to act — the counts are a convenience for "can anyone hear this at all?".

**A listener that disconnects is forgotten.** Its record leaves the summary when its `/ws` connection ends, so a tab that closed while holding a pack cannot keep answering for the audience. Once the last listener has gone, `samples` returns to `null` rather than reporting an audience of zero — "nobody has reported" and "the audience checked and has no samples" are different facts.

**It describes what listeners reported, not what they will hear.** A listener that has not loaded a pack yet, or that loaded one after its last report, is described by its most recent report; `epochMs` is how you judge whether that report is still current.

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
  "agent":{"active":true,"lastSeenMs":1757000000123},
  "lastSync":null,
  "samples":null
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
| `lastSync` | Newest listener drift observation, or `null` before one is reported. See [Reading drift](#reading-drift) |
| `samples` | Per-listener sample state, or `null` before any listener has reported. See [Audience sample state](#audience-sample-state) |

`anchor.cps` defaults to `0.5` cycles per second. `history` is bounded at 32 versions.

A stored verdict adds the server timestamp:

<!-- shape:storedEvalResult -->
```json
{"version":7,"ok":false,"error":"x is not a function","stats":{"haps":0},"samplesResolved":true,"epochMs":1757000000123}
```

Empty `error` and `stats` fields are omitted. That includes a report that sent `"stats":null`: a `null` means "no stats", so it is dropped rather than echoed back as `null`.

`samplesResolved` is omitted when it is UNKNOWN. The omission is meaningful and is not a formatting accident — see [Sample resolution](#sample-resolution).

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
| `stats` that is not a JSON object | `400` | `stats must be a JSON object, got [1,2]` |
| Wrong method on an API endpoint | `405` + `Allow` | `method GET not allowed on /api/code, use POST` |
| Unknown `/api` endpoint | `404` | `no such API endpoint: GET /api/nope` |
| Dry-run with no listener connected | `409` | `no listeners connected: cannot dry-run` |
| Dry-run nobody answered | `504` | `no listener answered the dry-run in time` |
| Dry-run verdict for an unknown or already-answered id | `404` | `unknown dry-run` |

Every `/api` error response is the single-field JSON error object above. Errors outside `/api` use ordinary `net/http` behavior. Messages name the offending field, limit or version so an agent can branch on them, not only on the status.

## The agent loop

1. `GET /api/state` to read the current version and last verdict.
2. Decide what to change.
3. `POST /api/code` (optionally with `message`).
4. Wait for a browser to submit `POST /api/eval-result`.
5. Read `GET /api/state` and inspect `lastEvalResult`.
6. Iterate.

While doing this, beat on `POST /api/heartbeat` more often than every 15 seconds — including while waiting in step 4, which is the step most likely to outlast the lease. An agent that heartbeats only when it has something to push will be reported absent during exactly the wait it is most idle through.

Step 4 has CLI support: `agentcli wait -version N` polls `GET /api/state` until `lastEvalResult.version >= N`, and `agentcli push --wait` publishes and then waits for the version it published. Both are bounded by the CLI's own `-timeout` and neither can be satisfied by a verdict for an older version, so the sleep-too-little failure — reading the previous version's verdict and concluding a bad push worked — is not something you can write by hand any more. Two things to know about them:

- **With no listener connected they fail fast**, rather than polling to the timeout and reporting "try again later". No browser means no verdict will ever arrive, so the CLI says so on its first read. Check `listenerCount` in the snapshot if you are driving this yourself.
- **They do not heartbeat for you.** There is no `agentcli heartbeat` command, so a wait long enough to outlast the 15-second lease still needs your own `POST /api/heartbeat`. Keep the wait inside the lease, or beat on it from elsewhere.

The push response proves only that the document was stored. It is not an evaluation result. With no connected listener, no verdict will arrive.

### When to re-anchor

Re-anchoring is the documented recovery for drift, and step 5 is where you act on it. Read `lastSync`:

- **`null`** — nothing observed yet. A browser that has not committed tells you nothing; this is not a healthy reading.
- **`unscheduled:true`** — a listener committed with no usable anchor. Re-anchor.
- **a large `driftMs`** — listeners are landing late against the bar grid. `POST /api/anchor` replaces the shared timeline and is what pulls them back onto it.

A small `driftMs` is normal; bar alignment reduces drift, it cannot eliminate it, since there is no cross-machine sample clock. The number that should worry you is one that **grows** across successive pushes, because that is a listener falling behind the grid rather than jittering around it. With several listeners, remember `lastSync` is the newest report from one browser, not the spread — see [Reading drift](#reading-drift).

If you would rather not publish a candidate until it is known to evaluate, `POST /api/dry-run` evaluates it without publishing: step 3 becomes a dry-run, and only a candidate that comes back `ok:true` is worth sending to `/api/code`. It leaves `version`, `history` and `lastEvalResult` untouched, so a broken candidate never becomes the published document. It needs a connected browser just as much as a push does — with none, it is `409` — and it is bounded, returning `504` rather than waiting forever.

Step 5 has one trap: `lastEvalResult` describes whichever version the browser last evaluated, which is not necessarily the version now live. Compare `lastEvalResult.version` against `version` before treating the verdict as an answer about your code. `agentcli state` does that comparison for you and labels the result `CURRENT`, `STALE` or `NONE YET`, naming both versions when they differ; `agentcli state -require-current` turns "not current" into a non-zero exit for a scripted caller.

Step 5 has a second trap, and it is the one that makes a closed loop an optimistic phrase. **`ok:true` means the code parsed, evaluated and was committed. It does not mean it will be audible.** A pattern naming a sample that exists in no pack passes every check this system makes and is committed to the live document; it simply produces no sound. Read `lastEvalResult.samplesResolved` as well — `false` names the sounds that did not resolve, and an absent field means nothing was checked, which is not the same as everything being fine. See [Sample resolution](#sample-resolution) for the tri-state and for why it describes one reporting browser rather than the whole audience. `agentcli state` prints this as a `samples:` row.

The practical shape of a loop that closes on audible output, rather than on syntax: push, read the verdict, and if `ok` is true while `samplesResolved` is `false`, treat the push as **not yet working** — the names are in `stats.samples.missing`. A sample pack is loaded per browser by a page-local control, so an agent cannot load one itself; the useful move is to name the problem in `message` so whoever is at the keyboard can press Samples, or to rewrite the pattern against sounds that are already present.

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
- A dry-run does not increment it either, and adds nothing to the history. A dry-run is not a revision.

## Listener WebSocket

`GET /ws` upgrades to a WebSocket. A new listener receives an immediate full snapshot.

There is exactly one frame shape:

<!-- shape:frame -->
```json
{"kind":"<what happened>","snapshot":{...}}
```

The client replaces its entire local snapshot; it does not merge deltas.

`dry-run` is the only kind that carries a third member, and only because the snapshot cannot express what it has to say:

<!-- shape:dryRunFrame -->
```json
{"kind":"dry-run","snapshot":{...},"dryRun":{"id":1,"code":"s(\"bd*2\")"}}
```

The candidate is here rather than in the snapshot precisely because it was never published: a snapshot is performance state, and a dry-run exists to leave that state untouched. On every other kind `dryRun` is absent, not null.

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
| `dry-run` | A candidate must be evaluated without being published |
| `sync` | A listener reported where its commit landed |
| `listener` | Sent once, to one connection only: the id the server assigned it |
| `samples` | A listener's sample registry changed in a way an observer can see |

Two of these are not transitions of the performance, and both are reasons of the same kind.

`listener` is the only frame ever sent to **one** subscriber rather than the audience. Identity is a fact about the recipient, and a `listener` frame broadcast would tell every browser which listener it is not — after which every report each of them sent would be keyed to the wrong listener. It arrives **first on connect, before the catch-up snapshot**, and that order is load-bearing: the snapshot may carry a code version the browser validates and reports on immediately, so a listener that learned its id afterwards would have to report against an identity it did not have yet. It carries the only other optional frame member, `listener.id`.

`samples` is sent for a **transition**, not for an accepted report. A browser re-reporting a registry that has not changed has told the server nothing new, and publishing that would put a frame on the wire every time one polled — filling listeners' bounded queues and evicting peers on a busy tab. A refused report is not broadcast either, so a `samples` frame always describes state the server actually stored.

Like `sync`, it never bumps the version and never enters the history: what the audience can hear is evidence about the performance, not a change to it.

`hush` and `play` intentionally share `transport` because listeners care about the resulting `playing` state.

`agent` is sent on exactly one occasion: a **held** lease lapsing. A heartbeat broadcasts nothing, and a server that has never seen an agent never announces one going away, because never-seen is the initial state rather than a transition. A client therefore treats `agent` as "the agent you were watching has gone", and re-reads `agent.active` from the frame rather than inferring the timing itself.

`sync` is the opposite direction of information from `anchor`. An `anchor` frame is a command every listener must obey; a `sync` frame is evidence coming back that the shared grid did or did not hold. Do not treat one as the other: re-anchoring because some listener was 12 ms late would be noise, and ignoring a large reading because no anchor changed would lose the only recovery signal there is. A refused sync report broadcasts nothing, so a `sync` frame always describes a measurement the server actually stored.

### Subscribe, then snapshot

A listener is subscribed before its initial snapshot is generated. That ordering prevents a state change from landing in the gap between subscribing and receiving the snapshot.

The new listener's own count change is not sent as a separate `listener-count` event because its snapshot already includes itself.

### Listener identity

Every listener is assigned an opaque id by the server, delivered in a `listener` frame **before** its catch-up snapshot:

<!-- shape:listenerFrame -->
```json
{"kind":"listener","snapshot":{...},"listener":{"id":"L2"}}
```

Rules an agent can rely on:

- Ids are **distinct** across live connections and are **never reused**, so a stored per-listener report always describes the connection that sent it.
- Ids are **opaque**. Echo them back verbatim; never parse them or infer an ordering from them. They name a **socket**, not a browser, tab, user or machine.
- Ids are **process-local**: there is no persistence, so a restart renumbers from the beginning. A report carrying an id from before a restart is refused.
- A report naming a listener that never connected, or whose connection has closed, is `400`. The server does not keep claims about listeners it no longer has.

`listener` is the only frame with an optional member other than `dry-run`, and it has exactly one — a frame carries **at most one** optional member, so `listener` and `dryRun` are absent on every kind they do not belong to, rather than null.

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
