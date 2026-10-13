# Agent Instructions

This file contains the architecture invariants, task-management rules, and verification conventions that keep the system correct.

Keep the three documentation files separate:

| Document | Owns |
|---|---|
| `README.md` | Build, run, deploy, user-facing behavior and limits |
| `AGENT_API.md` | HTTP/WebSocket wire contract, payloads, status codes, frame kinds |
| `AGENTS.md` | Internal invariants, development workflow, testing and proof requirements |

Do not duplicate wire-contract details here when they belong in `AGENT_API.md`.

## Repository map

```text
cmd/srv/            server entrypoint
cmd/agentcli/       pure-Go client of the documented HTTP API
srv/server.go       server lifecycle and page handling
srv/api.go          HTTP API and route table
srv/conductor.go    in-memory performance state
srv/hub.go          listener fan-out
srv/ws.go           WebSocket listener endpoint
srv/event.go        listener frame encoding and broadcast
srv/presence.go     agent liveness lease and the presence sweeper
srv/dryrun.go       dry-run request registry
srv/static/         browser modules
srv/templates/      HTML shell
scripts/mutations.json      the mutation table (JSON: anchors may hold any byte)
scripts/mutation-parse.py    table validator, NUL record emitter, --lint/--selftest
scripts/mutation-check.sh    the grid runner
srv.service         systemd unit
```

There is no database, no browser build step, and no vendored browser dependency. The page loads pinned CodeMirror and `@strudel/web` URLs from CDNs.

## Task tracking: beads (`bd`)

The repository uses beads for shared task state. On a fresh clone, initialize the shared Dolt-backed database before working:

```bash
bd bootstrap
```

Use `bd sync` rather than hand-written pull/push loops:

```bash
bd sync
```

Run it before reading the task list and again after completing work. A clean sync means the sync completed; it does **not** prove that a preceding claim or close succeeded.

Claims are leases, not advisory flags:

```bash
bd update <id> --claim
bd heartbeat <id>
bd unclaim <id>
bd reclaim
```

Verify claims and closes with `bd show <id> --json`, not with the exit code from a later `bd sync`.

Do not use `bd update --force` to replace a live claim. Use `bd reclaim` for expired leases.

For shared replicas, keep the `bd` versions aligned with the repository's database schema. Do not independently migrate a remote-backed database on multiple clones.

Dispatch only leaf subtasks; keep parent issues open until their children are complete.

## Architecture invariants

### Conductor

`Conductor` owns the entire live performance state.

- Thread-safe for concurrent use.
- Holds code, version, anchor, last agent message, bounded code history, playing state, listener count, the agent liveness lease, and the stored evaluation verdict.
- Nothing is persisted.
- Versions are contiguous, unique, monotonic, never reused, and never skipped.
- Only `Publish` / `POST /api/code` increments the version.
- Message, anchor, transport, listener-count, and evaluation-result changes do not increment it.
- `Snapshot` must not expose mutable internal state; history and nested verdict data are copied.
- `RecordEvalResult` must distinguish **stored** from **understood but stale** so only a stored verdict is broadcast.

### Agent presence

Agent liveness is a **lease**, not a connection: the agent speaks plain HTTP and holds no persistent connection.

- Presence is **derived** from `agentLastSeenMS` and the clock, never stored as a boolean. A stored flag needs a timer to unset it, and a timer that misfires leaves the server asserting an agent is present when it is not.
- `agentLastSeenMS == 0` means **never seen** and is distinct from a heartbeat at the epoch.
- The TTL comparison is **inclusive**, and the clock is an **atomic offset** rather than a `func() int64` field, so a test can age a lease while the sweeper is running. Ageing the clock is not the same operation as rewinding the timestamp: real decay leaves `lastSeenMS` untouched.
- Presence **never** increments the version and **never** enters history.
- `AgentPresence` is a value struct on `Snapshot`, not a pointer, so it cannot alias.
- Validation lives in `Conductor`, not only in the HTTP handler.

### Lease sweeper

The sweeper exists only to **push** presence transitions. It must not maintain the flag.

- It broadcasts `agent` for every presence **transition** — never-seen→active (arrival), active→lapsed, lapsed→active (return) — each exactly once; renewals stay silent. `POST /api/heartbeat` itself broadcasts nothing. A never-seen agent is an initial state rather than a transition, so the sweeper never announces one going away (the only announcement a never-seen server ever makes is none at all).
- The announcement keys on the **state**, with the last-announced `lastSeenMS` used only to suppress a repeat. Keying on "a value I have not seen" misses real decay, which does not move the value; keying on "I watched it go stale" misses a lease that is taken and dropped between two ticks. Both orderings must work.
- It starts in `Serve`, not `New`, so configuration between construction and running is not a data race.
- Shutdown is joined, bounded, and idempotent. A stop that signals without joining leaks a goroutine per `Server`.

### Hub

One goroutine owns the subscriber set.

- Broadcasts encoded frames without depending on the Conductor.
- Slow subscribers are dropped rather than blocking the hub.
- Subscriber queues are bounded at 64 messages.
- Subscriber removal is idempotent; channels close exactly once.
- `SubscriberCount` is a synchronous round trip and may block if the hub is wedged, so tests must bound it.
- Hub shutdown and subscriber drop both appear to receivers as channel closure.
- A count hook may create the event frame but must not call back into the Hub from the hub goroutine.

### WebSocket

The listener endpoint must never block on a client.

- Subscribe before generating the initial snapshot.
- Write every frame with a 5-second deadline.
- Unsubscribe on every exit path.
- Handle both hub shutdown and subscriber-channel closure.
- A slow listener is dropped; a transient catch-up write failure does not justify inventing a different state model.
- `/ws` is the exact path and uses the same-origin handshake behavior provided by the WebSocket library.
- Keepalive pings occur every 30s; listeners that cannot respond within 5s are reaped.
- The HTTP server does not impose a shorter `WriteTimeout` that would defeat the WebSocket close/write deadlines.

### HTTP API

- Apply the 64 KiB request cap centrally.
- Detect oversize before JSON parsing so oversized malformed bodies still return `413`.
- Reject unknown fields and trailing JSON data.
- Argument-free endpoints reject any supplied field.
- Empty `code` and `message` are errors, not no-ops.
- Validate first, commit second, broadcast last.
- An argument-free endpoint rejects any supplied field, including `/api/heartbeat`.
- **A field that arrives as `json.RawMessage` is type-checked by hand, in the `Conductor`.** `stats` on `/api/eval-result` is the only such field: its KEYS are opaque (the browser decides what to report and the server never interprets them), but the value must be a JSON **object**, and nothing but an explicit check enforces that — `json.RawMessage` accepts any JSON value, so `stats:123` would otherwise be stored and echoed back, leaving every future reader of `stats.haps` with a silent `undefined`. Opacity is about the contents, not the shape. Rejecting once at the boundary beats defending in every reader (strudel-agent-uvj.19).
- **"No stats" has three spellings — absent, empty, and JSON `null` — and all three must store `nil`.** The `null` case is the trap: `json.RawMessage` implements `json.Unmarshaler`, so a `null` does *not* leave the field nil, it becomes the four bytes `"null"`, which a zero-length `omitempty` does not drop. Storing that verbatim emits `"stats":null` from a verdict that reported no stats, contradicting the API's "empty fields are omitted".
- Validation order within one body is body-then-identity: a report wrong in two ways is diagnosed by the check the sender can act on, so `stats` is validated before the version.
- Broadcast the same snapshot that the HTTP response returns.
- Keep `/api` errors uniformly JSON.
- Keep route handling centralized in `routes()`; tests should exercise that real tree.

### Event frames

Every listener event has this shape:

```json
{"kind":"<what happened>","snapshot":{...}}
```

- `snapshot` is the actual snapshot value, not a second bespoke representation.
- All frame construction goes through one encoder.
- The event vocabulary is `snapshot`, `code`, `message`, `transport`, `eval-result`, `listener-count`, `anchor`, `agent`, `dry-run`, `sync`, `samples`, `listener`. The list is a claim about `event.go`, so it is checked against the DECLARED kinds rather than kept by hand: `TestAgentAPIDocCoversEveryEventKind` parses them out of `event.go`. This line previously named nine of the twelve while reading as exhaustive, which is the failure mode this whole section is about.
- **AT MOST ONE OPTIONAL MEMBER, PER KIND.** `dry-run` frames carry a fourth field, `dryRun` (`{id, code}`), because that frame asks a listener to evaluate a candidate that was **not** published, and the snapshot cannot say so: it is performance state, and the candidate is deliberately not state. `listener` frames carry `listener` (`{id}`), because identity is a fact about the *recipient* and a snapshot describes the performance. Every kind omits both fields entirely — `omitempty` on a nil pointer emits nothing, so the frames a client already knew stay byte-identical. A second bespoke frame *shape* would be worse than this, because it would break the single decode path; a new field on the existing envelope does not. The rule is per-KIND rather than "one member in the whole protocol" so that a second need can be met by its own kind without ever giving a frame two optional members — see [Listener identity](#listener-identity-and-audience-state).
- Event names describe listener-visible changes, not endpoint names; therefore play and hush both use `transport`.
- A frame is sent for a **transition**, not for an accepted write that changed nothing observable. A heartbeat that merely renews a held lease is accepted and silent; a heartbeat that establishes or revives presence is announced by the lease sweeper on its next tick, as is every lease lapse — each transition exactly once.
- `dry-run` is the one kind that is a **request** rather than a transition, which is also why its `snapshot` does not describe what the frame is about. It never bumps the version and never enters history.

### Anchor and coherence

- An anchor replaces the entire timeline.
- Both `epochMs` and `cps` are required.
- Validation belongs in `Conductor`, not only in the HTTP handler.
- `cps` accepts only finite positive values up to 1000.
- Epoch skew is checked against a supplied reference clock so boundary tests are deterministic.
- A rejected anchor must leave the existing anchor unchanged and broadcast nothing.
- Client commits are bar-aligned, not sample-accurate.
- A newer code version must cancel an older pending commit.
- With no usable anchor, the client commits immediately and marks the state `unscheduled`.
- Synchronization behavior is proved by executing the served browser JavaScript, not by duplicating its arithmetic in Go tests.

### Drift reporting

Drift is measured in the browser, so drift reporting is proved in the browser.
The defect this section exists for (strudel-agent-uvj.15) is the purest example
in the repository of a **claimed but unverified** capability: sync.js computed
drift, session.js painted it into `#sync-status`, and then it was dropped. The
only outbound call in any browser asset was the eval-result POST, so
`README`'s "residual drift is visible" and `AGENT_API.md`'s "the browser reports
observed drift" were true only to a human watching the tab — and an agent, the
consumer both documents address, could not observe any of it and so could not
know when re-anchoring was needed.

- **THE ASSERTION IS ON THE WIRE, NOT ON THE RENDERER.** A Go test that checks
  the server accepts a `driftMs` field passes against exactly the broken code:
  the field validates, stores and echoes while the browser still sends nothing.
  `srv/sync_browser_test.go` drives the served bytes in goja and asserts on the
  body that would have been POSTed. This is the same rule as the agent badge,
  and it exists because the original drift tests all stopped at the display.
- **AN OBSERVATION IS NOT A VERDICT.** Drift has its own endpoint rather than a
  field in the eval-result `stats`, and the reason is **call order**, not taste:
  `applyVersion` arms the boundary timer and *then* POSTs the verdict, so at that
  instant the drift does not exist yet. Folding it in would mean posting one
  version twice — with the second POST overwriting the agent's verdict — or
  stalling the feedback loop behind a bar line.
- **UNSCHEDULED IS NOT `driftMs:0`.** One says the commit landed on the bar line
  it targeted; the other says there was no bar line at all. Collapsing them makes
  a listener that never aligned look perfectly aligned, which stops an agent
  re-anchoring a system that needs it. The server refuses a report carrying
  **exactly one** of the two claims; both or neither is `400`.
- **`lastSync:null` IS NOT ZERO DRIFT.** "Nobody has measured" and "measured as
  perfect" are different facts, so the field is a `*SyncObservation` with
  `driftMs` itself a `*int64`. Folding the absent case into a zero is how a
  silent system keeps looking healthy.
- **SYNC DATA NEVER BUMPS THE VERSION** and never enters history. It is evidence
  about a commit that already happened; storing it as a document would put a
  second entry for one push in the history.
- **THE NEWEST ARRIVAL WINS, INCLUDING AN OLDER VERSION** — the deliberate
  asymmetry with `RecordEvalResult`, which discards a stale *verdict*. A verdict
  is about the document; an observation is about *when* a commit landed, and a
  listener that committed version 1 late can report it after version 2 is
  published. Discarding it would drop the evidence the feature exists to deliver.
- **`lastSync` DESCRIBES ONE BROWSER, NOT THE AUDIENCE.** The newest report wins
  and listeners have no identity, so this is an observation rather than a
  guarantee. The wording discipline is the one used for `samplesResolved`
  (strudel-agent-uvj.18); per-listener drift belongs with `strudel-agent-f79`.
  No opaque count is invented, because nothing here could earn one.
- **A REFUSED REPORT BROADCASTS NOTHING.** A `sync` frame therefore always
  describes a measurement the server stored, and it is the opposite direction of
  information from an `anchor` frame: one is a command, the other is evidence.

### Dry-run

`POST /api/dry-run` evaluates a candidate **without publishing it**, so a broken
pattern never becomes the current document. It is the one endpoint that blocks:
the server never evaluates JavaScript, so it must ask a connected browser and
wait for the answer.

- **Dry-run state lives on the `Server`, never in the `Conductor`.** The version,
  the history and the stored verdict must all survive one untouched, and the
  cheapest way to keep that promise is for the code path that could break it to
  have no way to reach them. Nothing in either dry-run handler calls into the
  `Conductor`; a dry-run verdict is **never** stored, because a verdict about
  code that is not live is indistinguishable from a real one when read back.
- **The correlation id is not a version.** Versions identify published
  documents. Ids are monotonic and never reused, so a late report cannot be
  mistaken for a live request.
- **First answer wins, and the losers are refused.** Every listener evaluates and
  reports, so a second report for the same id is `404` rather than an overwrite.
- **Every exit path removes the registry entry** — answered, timed out, or client
  gone. A registry that leaked one key per abandoned dry-run would leak one per
  agent loop iteration.
- **No listener is `409` immediately**, not a wait: the server already knows
  there is no evaluator, so blocking would spend the agent's budget to report
  "unknown" for a condition it can see at once. An unanswered listener is `504`,
  bounded by a server-side timeout as well as the caller's context.
- **The browser evaluates in the sandbox only.** `dryRunCandidate` must never
  call `live.setPattern`, never touch the editor, and never advance
  `lastVersion` — a dry-run frame that advanced it would make the next real code
  frame look stale and be skipped. The audio commit is the one thing that must
  not happen, and the server cannot catch it because the server never sees it.

### Deployment unit

`srv.service` is the only artifact no Go test executes, so a typo in it produces a service that never starts while the suite stays green. `srv/unit_test.go` closes that gap statically, without needing systemd, root, or the production port.

- `ExecStart` must be the single absolute path `make build` writes (`srv/srv`, because `srv/` is a package directory). A change to the build output path must update the unit in the same commit.
- `WorkingDirectory` must be the checkout root, since the server resolves `srv/templates` and `srv/static` relative to it.
- The unit must name a non-root `User`, send stdout/stderr to `journal`, and set `HOME`/`USER` explicitly, because a systemd service inherits neither from a login session.
- `Restart`/`RestartSec` must be present and `RestartSec` positive; `WantedBy=multi-user.target`.
- `make start`/`stop`/`restart` must call `sudo systemctl <verb> srv` and must not contain `-`, `|| true` or `@`, which would turn a failed `systemctl` into a make success.
- Live host behaviour is evidence, not inference: `systemctl start` can exit `0` while the unit is failing with `203/EXEC`, so a deployment is only proven by `is-active`, the journal, and a request against `/api/state`. The verified run is recorded in README.md.
- `systemd-analyze verify` is opportunistic: the static assertions above are the portable guarantee, and the check skips when systemd is absent.

### Page and browser assets

- `GET /` must render the complete shell, not merely a status-200 prefix.
- Serve the actual browser modules that the page references.
- The browser is the only evaluator and audio producer.
- `editor.js` may fall back to the plain textarea if CodeMirror is unavailable; that does not imply audio can work offline.
- **A listener boots playing; mute is local and sticky.** The client boots `desiredPlaying=true` and applies the boot start when the repl is READY — readiness meaning the first code commit has landed a pattern, not merely that `strudelInitPromise` resolved (starting earlier is silently ineffective: the pinned bundle's scheduler rejects a start with no pattern ever committed). A one-shot `bootStartPending` flag carries the intent from boot to that first successful commit, which re-applies effective audio once and clears it; validation failures never consume it, and muting during load keeps the tab silent when the pattern lands. `playing` remains shared transport intent, not a per-browser audio command: `snapshot`/`code` frames never drive audio, `setPattern` commits with autostart disabled so new code never force-starts a muted listener, and a `transport` frame records `desiredPlaying` and re-applies effective audio (`desiredPlaying && !localMuted`) without ever clearing `localMuted`. The Mute button toggles per-browser `localMuted` only: it drives `liveRepl.stop()/start()` directly, never writes `desiredPlaying`, and never fetches `/api/hush` or `/api/play`. When starting audio, the AudioContext is resumed if suspended (the Tier 1 attempt: a browser whose policy ALLOWS web audio never needs a gesture).
- **A browser that blocks autoplay is unlocked by the FIRST gesture of ANY kind, not by a Mute/Unmute cycle.** This is the fix for the defect the first two attempts at strudel-agent-3q0.6 shared: each treated a blocked browser as one whose `resume()` never resolves (it pends only until the first user gesture) or whose only gesture is the Mute button (any real input event grants it). `armGestureUnlock`, armed at boot before the boot start fires, registers `pointerdown`/`keydown`/`touchend` on the document (capture, passive) and resumes the still-suspended context on the first one. The unlock is CONTEXT-level and never TRANSPORT-level: it never calls start/stop, never writes `desiredPlaying` or `localMuted`, and never fetches a transport endpoint — by the time a gesture can arrive the scheduler state is already right, so the only thing missing is the context. A gesture may arrive before or after the first commit; both orders end audible with ONE gesture. ONE resolver (`audioContextOf`) feeds the Tier 1 resume, the unlock, and the probe, so the three can never disagree about which context they mean. `autoplayProbe` rides `collectStats` so every eval AND dry-run verdict carries `stats.autoplay` (`ctx` = the context state or `"none"`, `act` = sticky user activation) — the assertion is on the wire, not on the renderer: a browser-side audio state the agent cannot read from `/api/state` is the uvj.15 failure mode applied to audio, and it is how the earlier claim "autoplay policy does not keep the tab silent" went unverified through two fixes.
- The visualizer is pattern-driven only: it paints hap lanes and a playhead
  from `currentPattern.queryArc()`, with no audio-context dependency. A
  waveform branch was removed because the pinned bundle exposes no reachable,
  connected analyser path for it (strudel-agent-uvj.2), and an unwired scope
  would have reported sound that was never observed. The lane SET describes
  the pattern, not the window: a bounded discovery query (`queryArc(0,
  LANE_DISCOVERY_CYCLES)`, anchored at cycle 0 so rows never depend on when
  you look) fixes the rows, while the 2-cycle window still decides what paints
  where — a lane with no window hap keeps its row (label + separator, no
  block). A pattern whose lanes spread over more than LANE_DISCOVERY_CYCLES
  still misses rows (documented limitation, strudel-agent-voo.1).
- Lane identity and lane colour come from **one** resolver (`laneFor`), so they
  cannot disagree. Precedence is **explicit**, not first-match: pitch
  (`note`, then `n` — one control with two spellings in the pinned bundle) wins,
  then the sound (`s`), then `other`. A hap carrying both pitch and sound is a
  lane per **pitch**; keying it on `s` collapses a whole pattern onto one lane
  named after a shared instrument (strudel-agent-uvj.10). Deriving the colour by
  re-inspecting the hap is the defect, so `colourFor` takes the resolved kind.
- **Lane colour is per-lane, and it is a HASH OF THE LANE KEY.** `colourFor(key,
  kind)` picks from a per-family palette by `hashKey(key) % palette.length`, never
  by lane index. `laneKeys.sort()` runs every frame (viz.js), so an index-based
  colour would reassign itself whenever the lane count or ordering changed; the
  flicker that would cause is **inferred from the code, not observed** — nobody
  has watched that alternative render, because it is the implementation this one
  replaced. The hash is what makes colour a function of the lane rather than of
  its position, and `TestVisualizationPerLanePalette` asserts stability across
  re-renders and sort-order changes (strudel-agent-uvj.9).
- **The three family palettes are DISJOINT, and that is load-bearing.** It is
  what lets a lane's family be asserted by *membership* — "the colour painted is
  one of the note palette's entries". Hue cannot do this job: the `other` family
  is grey-violet by design and most of its entries sit inside the note family's
  blue band, so a hue-banded check accepts an `other` colour as a note colour.
  That is the uvj.10 defect re-entering through the palette instead of through
  the resolver, and a hue-banded version of the identity test let mutation
  `130-viz-lane-colour-decoupled-from-lane` survive. Assert membership, and keep
  the disjointness test that makes it sound.
- **The sounding-token highlight runs on the anchor-derived cycle, never `scheduler.now()`.** Every viz frame derives the active spans from the SAME `currentCycle` and SAME `haps` array as the lanes and playhead (`activeSpans` in viz.js → `setActiveSpans` in editor.js), so the three can never disagree across listeners. `editor.js` owns the only CodeMirror mark touchpoint; marks are recomputed per frame and carry no cross-frame state; the textarea path is a documented no-op; and the feature adds no browser dependency and no wire frame kind (AGENT_API.md untouched).
- **A new version lands as a simulated human edit, and the animation is view-only.** When a code frame changes the displayed document, the cursor moves to the edit site and the old text is deleted and the new text inserted in bounded steps (delete phase first, then insert), rather than flashing the whole document at once. The animation never touches what the agent reads back: the verdict is posted at frame arrival, not at convergence (`setCode` returns synchronously before validation starts); the bar-aligned commit and the 700ms flash timer are unchanged. The displayed document always ends EXACTLY at the latest version's code; a newer version supersedes a running animation (single-flight retarget re-derived from the live buffer — at most one pending script, at most one timer outstanding — never a queue of stale scripts). The total budget is clamped: at most 30 steps of 117ms (3510ms wall-clock), one delete/insert character per step, and a change too large to fit jumps instantly with zero animation timers. The caret stays visible while animating without stealing focus: init requests a steady caret via setOption('cursorBlinkRate', 0) when supported, and a 'strudel-animating' marker class on the CodeMirror wrapper (added at animation start, removed at convergence on every landing path) keeps .CodeMirror-cursor visible under a stylesheet rule scoped to that marker. With CodeMirror unavailable the plain-textarea path is an instant-swap no-op (0 timers armed, behaviour byte-identical to before). While animating, the sounding-token highlight is SUPPRESSED, not remapped: `setActiveSpans` stores its argument in a one-slot pending buffer (newer overwrites; an empty set still clears it) and paints nothing, then re-applies the buffered set against the final document at convergence — so every rendered mark is document-true at all times and no mark ever lands on text that does not exist yet. The suppression window is bounded by the same 3510ms budget.
- **The live pattern must carry document-true locations; the sandbox must not silently substitute a different coordinate frame (strudel-agent-pnn.5).** `buildSandbox` constructs the validating repl WITH `transpiler:strudel.transpiler` — the same transpiler the live repl was built with — because in the pinned @strudel/web@1.3.0 bundle only the transpiled evaluation path reports document-relative hap locations and populates `state.miniLocations`; the raw path reports offsets relative to the extracted mini string (for `s("bd sd hh cp")` haps carry [[1,3],...] while the document spans are [[3,5],...]). The sandbox-validated pattern is hot-swapped straight into the live repl, so a raw-path pattern hands viz.js sandbox-relative offsets to mark against the document. A bundle shape without a transpiler still builds but warns; `srv/spanframe_browser_test.go` proves the frame by asserting SPANS ([3,5) for `s("bd")`, not merely "a mark appeared") through the served session.js + editor.js + viz.js.

### Sample resolution

`ok:true` means a pattern PARSED, EVALUATED and was COMMITTED. It has never meant
"it will be audible", and strudel-agent-uvj.18 is the bead that made the gap
measurable: a sample name in no pack validated cleanly and was reported as
unqualified success.

- **Resolution is a REGISTRY LOOKUP, never playback.** The validating sandbox
  stubs `defaultOutput` and pins `getTime`, which is what keeps validation free of
  audio, of the scheduler and of the network. Sample resolution in Strudel happens
  on the OUTPUT path, so that stub hides it — by design. The fix reads
  `window.strudel.soundMap`, which is the very map playback consults
  (`soundMap.get()[name.toLowerCase()]`, yielding nothing for a miss). Removing the
  `defaultOutput` stub is NOT an option: it would make every validation start a
  scheduler and fetch over the network, trading a false positive for something
  worse.
- **The normalisation is copied from the bundle, not reimplemented.** Verified
  against the pinned `@strudel/web@1.3.0`: `registerSound` applies
  `toLowerCase().replace(/\s+/g, "_")` before storing. A resolver that normalised
  differently would disagree with the audio the user hears, which is the one thing
  it must never do.
- **The registry is the source of truth, not the page label.** `welcome.html`
  has no unload control (the old toggle's off branch flipped a label and
  unregistered nothing), so a flag-driven resolver would keep reporting a loaded
  pack no page label could remove.
- **ONE resolver feeds both paths.** The eval-result and dry-run reports are two
  ways an agent asks the same question; a resolver that answered on one and stayed
  silent on the other would give an agent two answers to one question.
- **`samplesResolved` is TRI-STATE, and absence is not success.** `true`, `false`,
  and ABSENT (UNKNOWN) are three distinct facts on the wire, so the field is a
  `*bool` with `omitempty` on both `EvalResult` and `DryRunVerdict`. A resolver
  that could not look — no bundle, no registry — must report UNKNOWN. Reporting
  `true` there would be the original defect wearing a fix's clothes: a green light
  over a check that never ran.
- **A parse failure NEVER carries a sample finding.** A syntax error proves nothing
  about samples; the pattern never got far enough to name one. Attaching
  `samplesResolved:false` there would invent a defect the agent did not commit, on
  top of an error that already says what is wrong.
- **Stored verdicts deep-copy the tri-state**, as they do `stats`. `Snapshot` hands
  out a copy; a caller that wrote through the pointer would corrupt what the next
  reader sees, and the write would succeed, so nothing would report it.
- **It describes ONE reporting browser, not the audience.** Listeners can be in
  materially different states and the server stores the most recent report, so the
  value is an observation. That limit is now ADDRESSED rather than documented:
  `snapshot.samples` answers the audience question per listener, because listeners
  have identity — see [Listener identity](#listener-identity-and-audience-state).
  The verdict-scoped field keeps its own single-browser meaning, and the two are
  separate because they answer separate questions.
- Resolution lives in the browser, so it is proved by executing the SERVED
  JavaScript (goja), never by translating the algorithm into Go.

### Listener identity and audience state

`strudel-agent-f79` is the remainder of `uvj.18`: `samplesResolved` is
verdict-scoped, and with listeners anonymous the server could not dedupe them,
forget a departed one, or say which answered. This section owns identity itself,
because listeners will need it for things beyond samples.

- **THE HUB MINTS IDS, AND ONLY THE HUB.** It is the one owner of the subscriber
  set, so it is the only place a connection exists. `Subscriber.ID` is assigned on
  the subscribe path, before the handle is returned.
- **AN ID IS NEVER REUSED.** This is the property the whole feature rests on: a
  recycled id would make a report from a closed tab indistinguishable from one by
  whatever connection inherited its number, which is the exact confusion identity
  was added to remove. `TestSubscriberIDsAreNeverReused` is the guard.
- **AN ID NAMES A SOCKET, NOT A BROWSER.** Not a tab, a user, or a machine. Ids are
  process-local and renumber after a restart. Every wording here and in the docs is
  "this listener" for that reason.
- **IDENTITY ARRIVES BEFORE THE CATCH-UP SNAPSHOT, and that order is load-bearing.**
  The snapshot may carry a version the browser validates and reports on
  immediately; a listener that learned its id afterwards would have to report
  against an identity it did not have, and the server — correctly — refuses it.
  `listener` is also the only frame delivered to ONE subscriber, via `Hub.Direct`,
  which never blocks and answers `ErrNoSubscriber` rather than writing to a closed
  channel.
- **A REPORT NAMES A LIVE CONNECTION OR IT IS REFUSED.** `knownListeners` is the
  authority, and `ErrUnknownListener` is what a stale or invented id gets. A server
  that stored it would hold claims about an audience it cannot enumerate.
- **DEPARTURE IS FORGOTTEN, AND THE DEFER ORDER IS LOAD-BEARING.** The forget is
  registered *after* `defer s.Hub.Unsubscribe(sub)` so it runs *first* — otherwise
  the `listener-count` frame the hub publishes on removal would encode a snapshot
  still listing the listener that just left, describing a state that never existed.
- **THE TRI-STATE IS PER LISTENER, NOT PER AUDIENCE.** `ListenerSamples.Loaded` is
  a `*bool` for the reason `EvalResult.SamplesResolved` is: a listener with no
  registry learned nothing, and folding that into `false` would invent a defect
  nobody found. `agentcli state` counts only listeners that CHECKED for its
  headline fraction — a fraction over `reporting` would report an un-checked
  listener's silence as a missing-sample finding.
- **`samples:null` IS NOT AN EMPTY AUDIENCE.** "Nobody has reported" and "the
  audience checked and has no samples" are different facts, so the summary is a
  pointer and goes absent when the last listener leaves.
- **THE COUNTS ARE DERIVED, NOT INVENTED.** `reporting` and `loaded` are arithmetic
  over the enumerated `listeners`. This is the opaque-number case the drift section
  above declined to publish; it is publishable *now* only because identity makes the
  audience enumerable, and it would be a defect again if the counts were computed
  any other way.
- **THE BROWSDRIVER IS PROVED ON THE WIRE.** `srv/samples_audience_browser_test.go`
  asserts on the body that would have been POSTed, never on what `describeSamples`
  returned — the `uvj.15` lesson, and it is why the report is exercised through the
  real `handleFrame` seam.
- **THE RESOLVER IS THE REGISTRY, NEVER THE PAGE LABEL.** `welcome.html`
  auto-loads the pack once at startup and offers no unload control, so the
  client polls `soundMap.get()` on the status tick and reports only on a real
  change. The server ignores an unchanged report regardless, so the frame rule
  holds even if a client polls carelessly.
- **THE LOADER IS welcome.html AND IT IS PROVED ON THE SERVED BYTES.**
  `loadSamplePack()` in the inline script is the only load path, invoked once
  from the merged `strudelInitPromise.then` handler; `session.js` stays
  poll-and-observe and is untouched. Behaviour is proved by
  `srv/samples_autoload_browser_test.go`, which executes the served `/`
  template bytes in goja — never a Go translation. Element ids
  `samples-error` / `samples-retry` are the contract (there is no status
  pill): success is silent, the error element is empty while loading and
  carries the pinned failure text after failure, and retry is hidden until
  failure. Mute behaviour alongside it is proved by
  `srv/session_mute_browser_test.go`, which executes the served
  `session.js` bytes in goja.

## Standing limitations

These are design properties, not bugs:

- **No server audio.** The Go process never evaluates JavaScript or produces sound.
- **Browser-only evaluation.** A server-accepted pattern can still fail in Strudel.
- **Bar-aligned synchronization only.** There is no cross-machine sample clock.
- **Drift is observable, not eliminated.** The client reports measured drift and `unscheduled` when the anchor cannot be used, to `POST /api/sync-result`; an agent reads it from `snapshot.lastSync`, where `null` means nothing has been observed yet rather than zero. One reading describes one reporting browser, not the whole audience — see [Drift reporting](#drift-reporting).
- **Per-listener sample state is observable.** Each listener reports its own registry to `POST /api/samples`, keyed by the connection id the server assigned it; an agent reads the enumerated audience from `snapshot.samples`, where `null` means nobody has reported yet rather than zero. It is what listeners reported, not a prediction of what they will hear — see [Listener identity](#listener-identity-and-audience-state).
- **Autoplay is subject to the platform's policy, and the page meets it halfway.** A browser that blocks autoplay cannot become audible without its first user gesture — that is the browser's rule, not a defect the page can remove — but the page guarantees the gesture can be ANY interaction with the page (click, keypress, tap), never a Mute/Unmute cycle, and a browser whose policy allows web audio (or a listener the user already unlocked, or an origin the user allowlisted for sound) needs no gesture at all. The state the browser was in at evaluation time is on the wire (`stats.autoplay`), so an agent is never left guessing why `ok:true` is silent.
- **No persistence.** Restart returns to version 0. Listener ids are process-local and renumber, which is why a report carrying a pre-restart id is refused.
- **Network required at browser load.** Pinned CDN dependencies are not vendored.

Any change to these limitations changes the API/README contract and must be documented with the implementation.

## Verification gate

The standard gate is:

```bash
make verify
```

Equivalent steps:

```bash
gofmt -l .
go vet ./...
go build ./...
go test ./... -race -count=1
```

Run this before committing completed work.

Tests must not depend on:

- external network access;
- databases;
- the production port (`:8000`);
- already-running services.

Use `httptest` and loopback instead.

### Core test locations

| Area | Primary tests |
|---|---|
| API/integration | `srv/integration_test.go` |
| Dry-run (server) | `srv/dryrun_test.go` |
| Dry-run (browser) | `srv/dryrun_browser_test.go` |
| WebSocket | `srv/ws_test.go` |
| Hub | `srv/hub_test.go` |
| API handlers | `srv/api_test.go` |
| Conductor/state | `srv/conductor_test.go`, `srv/conductor_eval_test.go`, `srv/conductor_presence_test.go` |
| Agent presence/lease | `srv/presence_test.go` |
| Browser timing | `srv/coherence_test.go` |
| API documentation | `srv/agent_api_doc_test.go` |
| Browser wiring | `srv/session_client_test.go`, `srv/editor_view_test.go`, `srv/viz_test.go`, `srv/agent_indicator_test.go` |
| Deployment unit | `srv/unit_test.go` |
| CLI | `cmd/agentcli/main_test.go` |

Assert response **bodies**, not just status codes.

Prefer tests that drive the real route tree and served bytes over tests that reproduce the implementation in the test harness.

### Agent CLI exit codes

The CLI is a documented agent interface, so its exit codes are a contract:

- `0` success, including a **help request** at both the top level and per
  subcommand.
- `1` the server refused, could not be reached, or did not return what the caller
  asked for. This now covers four distinct situations, all of which share the
  property that **the command line was valid and the request was answered**:
  `-require-current` refusing a stale verdict, `eval-result -force` refusing a
  destructive overwrite, `push --dry-run` reporting a failing candidate, and
  `wait` timing out. None of them is a usage mistake, so none may exit `2`.
- `2` the command line was wrong; nothing was sent.

Help is a successful request, not a usage mistake. Each subcommand parses its own
`FlagSet` with output discarded so that `run()` owns every message and code, which
also suppresses Go's automatic usage printing — so each subcommand installs its own
usage function and maps `flag.ErrHelp` to a sentinel that `report()` turns into
`exitOK`. Folding that sentinel into `usageError` re-creates the defect: the flags
become undiscoverable and a valid request is mislabelled as a mistake. Every other
parse error must remain a `usageError`.

The flagless subcommands (`message`, `hush`, `play`) answer help only when
their args are **exactly** the help flag; their arguments are otherwise meaningful
(`message "-h"` is narration), so a wider check would swallow real usage errors.
`state` is not on that list any more: it grew `-require-current`, so it owns a
`FlagSet` and its flags must stay documented by `state -h`.

### Verdict currency in the CLI

The stored verdict is the only feedback an agent gets about whether its code
worked, so `state` must never let a lagging verdict read as a current one.

- `verdictIsStale` is the **single** comparison defining staleness, shared by
  `classifyVerdict` and by `eval-result`'s accepted-but-ignored report. Neither
  command may grow its own, or the two can call the same version current in one
  and stale in the other.
- The currency is stated in words, naming **both** versions when stale. A row that
  names only the current version leaves the reader doing the comparison by eye,
  which is the defect (strudel-agent-uvj.14).
- `NONE YET` is not success and must not print an `ok` flag. There is no verdict,
  so there is no result.
- The comparison is never assumed to hold: a verdict naming a version the
  snapshot lacks is reported, not folded into "current".
- Existing prose output is **added to**, never reworded or removed. `last eval:`
  must survive verbatim, and `-json` must remain the untouched snapshot.
- A plain read exits `0` on a stale verdict — the read succeeded, and a lagging
  verdict is the normal state while an agent waits for a browser. Only
  `-require-current` turns it into a non-zero, and that error must not be a
  `usageError`: the command line was valid and the request was answered.

### Overwriting a verdict in the CLI

`eval-result` is the one command that **destroys** rather than adds: reporting a
version that already has a stored verdict replaces the browser's real one, and
that verdict is the only feedback signal the agent loop has. During live testing
an invented report silently replaced a genuine one while "testing the endpoint"
(strudel-agent-uvj.17).

- Overwriting an **existing** stored verdict for the same version requires
  `-force`. Without it the CLI refuses and **sends nothing**.
- A **first** verdict for a version with none stored needs no ceremony. A guard
  that flagged the ordinary case would be a flag everybody pastes everywhere,
  which is a flag nobody reads.
- `-force` is permission to **replace**, never to **regress**. It must not
  suppress the accepted-but-ignored report for an *older* version; that path is
  the shared `verdictIsStale` comparison and is unchanged.
- A permitted overwrite **says so** in its output. A forced overwrite printing
  the same line as a first verdict leaves the caller unable to tell that the
  browser's verdict is no longer the stored one.
- The refusal is **not** a `usageError` and must not exit `2`. The command line
  was valid and nothing was sent; it exits `1`, alongside `-require-current`.
- The guard is **CLI-surface only**. The server is unchanged, the endpoint still
  accepts the same report, and the one-command-to-one-endpoint mapping asserted
  in `cmd/agentcli/main.go` still holds. No `AGENT_API.md` change follows from
  it — the wire contract did not move.
- Detection rides on the pre-read this command already performs, so it costs no
  extra request. It is an **observation**, not a guarantee: another reporter
  could store a verdict between the read and the write, which is the same
  wording discipline `anchor`'s comparison follows.

### Half-anchoring in the CLI

`agentcli anchor` exists because `POST /api/anchor` REPLACES the whole timeline:
both fields are required, so a hand-written body naming only `cps` is a `400`. The
CLI reads state first and carries the unsupplied half forward, which is correct
and is exactly what `AGENT_API.md` tells an agent to do by hand. strudel-agent-uvj.11
is what that convenience ran into.

- **THE SERVER'S FIVE-MINUTE WINDOW IS AN INVARIANT AND IS NEVER RELAXED FOR A
  CLIENT.** It is what stops a stale client silently resetting the shared
  timeline. The server publishing an anchor it will later refuse is *intended*
  behaviour: the epoch is stamped at start and does not advance. A fix that
  widens the window, skips validation for a known client, or treats the stored
  anchor as grandfathered has fixed the wrong side — the CLI is what is wrong.
- **A CARRIED EPOCH IS REUSED ONLY WHILE IT IS STILL INSIDE THE WINDOW.** Past it,
  the carried value is one the server has already promised to refuse, so
  re-sending it is guaranteed failure rather than an edge case. Given the epoch
  does not advance, this is the *steady state* of any session over five minutes,
  not a corner — which is why the invariant is about the whole session rather
  than about a race.
- **WHAT "LEAVE THE EPOCH ALONE" MEANS IS THE SERVER'S CLOCK, AND IT IS
  DISCLOSED.** A stored epoch outside the window cannot be preserved, because
  preserving it is precisely what the guard forbids. It is replaced with the
  server's current time: the only value that both satisfies the guard and means
  "the grid starts now". This does move the shared bar grid, so it is **announced
  on stderr naming both epochs** — never silently. An agent that discovered its
  grid had shifted by an unmentioned substitution would be worse off than one
  that got a `400`.
- **THE SERVER'S CLOCK COMES FROM THE `Date` RESPONSE HEADER, NEVER FROM
  `time.Now()`.** The CLI does not know the server's time, and its own clock is
  the one thing guaranteed to disagree. `Date` is set by `net/http` on every
  response whether or not the application knows about it, so learning the clock
  adds **nothing to the wire contract** — no endpoint, no snapshot field, nothing
  for an agent to read. A body field would have moved `AGENT_API.md` for a purely
  local need. Absent or unparseable, it falls back to the local clock, which is
  the pre-existing behaviour and so cannot be a downgrade.
- **`anchorSkewWindowMS` MIRRORS `srv.AnchorMaxSkewMS`, AND THE COMPARISON IS
  INCLUSIVE.** The CLI needs to know where the server's line sits to avoid
  crossing it, so the mirror must not drift: wider re-bases epochs the server
  would have accepted, narrower carries forward one it will refuse.
  `TestPlanAnchorBoundaryAgreesWithTheServer` pins the boundary itself, because
  an off-by-one is invisible from outside — both sides still "work", and the only
  symptom is a grid that jumps for no reason. Both directions are tested, since
  the server compares *absolute* skew.
- **AN EXPLICIT `-epoch-ms` IS NEVER REWRITTEN.** The caller stated a value and is
  in a position to judge it; the server stays the only authority on whether it is
  acceptable. "Helping" here would silently override a decision the caller made,
  and would replace a clear server `400` with a client-side guess.
- **THE NOTICE GOES TO STDERR SO `-json` STAYS THE SNAPSHOT.** An unrequested
  narrative line is most damaging in a machine-readable stream, and the existing
  `-json` contract is that stdout is the snapshot and nothing else.

Each of those claims is a mutation row, because each is a way this could look
fixed while being wrong: `191-cli-anchor-resends-a-stale-epoch` (the original
defect, restored), `192-cli-anchor-always-rebases-the-epoch` (fixing the stale case
by re-basing *every* epoch, which invents the timeline the disclosure exists to
prevent), `193-cli-anchor-rebases-silently`, `194-cli-anchor-substitutes-its-own-clock`
and `195-cli-anchor-overrides-an-explicit-epoch`. Rows 91 and 92 were re-anchored
onto the extracted `planAnchor` when it was introduced; a re-anchored row must be
confirmed **caught**, since `--lint` only proves the anchor is unique and present.

### Waiting for a verdict in the CLI

`agentcli wait` (and `push --wait`) close step 4 of the agent loop, which is the
only step with no command — so every agent author hand-wrote the same
sleep-and-poll, and during live testing it was written by hand about a dozen times
in one session. The invariants are chosen so the defect it prevents cannot come
back through a refactor.

- **SATISFACTION REUSES `verdictIsStale`.** It is the single comparison defining
  staleness, shared with `state` and `eval-result`. A second comparison written
  for `wait` could drift, and then two commands would call the same version current
  in one and stale in the other — the exact split strudel-agent-uvj.14 closed.
- **AN OLDER VERDICT NEVER SATISFIES THE WAIT.** This is the regression the
  command exists to prevent: sleeping too little, reading the PREVIOUS version's
  verdict, and concluding a bad push worked. It needs its own explicit test rather
  than incidental coverage, and mutation row `163-cli-wait-accepts-an-older-verdict`
  guards it.
- **THE BUDGET IS THE EXISTING `-timeout`,** established once in `run()` and
  shared by the push and the wait. There is no second budget flag: two deadlines
  would let a command spend twice what the caller asked for. The sleep is a
  `select` on `ctx.Done()`, so `-timeout` bounds the WAIT and not merely each
  request — a plain `time.Sleep` would overshoot by up to one interval per poll.
- **IT FAILS FAST WHEN `listenerCount == 0`,** because no verdict can then EVER
  arrive. Polling to the timeout would spend the caller's whole budget to reach a
  conclusion available on the first read, and would report it as a bare timeout,
  hiding the diagnosis behind "try again later". The check runs on EVERY poll, not
  just the first, so a listener that disconnects mid-wait gives the same message.
  This is the constraint most easily missed, hence mutation row
  `165-cli-wait-ignores-the-listener-count`.
- **NO SUCCESS LINE WITHOUT A VERDICT.** On the timeout path there is no verdict,
  so nothing about `ok` is printed. A wait that burned its budget and printed "ok"
  anyway would be worse than no wait: it is indistinguishable from a real answer.
- **`push --wait` WAITS FOR THE VERSION THE SERVER RETURNED,** not a re-read and
  not the pre-push version. It calls the same `waitForVerdict` the `wait` command
  does; a second copy of that loop would be a second place for the
  never-accept-an-older-verdict invariant to be got wrong.
- **IT ADDS NO ENDPOINT.** It is a bounded client-side poll loop over
  `GET /api/state`, so the one-command-to-one-endpoint claim in
  `cmd/agentcli/main.go` still holds. A timeout exits `1`, never `2` — the command
  line was valid and the reads were answered (row
  `168-cli-wait-timeout-is-a-usage-error`).
- **IT DOES NOT HEARTBEAT.** There is no `agentcli heartbeat`, so a wait long
  enough to outlast the presence lease still needs the caller's own
  `POST /api/heartbeat`. This is documented in `AGENT_API.md` and `README.md`
  rather than papered over with a subcommand that would touch a second endpoint.

## Boundedness in tests

A hang is a test failure, not a waiting strategy.

Every potentially blocking operation needs its own bound:

- request context/deadline;
- transport/client timeout;
- watchdog for goroutine/channel waits;
- bounded server shutdown.

Do not rely on an unbounded `wg.Wait()`, `httptest.Server.Close()`, or a deadline checked only after a possibly blocking call.

For APIs owned by a single goroutine, bound the API call itself. A parked goroutine after the watchdog fires is acceptable when the test is about to fail.

### The overriding rule

> A bound may make a hang fast; it must never make a hang pass.

Adding a timeout must not remove the assertion that proves the underlying defect.

## Documentation verification

`AGENT_API.md` is a shipped artifact and is machine-checked against the implementation and a running server.

The checks should prove all of the following:

- documented routes and actual routes match in both directions;
- every event kind is documented;
- documented request shapes are accepted;
- undocumented fields are rejected;
- documented response shapes match live server bytes;
- the document's shape anchors remain present.

Important anchors:

```text
<!-- shape:error -->
<!-- shape:codeRequest -->
<!-- shape:messageRequest -->
<!-- shape:anchorRequest -->
<!-- shape:evalAck -->
<!-- shape:evalResultRequest -->
<!-- shape:snapshot -->
<!-- shape:storedEvalResult -->
<!-- shape:frame -->
<!-- frame-kinds -->
```

Do not remove or rename these without updating the document tests.

## Mutation testing

Mutation testing deliberately introduces defects and requires the test suite to catch them.

```bash
make mutation-check
make mutation-lint
./scripts/mutation-check.sh --list
./scripts/mutation-check.sh <mutation-id>
```

Rules:

- Mutations must actually change the intended source.
- Mutation anchors must be checked; a no-op mutation is not evidence.
- Mutations must be reverted automatically and byte-for-byte verified afterward.
- `caught` means a test failed as expected.
- `SURVIVED` is a failure.
- `WEAK` (compile/panic failure without the intended assertion catch) is a failure.
- `BROKEN` means the mutation anchor no longer matches and must be repaired.
- Routed mutations must match at least one test; a regex matching nothing is `SURVIVED`.
- Run only genuinely expensive mutations through narrow `-run` routing.
- Do not run mutation checks concurrently when measuring performance.

The table lives in `scripts/mutations.json` and is read by
`scripts/mutation-parse.py`. **A row is `{name, file, old, new}` plus an
optional `run`, and an anchor is JSON, so it may contain any character
whatsoever — including `|`, `||`, tabs and newlines.** Omit `run` entirely to
mean the whole suite, because an empty `run` is rejected and "no `-run`" must
stay distinguishable from `""`. No row may be added back into the shell script.

Two consequences are load-bearing:

- **A `WEAK` row caused by a malformed `-run` regexp is a table defect, not
  evidence.** The parser compiles every `run` value up front and refuses the
  grid, so that failure mode cannot masquerade as a result.
- **A malformed row must stop the run, never be skipped or coerced.** Records
  cross into bash NUL-separated through a **file**, never through `$(...)`:
  bash silently strips NUL bytes from command substitution, which would delete
  every field terminator and run the grid off garbage.

`make mutation-lint` validates the table and every anchor with no test run.
`./scripts/mutation-parse.py --selftest` proves a bar, a `||`, a tab, a newline
and a quote in an anchor round-trip, apply, and revert byte-identically. Run
both after touching the format: they are the only checks that fail if it
regresses toward a delimiter.

The grid **isolates itself in a throwaway git worktree** and never mutates the
caller's checkout. This is the harness owning the invariant, not a caller
remembering a convention: an interrupted run once left a mutation applied, and
two concurrent runs once poisoned each other's baseline into a false red. The
worktree is created from `HEAD` and then **overlaid with the caller's current
working-tree content**, including untracked files, so uncommitted work is what
gets graded — testing `HEAD` instead would quietly grade something nobody asked
about. `--list` and `--lint` are read-only and stay in place;
`MUTATION_WORKTREE=0` forces in-place mutation and announces itself, as does any
fallback when no worktree can be created.

Two consequences for other work:

- **Do not edit `mutation-check.sh` while it is running.** Bash reads a script
  incrementally by byte offset, so an edit mid-run makes it resume at a stale
  offset: this happened, and the grid silently ran all 140 rows twice and
  reported `280 caught`.
- **Tests must not assume the checkout's absolute path.** The grid runs in a
  worktree under `$TMPDIR`, which exposed `TestUnitExecStartPointsAtTheBuildOutput`
  asserting `ExecStart == filepath.Join(repoRoot, "srv", "srv")`. That can only
  hold at one directory on earth — the unit records where the service is
  *deployed*, which is host configuration, not a property of wherever the tests
  run. The test now asserts the portable relationship (one absolute command,
  under the unit's own `WorkingDirectory`, naming the file `make build` writes);
  all seven ways of breaking the unit still fail it.

A `BROKEN` row is **not** cosmetic. It is a row the grid cannot run, so the
behaviour it exists to prove is unproven, and the grid exits non-zero on it
for that reason. Two rows (`12-transport-accepts-any-body` and
`101-cli-flagless-help-hides-real-mistakes`) had been reporting BROKEN against
the current implementation for some time — an anchor matching twice, and an
anchor naming text a refactor deleted. Re-anchoring a row changes what it
proves, so the replacement must be confirmed **caught**, not merely applied;
`--lint` alone only proves the anchor is unique and present.

A timeout can expose a hang, but the test still needs to fail because the intended invariant was violated.

## Feature workflow

1. Write a failing test in the harness that owns the behavior.
2. Implement the behavior in the layer that owns the invariant.
3. Update `AGENT_API.md` when the wire contract changes.
4. Update `AGENTS.md` when an internal invariant changes.
5. Add or update mutation coverage for new behavior.
6. Run `make verify`.
7. Commit the completed work.
8. Sync/close the corresponding bead only after the code is actually committed.

- The pulse must be scoped to `.agent-connected`. A stylesheet that merely *mentions* the state classes is not enough; the animation itself has to live under the connected selector, or every state animates identically again. Strip CSS comments before searching, since the file explains the old unconditional pulse in prose containing the same text.

For browser timing or synchronization, test the **served JavaScript** with the fake clock/goja harness rather than translating the timing algorithm into Go.

Drive a decode seam (`handleFrame`), not only the renderer. Calling a paint function directly cannot prove it is wired to incoming frames — which is precisely how the original badge stayed decorative while every unit test of it would have passed. Where a function deliberately swallows bad input, have it **return** whether it handled the frame: a silent catch otherwise turns a caller passing the wrong type into a green test asserting nothing.

## Measurement discipline

- Use a real Git worktree when mutation or binary-build experiments could disturb the main tree. `scripts/mutation-check.sh` now does this itself for every mutating run; the rule stands for anything else that mutates the tree.
- Never edit a running bash script. It is read incrementally by byte offset, so a mid-run edit resumes at a stale offset — this corrupted a grid run and silently doubled its row count.
- Preserve and report command exit codes.
- Do not casually regenerate historical mutation benchmarks.
- When documenting a defect/fix, record concrete evidence: failure mode, relevant stack frames or output, and duration where timing is material.
