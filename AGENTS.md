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
* Slow subscribers are dropped and their channel is closed exactly once.
* Hub shutdown and subscriber drop both appear as channel closure.

**WebSocket (`srv/ws.go`)**

* `/ws` must never block on a client.
* Use `CloseRead`/its canceled context to detect disconnects; the handler does not need its own read loop.
* Every frame write has `defaultWSWriteTimeout` (5s).
* Unsubscribe on every exit path.
* Handshake/error behavior is fixed: 426, 400, 501, 403, 405 + `Allow`, 1001 after hub shutdown, and clean 1000 on shutdown.
* Match exactly one `/ws` path.

**API**

* Apply the 64 KiB `APIMaxBodyBytes` limit centrally in `limitAPIRequestBody`.
* Size checking occurs before parsing, so oversized + malformed requests return 413.
* `/api` errors are JSON; other net/http errors remain plain text.

## Not yet wired

Do not assume these features work; they remain open work.

* API writes do not fan out through the Hub; `srv/api.go` does not reference it.
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
* bound every operation so hangs fail instead of waiting forever.

Main test locations:

* `srv/integration_test.go` — real handler tree / end-to-end API behavior.
* `srv/ws_test.go` — WebSocket behavior.
* `srv/hub_test.go` — Hub behavior.

Coverage should include:

* API feedback loop and response bodies;
* malformed/oversized/error cases;
* concurrent versioning with exact `1..N` results;
* complete `/` rendering;
* WebSocket behavior including wedged listeners;
* Hub fan-out, exact-once close, churn, slow clients, and concurrent count polling;
* the shipped `cmd/srv` binary.

## Boundedness in tests

A hanging test is a defect.

Bounds must apply to the operation that can block:

* request context;
* transport/client timeouts;
* watchdogs waiting on channels;
* server shutdown must itself be bounded.

Do not rely on `wg.Wait()`, `httptest.Server.Close()`, or a deadline checked only after a blocking call.

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

Run:

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
./scripts/mutation-check.sh <mutation-name>
MUTATION_TEST_ARGS="-run TestHub" ./scripts/mutation-check.sh
```

Controls:

* `MUTATION_TIMEOUT` — bounds one mutation run.
* `GO_TEST_TIMEOUT` — `go test -timeout`.
* `MUTATION_TEST_ARGS` — extra test arguments.

Mutations must:

* actually modify the intended source;
* be reverted automatically;
* restore byte-identical files;
* treat assertion failures as catches;
* treat compile/panic failures as `WEAK`;
* treat survivors as failures.

## Mutation verdicts

| Verdict    | Meaning                                                               |
| ---------- | --------------------------------------------------------------------- |
| `caught`   | A test failed as intended.                                            |
| `SURVIVED` | The suite passed; coverage may be missing or routing matched nothing. |
| `WEAK`     | Compile/panic failure; does not prove the assertion caught it.        |
| `BROKEN`   | Mutation anchor no longer matches the implementation.                 |

`SURVIVED` and `WEAK` are both failures.

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
