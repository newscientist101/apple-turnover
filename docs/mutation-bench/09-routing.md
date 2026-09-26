# Per-Mutation -run Routing: Notes and Proof

**Date:** 2026-09-26  
**Machine:** Linux ROGQ58 6.18.33.2-microsoft-standard-WSL2 (WSL2)  
**Go version:** go1.27.1 linux/amd64  
**Git commit:** 5a47cf5 (parent 1c07742)  
**Commands used:**

```bash
# Whole-grid routed run (quoted below)
timeout 900 ./scripts/mutation-check.sh

# Integration-harness restriction (vacuous for routed mutations)
MUTATION_TEST_ARGS="-run TestIntegration" timeout 900 ./scripts/mutation-check.sh

# Single-mutation verification (example)
MUTATION_TEST_ARGS="-run TestHubSlowSubscriberCannotBlockOthers" \
  GO_TEST_TIMEOUT=60s MUTATION_TIMEOUT=150 \
  timeout 250 ./scripts/mutation-check.sh 30-hub-slow-client-blocks

# Broken-5th-field probe (in a throwaway worktree)
git worktree add /tmp/routing-probe HEAD
# edit one 5th field to a nonexistent test name
timeout 250 ./scripts/mutation-check.sh 23-wedged-state-handler
# captured SURVIVED + exit 1
git worktree remove --force /tmp/routing-probe

# Regex-to-test audit (for every routed mutation)
go test ./srv/... -list '<5th-field-regex>'
```

---

## 1. The routing as it stands

Nine mutations now carry a 5th pipe field in the MUTATIONS table of `scripts/mutation-check.sh`:

| Mutation | 5th field (regex) | Matched test(s) (from `go test -list`) |
|---|---|---|
| 23-wedged-state-handler | `TestIntegrationParallelPushesAreBoundedAndContiguous` | `TestIntegrationParallelPushesAreBoundedAndContiguous` |
| 29-hub-unsubscribe-does-not-close | `TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce` | `TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce` |
| 30-hub-slow-client-blocks | `TestHubSlowSubscriberCannotBlockOthers\|TestHubConcurrentSubscribeUnsubscribeBroadcast` | `TestHubSlowSubscriberCannotBlockOthers`, `TestHubConcurrentSubscribeUnsubscribeBroadcast` |
| 31-hub-broadcast-skips-a-subscriber | `TestHubBroadcastReachesEverySubscriberWithIdenticalBytes` | `TestHubBroadcastReachesEverySubscriberWithIdenticalBytes` |
| 33-hub-dropped-client-not-removed | `TestHubSlowSubscriberCannotBlockOthers` | `TestHubSlowSubscriberCannotBlockOthers` |
| 35-ws-listener-never-subscribes | `TestWSUpgradeSucceedsAndStaysOpen` | `TestWSUpgradeSucceedsAndStaysOpen` |
| 36-ws-subscriber-leaked-on-exit | `TestWSDisconnectUnsubscribesTheListener` | `TestWSDisconnectUnsubscribesTheListener` |
| 37-ws-broadcast-trimmed | `TestWSBroadcastReachesTheListenerAsExactBytes` | `TestWSBroadcastReachesTheListenerAsExactBytes` |
| 40-ws-client-close-not-noticed | `TestWSDisconnectUnsubscribesTheListener` | `TestWSDisconnectUnsubscribesTheListener` |

Mutation 30's row has **six** pipe-separated fields because its regex is an alternation. The `awk -F'|'` command above truncates it; use the `read` form to see the full regex:

```bash
grep '^30-hub-slow-client-blocks' scripts/mutation-check.sh | { IFS='|' read -r n f o nw run; echo "[$run]"; }
# → [TestHubSlowSubscriberCannotBlockOthers|TestHubConcurrentSubscribeUnsubscribeBroadcast]
```

Mutation **38-ws-write-deadline-removed** was **DELIBERATELY LEFT UNROUTED** (it has 4 fields) by `.2.1.2`, on a measured cost/benefit: its only catcher, `TestWSWedgedListenerCannotHangTeardown`, itself costs ~1.5s because it wedges a listener for ~1s before it can notice; routing would save under 1s while coupling the one test whose correctness depends on real wall time. This is a deliberate decision, not an omission.

---

## 2. Whole-grid result (routed)

```bash
timeout 900 ./scripts/mutation-check.sh
```

**Result:**

```
mutation-check: 41 caught, 0 survived, 0 weak, 0 broken anchor(s)
mutation-check: PASSED — every mutation was caught; the suite is non-vacuous.
```

**Wall time:** **133.92s** (exit 0)

Compare against the steps that produced this number:

- Baseline (no fixes, no routing): **312.680s** (from `docs/mutation-bench/01-baseline.md`)
- After .1/.1.3 wait fixes (no routing): **201.09s**
- After `.2.1.1` hub routing: **160.82s**
- After `.2.1.2` ws routing (this table): **133.92s**

Arithmetic:
- Wait fixes saved: `312.680 - 201.09 = 111.59s` (35.7%)
- Hub routing saved: `201.09 - 160.82 = 40.27s` (12.9%)
- WS routing saved: `160.82 - 133.92 = 26.90s` (8.6%)
- Total saved: `312.680 - 133.92 = 178.76s` (57.2%)

**Crucial distinction:** the wait fixes and the routing are **two different things**. The wait fixes turned a hang into a fast failure (mutation 30 went from 153.587s → 27-35s per-invocation, ~15s in-grid). Routing only removed the unnecessary test runs *after* the waits were bounded. Routing a mutation that still hangs would merely hide the hang outside the selection — it would not fix it. The ordering gate in the parent issue was correct: routing was deliberately blocked until `.1` and `.1.3` closed.

---

## 3. The prescribed integration-harness cross-check is VACUOUS for routed mutations

The parent issue prescribed holding the whole grid to the integration harness as a cross-check:

```bash
MUTATION_TEST_ARGS="-run TestIntegration" timeout 900 ./scripts/mutation-check.sh
```

**Result (measured):**

```
mutation-check: 41 caught, 0 survived, 0 weak, 0 broken anchor(s)
```

However, **this is vacuous for every routed mutation**. The script's `run_tests` function does:

```bash
local args=("${TEST_ARGS[@]}")     # TEST_ARGS comes from MUTATION_TEST_ARGS
if [ -n "${1:-}" ]; then
    args=(-run "$1")               # the 5th field REPLACES the global args
fi
```

So a mutation with a 5th field ignores `MUTATION_TEST_ARGS` entirely. This was verified by `.2.1.1`:

```bash
MUTATION_TEST_ARGS="-run TestNoSuchTestExistsXYZ" GO_TEST_TIMEOUT=40s MUTATION_TIMEOUT=120 \
  timeout 200 ./scripts/mutation-check.sh 23-wedged-state-handler
# → mutation-check: baseline (unmutated) run [go test ./... -run TestNoSuchTestExistsXYZ]
# → 23-wedged-state-handler  --- FAIL: TestIntegrationParallelPushesAreBoundedAndContiguous (12.16s)
# → 1 caught
```

The baseline honours `MUTATION_TEST_ARGS`, but the per-mutation run uses the 5th field instead. `.2.1.1` also confirmed the converse: unrouted mutation 34 correctly reported SURVIVED under the same restriction.

**Conclusion:** the integration-harness restriction is a real check **only for unrouted mutations**. For the nine routed mutations it reports `caught` regardless, so it cannot prove routing did not drop coverage. Do not present this run as proof. The substitute below is what carries information.

---

## 4. Substitute proof: routing errors are self-detecting

**Claim:** a routing regex that matches nothing, or that omits the test which actually catches the defect, makes `go test` PASS, which the script reports as `SURVIVED` → exit 1. So a stale or too-narrow 5th field announces itself loudly instead of hiding.

**Demonstration (in a throwaway worktree):**

```bash
cd /home/exedev/strudel-agent
git worktree add /tmp/routing-probe HEAD
# edit scripts/mutation-check.sh: change 23-wedged-state-handler's 5th field to TestThisDoesNotExist
cd /tmp/routing-probe
timeout 250 ./scripts/mutation-check.sh 23-wedged-state-handler >/tmp/x.log 2>&1
echo $?
```

**Output:**

```
mutation-check: baseline (unmutated) run
mutation-check: baseline green
23-wedged-state-handler              SURVIVED  <-- the tests did not notice this defect
mutation-check: 0 caught, 1 survived, 0 weak, 0 broken anchor(s)
mutation-check: FAILED — some mutations were not caught.
1
```

**Limit:** this catches a routing regex that fails to match the covering test *today*. It does not protect against a future test being deleted along with its routing field in the same commit — that is what the regex-to-test audit in Section 1 is for.

---

## 5. Mutation 30 — both hub tests fail, no timeout panic

Issue `strudel-agent-e2v.3.1` requires that under mutation `30-hub-slow-client-blocks`, **both**
`TestHubSlowSubscriberCannotBlockOthers` and `TestHubConcurrentSubscribeUnsubscribeBroadcast` fail
with messages naming the wedge and with no timeout panic. Routing 30 to only one test would destroy
that proof, which is why `.2.1.1` kept the alternation.

**Verification (in a worktree with the mutation applied):**

```bash
cd /home/exedev/strudel-agent
git worktree add /tmp/v30 HEAD
# apply mutation 30's anchor to srv/hub.go in the worktree
cd /tmp/v30
timeout 200 go test ./srv/... -race -count=1 -timeout 120s \
  -run 'TestHubSlowSubscriberCannotBlockOthers|TestHubConcurrentSubscribeUnsubscribeBroadcast'
```

**Output (quoted):**

```
--- FAIL: TestHubSlowSubscriberCannotBlockOthers (15.01s)
--- FAIL: TestHubConcurrentSubscribeUnsubscribeBroadcast (15.00s)
    hub_test.go:401: Broadcast 3 blocked for 10s: a non-reading subscriber must be dropped, never waited for
    hub_test.go:73: Hub.Close did not return within 5s: the hub goroutine is wedged
    hub_test.go:486: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the watcher cannot tell how many subscribers are live
    hub_test.go:573: concurrent churn did not finish within 10s: subscribe/unsubscribe/broadcast is wedged
    hub_test.go:73: Hub.Close did not return within 5s: the hub goroutine is wedged
```

`panic: test timed out` occurrences: **0**. Both tests fail independently, each naming the wedge. The script's `run_tests` prints only the first 3 matching lines, so a script-level quote surfaces only `TestHubSlowSubscriberCannotBlockOthers`; the direct `go test` run is needed to show both.

---

## 6. Two corrections to earlier reports (verified)

1. **The audit's claim that mutation 30 proves the ws hang is wrong.** `30-hub-slow-client-blocks` wedges the hub inside `deliver` only. No ws test drives the hub into that state. Measured: `MUTATION_TEST_ARGS="-run TestWS" ./scripts/mutation-check.sh 30-hub-slow-client-blocks` → `SURVIVED` / 0 caught. The count-path sites needed a count-only wedge (replace the run loop's `reply <- len(subs)` with `_ = reply`). The audit's site inventory stands; its evidence is corrected.

2. **Mutation 32-hub-count-escapes-the-hub-goroutine is caught by the `-race` detector ONLY.** With `-race` OFF the whole `srv` suite PASSES under it, both pre- and post-sweep (189f0be: `ok 1.887s`; 45617ca: `ok 1.869s`). It is caught by a DATA RACE report in `TestHubConcurrentSubscribeUnsubscribeBroadcast` (3 warnings), not by any count assertion. The bounded helper does not mask it (its goroutine performs exactly the round-trip `SubscriberCount` already did). Do not cite 32 as an assertion catch.

---

## 7. Provenance

| Number | Source | How obtained |
|---|---|---|
| 312.680s | `docs/mutation-bench/01-baseline.md` line 25 | Baseline run in that file's commands |
| 201.09s | `/tmp/grid-now.time` | `timeout 900 ./scripts/mutation-check.sh` after .1/.1.3, before any routing |
| 160.82s | `/tmp/grid-routed.time` | Whole grid after `.2.1.1`'s commit `ec4abfb` |
| 133.92s | `/tmp/grid-routed2.time` | Whole grid after `.2.1.2`'s commit `5a47cf5` (this table) |
| 134.35s | `.2.1.2` agent's measurement | Same as above, independent run |
| 154.27s / 5.65s | `.2.1.2` agent's concurrency test | Two concurrent runs vs three sequential runs of mutation 38 |
| 3.6s | `.2.1.1`/`.2.1.2` agents' observation | Per-invocation green-baseline pass (~3.6s) that routing does not remove |
| 41 | `./scripts/mutation-check.sh --list` | Still 41 at every stage |
| All `go test -list` outputs | Directly run and quoted | Verified in Section 1 table |

All measurements were taken in throwaway worktrees (`git worktree add`, `git worktree remove --force`), each bounded with `timeout`. No worktrees remain; `git worktree list` shows only the main tree.

---

## 8. Gotchas recorded for the next person

1. **The 5th field REPLACES `MUTATION_TEST_ARGS`** — it does not narrow it. The `hold the grid to the integration harness` cross-check is vacuous for routed mutations. (Finding 1)
2. **An alternation `TestA|TestB` is safe** as a 5th field: bash `read` assigns the whole remainder (pipes included) to the last variable. (Finding 2)
3. **A typo'd 5th field matches nothing → `go test` passes → SURVIVED → exit 1.** Routing errors are self-detecting. (Finding 3)
4. **Never run two `mutation-check` invocations concurrently.** A concurrent run produced a spurious 154.27s timeout panic on mutation 38 that vanished on sequential re-runs (5.65s three times). (Finding 3)
5. **Measuring catchers requires a real git worktree/clone.** A `git archive` or plain file copy cannot build `./cmd/srv` (`error obtaining VCS status: exit status 128`), so `TestIntegrationBuiltBinaryServesTheLoop` falsely fails for every mutation and pollutes the survey. Use a `git worktree` or `git clone`. (Agent's finding)
6. **Per-invocation timings include a ~3.6s green-baseline pass** that routing does NOT remove, so they understate routed savings. Quote whole-grid deltas for routing benefit, not per-invocation deltas. (Agent's finding)
7. **`run_tests` prints only the first 3 matching lines** (`head -3`), so one mutation's several failures are not all surfaced. To show that two tests both fail, apply the mutation in a worktree and run `go test` directly.

---

## 9. Final state

- **`scripts/mutation-check.sh`** — nine 5th fields added across `.2.1.1` (`ec4abfb`) and `.2.1.2` (`5a47cf5`), comment block updated to state the override behaviour. No other changes.
- **`docs/mutation-bench/09-routing.md`** — this file.
- **Whole grid:** 41 caught / 0 survived / 0 weak / 0 broken, 133.92s.
- **`make verify`:** green (gofmt, vet, build, `go test -race -count=1` → `ok srv.exe.dev/srv 2.908s`).
- **`./scripts/mutation-check.sh --list`:** 41 names.
- **Tree:** clean apart from this file and the two commits above. `git worktree list` shows only main.
