# Agent Instructions

This is a Go web application template for exe.dev.

See README.md for details on the structure and components.

## Mutation testing

A green test run proves nothing on its own: a test that asserts nothing passes just as loudly as a
real one. `scripts/mutation-check.sh` is this repo's answer. It applies 41 small deliberate defects
("mutations") to the implementation, runs the suite after each, and **requires** a failure.

```bash
make mutation-check                                  # whole grid, bounded by timeout 900
./scripts/mutation-check.sh --list                   # the 41 names
./scripts/mutation-check.sh 30-hub-slow-client-blocks # one mutation
MUTATION_TEST_ARGS="-run TestHub" ./scripts/mutation-check.sh   # hold a subset to account
```

Knobs: `MUTATION_TIMEOUT` (default 180s) bounds one run, `GO_TEST_TIMEOUT` (default 150s) is
`go test -timeout`, `MUTATION_TEST_ARGS` passes extra flags. Tighten them while exploring; the table's
routing must not depend on them.

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
