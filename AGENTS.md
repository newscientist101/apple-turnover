# Agent Instructions

Do not put build/run instructions here, and do not put invariants in the README.

## Task tracking: beads (`bd`)

Run `bd prime` for the current workflow context and this project's persistent
memories (it needs a project that has memories, and auto-injects via
`bd hooks install`). The notes below cover what `bd prime` does not: the Dolt
sync and lease contract specific to this repo.

Tasks live in a beads database (embedded Dolt, database `strudel_agent`, schema
v66). **On a fresh clone the database does not exist** — the Dolt history is
published to the git origin under `refs/dolt/data`, which a plain `git clone`
does not fetch. Bootstrap it before doing anything else:

```bash
bd bootstrap      # clones refs/dolt/data from origin and wires the Dolt remote
```

`bd bootstrap` is non-destructive and idempotent; on an already-bootstrapped
workspace `bd sync` is the cheaper way to catch up.

**Keep bd versions aligned across machines.** The schema is part of the shared
contract: bd refuses in-place migration on a remote-backed database precisely
because two clones migrating independently fork the schema so that
`bd dolt pull` can no longer merge — silently and unrecoverably. So migrations
run once, by a single designated migrator, who then publishes:

```bash
bd dolt push      # publish a migrated schema (see `bd migrate` --force)
```

An installer that takes *latest* bd will pull a version whose bootstrap rewrites
`.beads/config.yaml` and `.beads/.gitignore`, which dirties the tree in a way
that looks like a setup failure. `sync.remote` is nested under `sync:` in the
current format; a clone whose bootstrap prints "Smart gate (BD_SMART_GATE):
auto-applying N pending schema migrations" is running an out-of-date binary
against a newer schema — upgrade rather than resetting the tree.

**Sync discipline.** The database is shared, so a task you close is visible to
everyone and a claim you take is respected. Use `bd sync`, which runs the whole
cycle for you:

```bash
bd sync            # pull -> check conflicts -> recompute is_blocked -> push
```

`bd sync` is the supported way to do this by hand. It exists because the loop is
easy to get subtly wrong, and two of its steps are not what a hand-rolled
pull/push does:

* Conflicts are checked **positively**, from the merge's own conflict rows and
  from Dolt's conflict tables — never inferred from the pull's exit status,
  which is not a trustworthy conflict signal in either direction. A zero exit
  does not mean the merge was clean.
* `is_blocked` is recomputed after the pull, so dependency edges merged in from
  another replica do not leave `bd ready` stale. Skipping this is how you end up
  working a bead that is actually blocked.
* Push retries a bounded number of times when another replica wins the race.
* The pull underneath does auto-settle the conflict classes it can settle
  convergently (machine-local metadata, audit-only dependency rows,
  last-write-wins on issue cells). Anything beyond those is **never**
  auto-resolved.

**Git hooks.** The bd gate is installed via `bd hooks install --shared`, which
writes the five shims to the *committed* `.beads-hooks/` directory and points
`core.hooksPath` at it (local config, so each clone re-runs the install). The
shims are marker-managed (`# --- BEGIN/END BEADS INTEGRATION ---`) and delegate
to `bd hooks run <hook>`, so a bd upgrade changes behavior without a reinstall
and any non-bd content outside the markers is preserved.

Branch on the exit code rather than parsing output:

| code | meaning | action |
| ---- | ------- | ------ |
| 0 | synced, or nothing to do | continue |
| 1 | error (transport, auth, storage) | investigate; do not assume it synced |
| 2 | merge conflict — halted, nothing pushed | **operator** resolves by hand |
| 3 | retries exhausted (push race, or a concurrent writer's dirty working set) | transient, nothing pushed; retry next tick |
| 4 | dirty working set is *stuck*, not busy | nothing pushed; no later tick will publish until an operator clears it |

Repeated runs keep halting the same way for exit 2 — it is not self-healing.

* `bd sync` **before** you read the task list, so you do not work from a stale
  view or re-do a bead another agent already closed.
* `bd sync` **after** you finish a unit of work. Unpushed work exists only on
  your machine — this is the failure mode that loses a session's claims.
* `bd dolt commit` first if you hit `cannot merge with uncommitted changes` on a
  pull. Never reach for `--force` to resolve a conflict you have not read; Dolt
  merges cell-level and the losing side is usually the stale one.

**Claims are leases, not advisory hints.** Claiming takes a lease with a TTL:

* `bd update <id> --claim` claims atomically: sets assignee to you and status to
  `in_progress`. Idempotent if you already hold it. Exclusivity is real on the
  node that granted the lease, so two agents no longer silently hold the same
  bead.
* `bd heartbeat <id>` pushes `lease_expires_at` forward while you work. Without
  it a long task loses its claim to expiry. Heartbeats write no Dolt commit, so
  any cadence comfortably below the TTL is fine.
* `bd unclaim <id>` releases a claim you are abandoning (clears assignee, resets
  to `open`). Only the current assignee may release its own claim.
* `bd reclaim` reverts `in_progress` issues whose lease has gone **stale** back
  to `ready`, recording a recovery event. This is the dead-worker reaper: use it
  when an agent crashed mid-task, with `--older-than` as a grace window past
  expiry (roughly 2x the claim TTL) so a worker briefly paused by GC or clock
  skew is not robbed of live work.
* **Verify the claim/close, not the sync.** A clean `bd sync` (exit 0) only
  reports on the sync; it does **not** mean a preceding `--claim` or `close`
  applied. Assert on the claim/close exit code *and* on `bd show <id> --json`
  state (`status`/`assignee`), never on `bd sync`'s exit code. The read can also
  lag the write by an instant, so confirm the transition rather than trusting a
  single read taken the moment the write returns.

`bd update --force` overrides another actor's **live** `in_progress` claim. Use
it only for genuinely abandoned claims, and prefer `bd reclaim`, which
distinguishes an expired lease from a live one. Leases are only enforceable on
the node that granted them; cross-machine claim visibility rides the issue's
`status` and `assignee`, which do commit.

**Dispatch convention.** Three levels: epic `.3vo`, task `.3`/`.4`, subtask
`.3.2`/`.4.1`. Dispatch only leaf subtasks (two-dot IDs), and keep a parent open
until all of its children are closed. `bd ready` lists parents and leaves alike
— filter to the leaves with `bd ready --exclude-type=epic`.

`bd ready` uses blocker-aware ready-work semantics (the same as
`bd list --ready`) and excludes `in_progress`, `blocked`, `deferred`, and
`hooked` issues, so it shows only genuinely claimable work. Add
`--include-deferred` or `--include-ephemeral` when you need those, and
`--explain` to see why something is or is not ready.

`.beads/config.yaml`, `metadata.json`, `README.md` and `.beads/.gitignore` are
tracked; the database itself (`.beads/embeddeddolt/`), backups and runtime state
are not. Do not add the database directory to git, and do not use
`.beads/issues.jsonl` as a sync channel — JSONL import is upsert-only and cannot
reconcile a deletion.

## Architecture invariants

These are easy to break and each has tests. Changes should expect failures until the invariant is preserved.

**Conductor (`srv/conductor.go`)**

* One Conductor owns performance state; nothing is persisted.
* Stores code document, monotonically increasing version, timeline anchor, last agent message, bounded history, playing state, and listener count.
* Versions are contiguous, unique, never reused, and never skipped, including under concurrent pushes.

**Hub (`srv/hub.go`)**

* One goroutine exclusively owns the subscriber set.
* It is independent of the Conductor and wire format and broadcasts one encoded `[]byte` to subscribers.
* The optional count hook is `func(int) []byte`: it is handed the count and returns a frame the HUB delivers, to every subscriber except the one that caused the change. It must not call back into the Hub — it runs on the hub goroutine, so that deadlocks.
* `SubscriberCount` is a synchronous round-trip and can block forever if the hub wedges.
* Slow subscribers are dropped and their channel is closed exactly once; a double close would panic (mutation `28-hub-double-close-allowed`).
* Hub shutdown and subscriber drop both appear as channel closure.

**WebSocket (`srv/ws.go`)**

* `/ws` must never block on a client.
* Use `CloseRead`/its canceled context to detect disconnects; the handler does not need its own read loop.
* Every frame write has a per-frame write deadline via `defaultWSWriteTimeout` (5s).
* Unsubscribe on every exit path so a listener cannot leak a subscriber slot.
* Handshake/error behavior is fixed: 426 for a handshake-less GET, 400 for a bad `Sec-WebSocket-Version`, 501 for a writer that cannot be hijacked, 403 for a cross-origin handshake, 405 + `Allow`, 1001 for a listener arriving after hub shutdown, and clean 1000 on hub shutdown.
* Match exactly one `/ws` path.

**API**

* Apply the 64 KiB `APIMaxBodyBytes` payload cap centrally in `limitAPIRequestBody`, so no endpoint can forget it.
* Size checking occurs before parsing, so oversized + malformed requests return 413, not 400.
* `/api` errors are JSON; other net/http errors remain plain text.

## Not yet wired

Do not assume these features work; they remain open work.

* No browser client/audio playback exists yet. `/` and `srv/static/` are still the template content.
* The verified fan-out path stops at the API: nothing yet proves a listener
  renders what it receives, because there is no browser client.

## Wired since the last audit

Recorded here so the "not yet wired" list above cannot quietly become wrong.

* **The agent CLI** (`cmd/agentcli`, issue strudel-agent-3vo.9.1): a pure-Go
  client for the documented HTTP API, so the harness stops hand-rolling `curl`.
  It adds NO server behaviour — every subcommand maps one-to-one onto an
  endpoint, and `srv.Server.Handler()` exists (behaviour-free) purely so its
  tests can drive the REAL `routes()` instead of a fake that could drift from
  the contract. Two properties are load-bearing and each has a mutation
  (rows 70-73):
  - **A rejection is never softened.** The server's `{"error": ...}` string is
    printed verbatim on stderr and the exit code is non-zero (1 for a refusal,
    2 for a bad command line). A CLI that paraphrased the reason, or exited 0 on
    a 400, would let an agent believe a refused push had landed.
  - **"Accepted" is not "applied."** The server deliberately answers
    `accepted:true` for a report it understood and then DISCARDED as stale, and
    `hush`/`play` are idempotent — nothing on the wire distinguishes the cases.
    So `eval-result` and `hush`/`play` read `/api/state` first and report the
    comparison ("accepted but IGNORED", "no change") rather than an unqualified
    success. The pre-read is a bounded extra round trip and a deliberately racy
    report, which is why the wording says what was OBSERVED, never what the
    server holds now.

* **Snapshot on connect** (`srv/ws.go`, `srv/event.go`): a listener that
  subscribes is sent the full state immediately as a `snapshot` frame, so a late
  joiner lands mid-performance instead of waiting for the next change. The
  subscribe-then-snapshot order is load-bearing: subscribing first means no
  change can slip through the gap between reading the state and joining the
  fan-out.
* **Write fan-out** (`srv/api.go`): every accepted `POST /api/code`,
  `/api/message`, `/api/hush`, `/api/play` and stored `/api/eval-result`
  broadcasts one frame to every listener. A REJECTED or accepted-but-ignored
  request broadcasts NOTHING — telling listeners about a change that did not
  happen is worse than silence, because the frame carries no "nothing changed"
  marker. This is why `Conductor.RecordEvalResult` returns `(stored bool, err
  error)`: a nil error alone cannot tell "stored" from "understood and dropped as
  stale", and the stale case must not broadcast.
* **Listener count** (`srv/hub.go`, `srv/server.go`): the hub goroutine owns the
  subscriber set, so it publishes the count through an optional `func(int) []byte`
  hook that `New` wires to `Server.listenerCountFrame`. Two properties are
  load-bearing and each has a mutation:
  - **The hook fires BEFORE the command is acknowledged.** `srv/ws.go` subscribes
    and then immediately snapshots, so publishing afterwards would let a
    listener's catch-up frame report a count that excludes the listener reading
    it. Mutation 56 inverts the order; the test catches it by BLOCKING the hook,
    which makes the interleaving observable instead of raced. Reading the count
    straight after `Subscribe` returns does NOT work and survived that mutation —
    an intermittent test is not evidence.
  - **A drop publishes too.** The hub drops a slow subscriber from its own
    goroutine, so a count maintained by the ws handler would go stale until that
    handler happened to wake. A broadcast that drops nobody publishes nothing.
* **Listener-count frames** (`srv/event.go`, `srv/hub.go`, `srv/server.go`): a
  connect, a disconnect and a drop each send one `listener-count` frame to
  every OTHER listener. Three decisions are load-bearing, and each is a place a
  later change could quietly undo:
  - **The hook RETURNS the frame; the hub delivers it.** The hook runs on the hub
    goroutine, so a hook that called `Hub.Broadcast` would send on the unbuffered
    channel of the goroutine executing it — a deadlock that takes every listener
    with it. Handing the bytes back keeps delivery on the one goroutine that owns
    the set, which is also why count frames stay totally ordered with performance
    frames instead of racing a goroutine of their own. Mutations 65 and 68 break
    each half of this (never built, built and thrown away).
  - **The listener that caused the change is excluded.** `srv/ws.go` snapshots
    AFTER `Subscribe` returns, so the connecting listener already holds this
    exact count; a second copy right after the frame every client decodes on
    connect would be noise, and the count frame would need a per-client special
    case. Mutation 67 sends it anyway.
  - **No coalescing.** A burst of N connects produces N frames, in order. That is
    affordable because the frames are queued before the hub acknowledges each
    change, so by the time `/api/state` shows the final count every frame is
    already delivered and a client applying them in order lands exactly where the
    server is.
  The count frame never bumps the version (`SetListenerCount` does not touch it,
  mutation 60) and is documented in `AGENT_API.md`, which is where a new kind has
  to be recorded.
* **Keepalive and dead-client reaping** (`srv/ws.go`, `srv/server.go`): each
  listener is pinged every `wsPingInterval` (30s) and reaped if it cannot answer
  within `wsPongTimeout` (5s). Two properties are load-bearing:
  - **The pong deadline is what distinguishes slow from dead.** This is the only
    path that ends a connection the hub would otherwise keep forever: a listener
    on a quiet performance is sent nothing, so neither the hub's drop-on-overflow
    nor the per-write deadline ever fires for it. Reaping on ping failure alone
    (no deadline) would hang; reaping without pinging would never fire.
  - **Reaping decrements the listener count for free.** The reaper ends the
    handler, and the handler's deferred `Unsubscribe` publishes the new count
    through the hook above. There is deliberately no counting code in `ws.go`,
    so the reaped client cannot leave a phantom listener in `/api/state`.
  Both budgets live on `Server` as fields, like `wsWriteTimeout`, so the tests
  exercise the real policy in milliseconds. Mutation 63 removes the pong
  deadline and wedges instead of failing.

* **The agent contract doc is machine-checked** (`AGENT_API.md`,
  `srv/agent_api_doc_test.go`, issue strudel-agent-3vo.9.2): the document an
  external agent codes against is held to the source and to a running server, in
  both directions, and a check that cannot find what it is looking for FAILS
  rather than skips. Three things are load-bearing, and each was proved by a RED
  before being believed:
  - **The payload shapes are compared to a LIVE server, not to the source.**
    `TestReadmeAPITableMatchesRoutes` and the event-kind check both read source,
    so a json tag renamed in `srv/conductor.go` leaves them perfectly happy while
    every agent reading the doc breaks. Only driving the real handler tree and
    diffing live bytes catches that.
  - **Request shapes are pinned from both sides.** A body with exactly the
    documented fields must be accepted AND the same body plus one undocumented
    field must be rejected, which is what stops the doc from under-claiming
    (a field the server accepts but nobody wrote down).
  - **The frame-table parse is anchored.** The snapshot's field table is written
    in the same markdown shape as the frame-kind table, so an unscoped row scan
    reports `version` and `code` as frame kinds the server never sends — a check
    that cries wolf on the document's own tables trains the next agent to ignore
    it. `<!-- frame-kinds -->` scopes it.
  The anchors (`<!-- shape:NAME -->`, `<!-- frame-kinds -->`) are load-bearing:
  a document with several ```json fences is otherwise ambiguous, and a check
  that binds to the wrong block is a coin flip. `TestAgentAPIDocShapesAreStable`
  asserts the anchor set, so the set of checked shapes cannot quietly shrink.

## Verification conventions

`make verify` is the repo gate:

```text
gofmt -l .
go vet ./...
go build ./...
go test ./... -race -count=1
```

Run it before every commit.

Commit each piece of completed work when the task finishes; never leave finished changes sitting uncommitted in the tree, and do not close a bead until its work is committed.

When adding verification, extend `make verify`; do not create throwaway scripts.

Tests must:

* avoid network, databases, real ports, and running services;
* use `httptest` and loopback where appropriate;
* bound every operation so a hang fails rather than waits.

Main test locations:

* `srv/integration_test.go` — boots the real handler tree (`Server.routes()`, the same one `Server.Serve` mounts) / end-to-end API behavior.
* `srv/ws_test.go` — WebSocket behavior.
* `srv/hub_test.go` — Hub behavior.
* Extend these files when a new feature needs proving; a one-off script is not part of the gate and the next agent will not run it.

Coverage should include:

* API feedback loop asserting on response bodies, not just status codes;
* malformed/oversized/error cases;
* concurrent versioning with parallel pushes yielding exactly `1..N`;
* complete `/` rendering asserted on end-of-document markers;
* WebSocket behavior including a wedged listener — one that never reads a 1 MiB frame through a 1 KiB receive buffer;
* Hub fan-out, close exactly once, churn, slow clients (mutation `30-hub-slow-client-blocks` wedges the hub and fails after a 10s watchdog rather than hanging), and concurrent count polling;
* the shipped `cmd/srv` binary.

## Boundedness in tests

A hanging test is itself a defect, so bounding is a requirement, not a nicety.

Bounds must apply to the operation that can block — the parallel test carries
three independent bounds (a context deadline on every request, per-transport
response-header and client timeouts, and a watchdog waiting on a channel rather
than on `wg.Wait()`):

* request context;
* transport/client timeouts;
* watchdogs waiting on channels;
* server shutdown must itself be bounded.

Do not rely on `wg.Wait()`, `httptest.Server.Close()`, or a deadline checked only after a blocking call.

Close each `httptest.Server` with a timeout on a goroutine instead of `defer ts.Close()`, because `Close` waits for outstanding requests and would itself hang on a wedged handler. Verified against a deliberately deadlocked handler: the test fails in ~12s instead of hanging (mutation `23-wedged-state-handler`).

## Documentation needs no mutation proof

Documentation does not require mutation testing.

* Verify documentation against source.
* Verify runtime behavior with a running server.
* The README API table is checked against `routes()` by `TestReadmeAPITableMatchesRoutes`.
* A missing route must fail the check rather than produce a vacuous pass.

## Building

`make build` produces:

```text
srv/srv
```

`srv/` is a package directory, so `go build -o srv` places the binary inside the `srv/` directory. Run:

```text
./srv/srv
```

Default listen address is `:8000`; override with `-listen`.

Tests must not bind `:8000`.

## Mutation testing

`scripts/mutation-check.sh` applies deliberate defects and requires tests to catch them.

Examples:

```text
make mutation-check
./scripts/mutation-check.sh --list
./scripts/mutation-check.sh 30-hub-slow-client-blocks
./scripts/mutation-check.sh 04-payload-cap-removed
MUTATION_TEST_ARGS="-run TestHub" ./scripts/mutation-check.sh
```

Controls:

* `MUTATION_TIMEOUT` — bounds one mutation run.
* `GO_TEST_TIMEOUT` — `go test -timeout`.
* `MUTATION_TEST_ARGS` — extra test arguments.

Mutations must:

* actually modify the intended source;
* be reverted automatically and checked byte-identical against a recorded sha256 — including on Ctrl-C, because the revert also runs from an `EXIT` trap;
* treat assertion failures as catches (a failing test);
* be applied with `python3` (no sed portability trap), verified to have actually changed the file, reverted as above;
* treat compile/panic failures as `WEAK` — weak evidence rather than a catch;
* treat survivors as failures.

## Mutation verdicts

| Verdict    | Meaning                                                               |
| ---------- | --------------------------------------------------------------------- |
| `caught`   | A test failed as intended.                                            |
| `SURVIVED` | The suite passed; coverage may be missing or routing matched nothing. |
| `WEAK`     | Compile/panic failure; does not prove the assertion caught it.        |
| `BROKEN`   | Mutation anchor no longer matches the implementation.                 |

`SURVIVED` and `WEAK` are both failures.

## Why there is no gomutants gate

`gomutants` was evaluated as a replacement for this grid and **rejected**
(epic `strudel-agent-kki`; verdict and numbers in
`docs/mutation-bench/06-decision.md`). Do not re-litigate it without new
evidence. Two independent reasons, both measured:

* **It is slower.** 231.32s cold against this grid's 128.042s for all 41
  mutations. Its one fast number (2.52s) needs an unchanged tree and a
  populated cache — the wrong regime, because a gate runs after an edit.
* **It passes a deliberate deadlock.** `TIMED OUT` and `PENDING` are both
  excluded from the efficacy denominator *and* exit 0. Three wedged infinite
  loops scored exit 0, efficacy 81.82%, coverage 100% — and the verdict was
  cached and replayed. Independently, 23 of the 41 curated mutations have no
  gomutants mutant expressing the same defect, including every `ws.go` row and
  both hang rows.

So: no `make verify` step, no `.gomutants.yml`, no pinned dependency. A tool
whose `TIMED OUT` and `PENDING` verdicts both exit 0 must not be the thing that
says the suite is non-vacuous.

It remains useful as **non-gating breadth** in the dev loop, where its off-anchor
survivors are how new curated mutations get found:

```text
gomutants -w 1 --exclude-files 'conductor\.go$' \
  -cache=/tmp/gomutants-cache -o=/tmp/gomutants-out ./srv/...
```

Treat that as triage input, never a pass/fail. Keep `-cache`/`-o` outside the
repo and use a fresh cache path per run. Expect noise: ~77% efficacy, and
`conductor.go` is excluded because one of its mutants reaches 2.5 GB in ~7s.

## The invariant that overrides everything else

**A bound may make a hang fast; it must never make a hang pass.**

Adding a timeout must not remove the assertion that proves the defect.

A test that previously hung and now passes because the wait was merely bounded has lost coverage.

## Unbounded waits in tests

Two common bad patterns:

* Waiting for a goroutine that itself is blocked in the wedged component.
* Checking a deadline only after calling an API that may block forever.

For APIs backed by a single owning goroutine, bound the API call itself:

```go
got := make(chan int, 1)
go func() { got <- h.SubscriberCount() }()

select {
case n := <-got:
    return n, true
case <-time.After(d):
    return 0, false
}
```

A parked goroutine after timeout is acceptable when the test is about to fail.

Never call the same blocking API again while constructing the failure message.

## Making a RED

Reproduce the actual defect before claiming it is proven.

For Hub count-path hangs, use a count-only wedge such as replacing:

```go
reply <- len(subs)
```

with:

```go
_ = reply
```

A valid RED includes:

* `panic: test timed out`;
* parked goroutine frames identifying the relevant file/line.

The stack frames are the evidence, not the timeout alone.

## Mutation routing

A mutation may specify a 5th pipe-delimited field containing a `-run` regex.

Rules:

* The 5th field **replaces** `MUTATION_TEST_ARGS`.
* A regex matching no tests produces `SURVIVED`.
* Verify routed names with `go test ./srv/... -list '<regex>'`.
* Route only genuinely expensive mutations.
* Include all tests required to catch a mutation.

Routing is an optimization, not a substitute for fixing unbounded waits.

## Measurement discipline

* Never mutate the main tree; use a Git worktree.
* Worktrees start from the commit, so copy required uncommitted files explicitly.
* Assert mutation anchors actually matched.
* Never run mutation checks concurrently when measuring performance.
* Use a real Git worktree/clone for binary-building integration tests.
* Do not casually run `scripts/mutation-bench.sh`; it rewrites the historical baseline.
* Preserve actual exit codes when piping command output.
* Report exact commands, timings, and relevant output rather than adjectives.

## Evidence standards

Claims about a defect or fix should include:

**Before**

* timeout panic;
* parked goroutine frames identifying the wedge;
* duration.

**After**

* no hang;
* clean failure naming the wedged component;
* duration.

Use prior mutation-bench documents as reference for evidence format.

Known corrections:

* Mutation 30 does not prove the WebSocket hang; it wedges `deliver`.
* Mutation 32 is caught only by `-race`; without `-race` it is not an assertion catch.
