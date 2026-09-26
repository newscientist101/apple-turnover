# Agent Instructions

Strudel Agent: a live, multi-user algorithmic music performance. The Go server is the conductor
holding the one live performance state; an external AI agent drives it by pushing strudel code over
HTTP. README.md carries what a user needs (build, run, systemd, the API, authorization); this file
carries what an agent building the project needs — the architecture's non-obvious invariants and the
verification conventions. Do not put build/run instructions here, and do not put invariants in the
README.

## Architecture invariants

These are the things that are easy to break and hard to notice. Each has a test that catches it; if
you change one, expect red.

**One Conductor owns the performance** (`srv/conductor.go`). No database, nothing persisted. It holds
the code document, a monotonically increasing version, the timeline anchor (epoch ms + cps), the last
agent message, a bounded history, a playing flag and a listener count. Versions are never reused and
never skip: parallel pushes must yield exactly `1..N`, contiguous and unique, with no lost updates.

**The hub owns its subscriber set from one goroutine** (`srv/hub.go`). It is decoupled from the
Conductor and from the wire format — it fans one already-encoded `[]byte` out to every subscriber.
Because one goroutine owns the subscriber set, subscribe/unsubscribe/broadcast are safe concurrently, and
`SubscriberCount` is a round-trip to that goroutine: it **blocks forever if the hub wedges**. Every
call to it in a test must be bounded at the call, not in the loop around it.

**A subscriber that cannot keep up is dropped, not tolerated.** Each subscriber has a small buffered
send channel; a non-reading one is dropped with its channel closed **exactly once** rather than
allowed to block the fan-out. A double close panics, which is how mutation
`28-hub-double-close-allowed` is caught. Hub shutdown and a hub drop look identical to the subscriber
— the channel closes — so `/ws` selects on both.

**`/ws` never blocks on a client** (`srv/ws.go`). The read side is `websocket.Conn.CloseRead`, whose
cancelled context is the "client disconnected" signal, so a client that sends nothing cannot block
the handler and the handler needs no read of its own. Every write carries a per-frame write deadline
(`defaultWSWriteTimeout`, 5s), because a frame larger than a dead client's socket buffers would
otherwise wait forever for space that never comes. It unsubscribes on every exit path, so a listener
cannot leak a subscriber slot.

**The handshake answers with clear codes, never a panic:** 426 for a handshake-less GET, 400 for a bad
`Sec-WebSocket-Version`, 501 for a writer that cannot be hijacked, 403 for a cross-origin handshake,
405 + `Allow` for a non-GET verb, 1001 for a listener arriving after hub shutdown, a clean 1000 on hub
shutdown. `/ws` matches exactly one path.

**The 64 KiB payload cap** (`APIMaxBodyBytes`) is applied in `limitAPIRequestBody`, so no endpoint can
forget it. A body that is oversized **and** malformed must be **413, not 400** — the cap trips before
parsing. Errors are JSON under `/api`, net/http's plain text elsewhere.

### Not yet wired

Do not assume these work; they are open bd issues, not bugs.

- No snapshot on connect, and no `/api`-to-hub fan-out: `srv/api.go` never references the Hub, so a
  write does not reach listeners.
- No listener count published through the Conductor: `SetListenerCount` has **no production caller**,
  so `Snapshot.ListenerCount` always serialises as 0.
- No ping/pong, no dead-socket reaping, no message encoding beyond the raw bytes the hub is handed.
- The browser client does not exist. `GET /` still renders the template's `welcome.html`, and
  `srv/static/` is still the template's `style.css` + `script.js` (an SSH-copy-link handler). Nothing
  plays audio. The fan-out path is proven from the hub onwards only.

## Verification conventions

`make verify` is the repo-owned gate: `gofmt -l .`, `go vet ./...`, `go build ./...`,
`go test ./... -race -count=1`. Run it **before every commit**. When a new feature needs proving,
**extend `make verify`** rather than writing a throwaway script — a one-off script is not part of the
gate and the next agent will not run it.

Nothing in the suite needs the network, a database, a real port or a running service
(`httptest` + loopback only), and every operation is bounded, so a hang fails rather than waits.

`srv/integration_test.go` boots the **real** handler tree (`Server.routes()`, the same one
`Server.Serve` mounts) and drives it the way the external agent does; `srv/ws_test.go` and
`srv/hub_test.go` are its WebSocket and hub slices. New cases belong in these files.

What they assert, because these are the shapes a regression takes:

- the feedback loop `GET /api/state` → `POST /api/code` → `POST /api/message` →
  `POST /api/eval-result` → `GET /api/state`, asserting on response **bodies**, not just status codes;
- the error/edge matrix as table-driven cases: malformed and oversized bodies, missing
  `Content-Type`, unknown/zero/negative versions, stale eval reports, 405 + `Allow`, JSON 404s under
  `/api`;
- concurrency: parallel pushes over a real loopback server, versions exactly `1..N`;
- `GET /` renders to completion, asserted on **end-of-document markers** — the only thing that catches
  an `html/template` render aborting mid-document while `HandleRoot` still returns 200;
- the WebSocket contract above, over both a real loopback listener and the in-process recorder,
  including a wedged listener — one that never reads a 1 MiB frame through a 1 KiB receive buffer —
  which must not be able to hold up shutdown;
- the hub core: byte-identical fan-out to every subscriber, close-exactly-once, concurrent churn clean
  under `-race` including concurrent `SubscriberCount` polling, and a non-reading subscriber unable to
  block anyone (`30-hub-slow-client-blocks` wedges the hub and fails after a 10s watchdog rather than
  hanging);
- the shipped binary: `cmd/srv` is built and run on a loopback port and the loop re-driven through the
  real process. Measuring mutations needs a real worktree or clone, because a `git archive` copy
  cannot build it.

### Boundedness in tests

A hanging test is itself a defect, so bounding is a requirement, not a nicety. The parallel test
carries three independent bounds — a context deadline on every request, per-transport response-header
and client timeouts, and a watchdog waiting on a channel rather than on `wg.Wait()` — and it closes
its `httptest.Server` with a timeout on a goroutine instead of `defer ts.Close()`, because `Close`
waits for outstanding requests and would itself hang on a wedged handler. Verified against a
deliberately deadlocked handler: the test fails in ~12s instead of hanging (mutation
`23-wedged-state-handler`). See "The defect class this project actually hit" below for the two shapes
of ineffective bound.

### Documentation needs no mutation proof

The mutation grid exists to prove the *implementation's* tests are non-vacuous. **Documentation is
exempt.** Do not spend a worktree-and-mutate cycle proving that a doc-consistency check catches
something, and do not add a mutation entry for a README claim. Verify prose by reading it against the
source, and anything describing runtime behaviour by exercising it against a running server — one
`curl` pass over the documented endpoints is the whole cost.

That is not licence to let prose rot. The README's API table is held to `routes()` by
`TestReadmeAPITableMatchesRoutes`, a deterministic text comparison in the ordinary suite: every
mounted endpoint must appear in the table, and every endpoint in the table must be mounted. It is
non-vacuous by construction, not by mutation — a parse that finds no routes fails the test rather
than passing it, and both directions report an error on mismatch — so drift is caught by
`make verify`.

### Building

`make build` writes `srv/srv` — because `srv/` is a package directory, `go build -o srv` places the
binary inside the `srv/` directory. Run `./srv/srv`; it listens on `:8000` by default, overridable with `-listen`
(`cmd/srv/main.go`). Tests must not bind `:8000`.

## Mutation testing

A green test run proves nothing on its own: a test that asserts nothing passes just as loudly as a
real one. `scripts/mutation-check.sh` is this repo's answer. It applies 41 small deliberate defects
("mutations") to the implementation, runs the suite after each, and **requires** a failure.

```bash
make mutation-check                                  # whole grid, bounded by timeout 900
./scripts/mutation-check.sh --list                   # the 41 names
./scripts/mutation-check.sh 30-hub-slow-client-blocks # one mutation
./scripts/mutation-check.sh 04-payload-cap-removed    # one mutation
MUTATION_TEST_ARGS="-run TestHub" ./scripts/mutation-check.sh   # hold a subset to account
```

Knobs: `MUTATION_TIMEOUT` (default 180s) bounds one run, `GO_TEST_TIMEOUT` (default 150s) is
`go test -timeout`, `MUTATION_TEST_ARGS` passes extra flags. Tighten them while exploring; the table's
routing must not depend on them.

Each mutation is applied with `python3` (no sed portability trap), verified to have actually changed
the file, reverted, and checked byte-identical against a recorded sha256 — including on Ctrl-C, because
the revert also runs from an `EXIT` trap. Only a **failing test** counts as a catch: a mutation that
merely fails to compile is weak evidence rather than a catch, and a survivor exits non-zero.

### The four verdicts

| Verdict | Meaning |
|---|---|
| `caught` | A test FAILED. This is the only good outcome. |
| `SURVIVED` | The suite passed. Coverage is missing, or a `-run` regex matched nothing. |
| `WEAK` | It failed to compile or panicked for a non-assertion reason. Proves nothing about the assertions. |
| `BROKEN` | The anchor text no longer matches — the implementation moved and the table needs updating. |

`SURVIVED` and `WEAK` both exit non-zero. A mutation that merely fails to compile is reported `WEAK`,
not `caught`, so a typo in the table cannot masquerade as a passing check.

### The invariant, which overrides everything else

**A bound may make a hang FAST. It may never make a hang PASS.**

Bounding a wait is not the same as removing an assertion. Every test that failed before a change must
still fail, with a message that names what wedged. If a fix turns a hang into a pass, it has deleted
coverage — revert it. This is the single most important rule in this file, because bounding a wait can
silently convert a hang into a pass with nothing going red.

### The defect class this project actually hit: unbounded waits in tests

A bound is **ineffective** if, while the thing it bounds is blocked, control cannot reach the check.
Two shapes, both real here:

- **Shape A** — waiting on a goroutine that is itself parked inside the wedged component:
  `close(stop)` then `<-watchDone`, but the watcher never returns because it is blocked in a hub
  command, so `watchDone` never closes.
- **Shape B** — the deadline is checked only **after** the blocking call:
  ```go
  for {
      if got := h.SubscriberCount(); got == want { return }  // blocks forever on a wedged hub
      if time.Now().After(deadline) { t.Fatalf(...) }        // NEVER REACHED
      time.Sleep(2 * time.Millisecond)
  }
  ```

Any API implemented as a round-trip to a single owning goroutine (this repo's `Hub.SubscriberCount`
sends on a reply channel and waits) **blocks forever** if that goroutine wedges. Every unguarded call
to one in test code is an unbounded wait, whatever comment sits next to it.

The fix is to bound **the call itself**, not the loop around it:

```go
got := make(chan int, 1)
go func() { got <- h.SubscriberCount() }()
select {
case n := <-got:
    return n, true
case <-time.After(d):
    return 0, false   // ok == false means the component is wedged; report it, do not wait it out
}
```

The goroutine left parked when the bound fires is a deliberate, documented leak: a blocked channel
send cannot be cancelled, and the test is about to fail and exit. Moving the deadline check earlier in
the loop does **not** work — the blocking call is still unbounded.

A wedged component and a component that answered wrongly are **different defects** and must produce
different messages. Never call the blocking API a second time inside a failure message; that
reintroduces the hang on the failure path.

### Making a RED: wedge the thing you are bounding

Never claim a defect is proven without reproducing it. Pick a wedge that actually parks the site:

- `30-hub-slow-client-blocks` wedges the hub inside `deliver`. It does **not** wedge the count path —
  no ws test drives the hub into that state, so under `-run TestWS` it reports `SURVIVED`. It is the
  wrong tool for count-path sites.
- For a count-path site, hand-apply a count-only wedge in a worktree: replace the run loop's
  `reply <- len(subs)` with `_ = reply`. The hub keeps serving
  subscribe/unsubscribe/broadcast/close and only stops answering `count`.

A RED is proven when you capture `panic: test timed out` **plus the parked goroutine frames** naming
your file:line. The frames are the evidence; the panic alone is not.

### Routing (`-run` per mutation)

A mutation entry may carry an optional 5th pipe field narrowing the run to the test(s) that catch it.
Precedent: `23-wedged-state-handler`.

For the record, the grid's wall time fell in two distinct steps, and the difference matters: fixing
the unbounded waits took it from 312.680s to 201.09s (a hang became a fast failure), and routing then
took it from 201.09s to 133.92s. Routing must come *after* the waits are fixed — routing a wedged
mutation to a narrow subset merely hides the hang outside the selection instead of fixing it.

Rules learned the hard way:

- **The 5th field REPLACES `MUTATION_TEST_ARGS`, it does not narrow it.** `run_tests` does
  `args=(-run "$1")`. So holding the grid to a subset is **vacuous** for every routed mutation — it
  reports `caught` regardless. Never present that as proof routing dropped no coverage.
- An alternation (`TestA|TestB`) is safe as a 5th field: bash `read` assigns the whole remainder,
  pipes included, to the last variable. Such a row has six pipe-separated fields — that is correct.
- Routing errors are **self-detecting**: a regex that matches nothing makes `go test` pass, which is
  `SURVIVED` and exit 1. Confirm a regex names real tests with `go test ./srv/... -list '<regex>'`.
- Route only what is genuinely expensive. A ~5.5s mutation saves ~2s and adds a rename coupling; if a
  mutation's only catcher itself costs most of that, leave it unrouted and say why.
- If a later issue requires two tests to fail under one mutation, the regex must include **both**.

### Measurement discipline

- **Never mutate the real tree.** Worktrees only: `git worktree add /tmp/x HEAD`, mutate there,
  `git worktree remove --force` afterwards, then confirm `git worktree list` shows only the main tree.
- **A worktree does not carry uncommitted changes.** `git worktree add /tmp/x HEAD` checks out the
  *commit*, so any file you have edited but not committed is absent there. Copy every file the
  measurement depends on, not just the implementation. Symptom: the mutation "fails" by reporting a
  whole missing feature, and an anchor string silently no-ops because `str.replace` matched nothing.
  That is the `WEAK` verdict wearing a `caught` costume — assert the anchor matched before believing
  the result.
- **Never run two `mutation-check` invocations concurrently.** Timings become meaningless and spurious
  ~150s timeout panics appear. One measured 154.27s with a panic under concurrency, then 5.65s three
  times sequentially — the panic was contention, not a defect.
- **Measuring catchers requires a real git worktree or clone.** A `git archive` or plain file copy
  cannot build `./cmd/srv` (`error obtaining VCS status: exit status 128`), so
  `TestIntegrationBuiltBinaryServesTheLoop` falsely fails for every mutation and pollutes the survey.
- Per-invocation timings include a ~3.6s green-baseline pass that routing does **not** remove, so they
  understate routed savings. Quote whole-grid deltas for routing benefit.
- **Do not run `scripts/mutation-bench.sh` casually**: it overwrites
  `docs/mutation-bench/01-baseline.tsv`, the historical baseline that re-baselining work compares
  against.
- Piping the script into `tail` makes `$?` report tail's status. Redirect to a file and echo `$?`
  separately when an exit code matters.
- `run_tests` prints only the first 3 matching lines, so one mutation's several failures are not all
  surfaced. To show that two tests both fail, apply the mutation in a worktree and run `go test`
  directly.
- Report numbers, not adjectives: the exact command, wall-clock seconds, quoted output.

### Evidence standards

Every claim about a defect or a fix carries a quoted BEFORE (timeout panic plus parked frames) and a
quoted AFTER (no panic, a clean failure naming the wedge), each with durations. Prior reports to
follow as models: `docs/mutation-bench/07-unbounded-audit.md` (site inventory and classification) and
`docs/mutation-bench/08-unbounded-fixes.md` (class-level proof). Findings that contradict the plan are
more valuable than quiet workarounds — record them.

Two corrections worth knowing, both verified: the audit's claim that mutation 30 proves the ws
hang is wrong (it wedges `deliver` only), and `32-hub-count-escapes-the-hub-goroutine` is caught by
the `-race` detector **only** — with `-race` off the whole suite passes under it. Do not cite 32 as an
assertion catch.
