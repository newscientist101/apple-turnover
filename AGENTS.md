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

The sweeper exists only to **push** the expiry. It must not maintain the flag.

- It broadcasts `agent` on exactly one occasion: a **held** lease lapsing. A heartbeat broadcasts nothing, and a never-seen agent is an initial state rather than a transition, so it is never announced.
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
- The event vocabulary is `snapshot`, `code`, `message`, `transport`, `eval-result`, `listener-count`, `anchor`, `agent`, `dry-run`.
- **One optional member, on one kind.** `dry-run` frames carry a fourth field, `dryRun` (`{id, code}`), because that frame asks a listener to evaluate a candidate that was **not** published, and the snapshot cannot say so: it is performance state, and the candidate is deliberately not state. Every other kind omits the field entirely — `omitempty` on a nil pointer emits nothing, so the frames a client already knew stay byte-identical. A second bespoke frame *shape* would be worse than this, because it would break the single decode path; a new field on the existing envelope does not.
- Event names describe listener-visible changes, not endpoint names; therefore play and hush both use `transport`.
- A frame is sent for a **transition**, not for an accepted write that changed nothing observable. A heartbeat is accepted and silent; a lease lapse is announced once.
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
- The visualizer is pattern-driven only: it paints hap lanes and a playhead
  from `currentPattern.queryArc()`, with no audio-context dependency. A
  waveform branch was removed because the pinned bundle exposes no reachable,
  connected analyser path for it (strudel-agent-uvj.2), and an unwired scope
  would have reported sound that was never observed.
- Lane identity and lane colour come from **one** resolver (`laneFor`), so they
  cannot disagree. Precedence is **explicit**, not first-match: pitch
  (`note`, then `n` — one control with two spellings in the pinned bundle) wins,
  then the sound (`s`), then `other`. A hap carrying both pitch and sound is a
  lane per **pitch**; keying it on `s` collapses a whole pattern onto one lane
  named after a shared instrument (strudel-agent-uvj.10). Deriving the colour by
  re-inspecting the hap is the defect, so `colourFor` takes the resolved kind.
- **Lane colour is per-lane, and it is a HASH OF THE LANE KEY.** `colourFor(key,
  kind)` picks from a per-family palette by `hashKey(key) % palette.length`, never
  by lane index: `laneKeys.sort()` runs every frame, so an index-based colour
  reassigns itself whenever the lane count or ordering changes and the display
  flickers between pushes (strudel-agent-uvj.9).
- **The three family palettes are DISJOINT, and that is load-bearing.** It is
  what lets a lane's family be asserted by *membership* — "the colour painted is
  one of the note palette's entries". Hue cannot do this job: the `other` family
  is grey-violet by design and most of its entries sit inside the note family's
  blue band, so a hue-banded check accepts an `other` colour as a note colour.
  That is the uvj.10 defect re-entering through the palette instead of through
  the resolver, and a hue-banded version of the identity test let mutation
  `130-viz-lane-colour-decoupled-from-lane` survive. Assert membership, and keep
  the disjointness test that makes it sound.

## Standing limitations

These are design properties, not bugs:

- **No server audio.** The Go process never evaluates JavaScript or produces sound.
- **Browser-only evaluation.** A server-accepted pattern can still fail in Strudel.
- **Bar-aligned synchronization only.** There is no cross-machine sample clock.
- **Drift is observable, not eliminated.** The client reports measured drift and `unscheduled` when the anchor cannot be used.
- **No persistence.** Restart returns to version 0.
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
  asked for.
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

**Any text describing a "pipe-delimited row", a "5th field", or "`|` is safe in
a run regex" is HISTORY from before `231764d` and must not be copied into a
row.** The table used to be a heredoc inside `mutation-check.sh`, parsed as
`<name>|<file>|<old>|<new>|<optional -run regex>`, and that format could not
express an anchor containing a `|`: the delimiter split the row, `old` was
truncated at the bar, and the rest was read as the `-run` regex. **Every one of
those outcomes was non-diagnostic**: a truncated anchor matched nothing
(`BROKEN`, blaming the implementation for a table typo), failed to compile as a
regexp (`WEAK`, which reads like a routing mistake), or — worst — happened to
match exactly once, so the grid mutated the **wrong text** and reported the row
healthy. Commit `12f3a3d` worked around it by re-anchoring a row onto a
pipe-free line rather than by fixing the format. JSON removed the class of bug
rather than relocating it: there is no delimiter left for an anchor to collide
with. (Row 126 is the worked example — it was moved off its real target by that
workaround and is back on it; see `scripts/mutation-check.sh`.)

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
