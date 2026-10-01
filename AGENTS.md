# Agent Instructions

Do not put build/run instructions here, and do not put invariants in the README.

## Architecture invariants

These are easy to break and each has tests. Changes should expect failures until the invariant is preserved.

**Conductor (`srv/conductor.go`)**

* One Conductor owns performance state; nothing is persisted.
* Stores code document, monotonically increasing version, timeline anchor, last agent message, bounded history, playing state, and listener count.
* Versions are contiguous, unique, never reused, and never skipped, including under concurrent pushes.

**Hub (`srv/hub.go`)**

* One goroutine exclusively owns the subscriber set.
* It is independent of the Conductor and wire format and broadcasts one encoded `[]byte` to subscribers.
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

* No snapshot on connect, and API writes do not fan out through the Hub; `srv/api.go` does not reference it, so a write does not reach listeners.
* `SetListenerCount` has no production caller, so snapshots report listener count as 0.
* No ping/pong, dead-socket reaping, or additional message encoding.
* No browser client/audio playback exists yet. `/` and `srv/static/` are still the template content.
* The working fan-out path is proven only from the Hub onward.

## Verification conventions

`make verify` is the repo gate:

```text
gofmt -l .
go vet ./...
go build ./...
go test ./... -race -count=1
```

Run it before every commit.

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
