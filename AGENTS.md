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
scripts/            mutation-testing tools
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
- Broadcast the same snapshot that the HTTP response returns.
- Keep `/api` errors uniformly JSON.
- Keep route handling centralized in `routes()`; tests should exercise that real tree.

### Event frames

Every listener event has exactly this shape:

```json
{"kind":"<what happened>","snapshot":{...}}
```

- `snapshot` is the actual snapshot value, not a second bespoke representation.
- All frame construction goes through one encoder.
- The event vocabulary is `snapshot`, `code`, `message`, `transport`, `eval-result`, `listener-count`, `anchor`, `agent`.
- Event names describe listener-visible changes, not endpoint names; therefore play and hush both use `transport`.
- A frame is sent for a **transition**, not for an accepted write that changed nothing observable. A heartbeat is accepted and silent; a lease lapse is announced once.

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
  Lane colour vocabulary stays `other`/`note`/`sample`; per-lane palette
  variation is a separate concern (strudel-agent-uvj.9).

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

- Use a real Git worktree when mutation or binary-build experiments could disturb the main tree.
- Preserve and report command exit codes.
- Do not casually regenerate historical mutation benchmarks.
- When documenting a defect/fix, record concrete evidence: failure mode, relevant stack frames or output, and duration where timing is material.
