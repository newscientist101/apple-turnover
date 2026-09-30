# kki.2 — The Cheap-Win Experiment: Is Per-Mutation Routing Enough?

**Date:** 2026-09-30
**Machine:** Linux ROGQ58 WSL2, 12th Gen Intel(R) Core(TM) i7-12700K
**Go:** go1.27.1 linux/amd64
**Commit under test:** `2d9834d` (main; no test/script modified by this task)
**Deliverable for:** `strudel-agent-kki.2` (parent `kki`, epic `kki`)

## 0. The question, and the answer up front

`kki` exists to decide whether mutation checking in this repo can be made
cheap enough to run on every local review. `kki.2` is the cheap option:
narrow each mutation's `go test` run to only the test(s) that catch it, and
see whether that alone makes the grid locally runnable.

**Answer: the cheap option is already exhausted.** Routing every one of the
41 mutations — not just the 9 already routed by `e2v.2.1` — takes the grid
from **128.042s** to a measured **79.5s**, and the remaining 79.5s is
almost entirely *not* test execution (§5). Every mutation is caught; nothing
is left to win.

The interesting number is not the 48.5s saved. It is that after full routing,
**41 individually-timed runs sum to 56.672s of which only 12.819s is
attributable to the tests** — the other 43.853s is `go test` process startup
and package compilation, paid again for every mutation (§4). That cost is
invariant to routing. No amount of `-run` narrowing can touch it, which is
why the verdict is "no" and why buying a different tool is the remaining
question for `kki.3`–`kki.6`.

## 1. What was already done, and what was left

`e2v.2.1` routed the **nine slowest** mutations (5th pipe field in
`scripts/mutation-check.sh`). That left **32 unrouted**, which is the scope
surveyed here. `e2v.3.2` §6 predicted kki.2's remaining unique scope as
"the full 41-row survey, the projected-vs-measured comparison, and its
verdict". This file is that.

## 2. Method

Every regex below was **measured**, not inferred. For each mutation: start
from the narrowest plausible test name (read off the mutated file and the
behaviour), then widen only until the harness prints `caught`. A `SURVIVED`
at a narrow width is recorded as information, not hidden.

```bash
# Survey + timing, one invocation per mutation, SEQUENTIAL (never concurrent —
# 09-routing.md §8 gotcha 4: concurrency manufactures spurious timeout panics)
MUTATION_TEST_ARGS="-run <regex>" GO_TEST_TIMEOUT=60s MUTATION_TIMEOUT=150 \
  timeout 200 ./scripts/mutation-check.sh <name>

# Narrow baseline: mutation-check.sh pays ONE baseline per invocation, using
# the SAME -run, so the overhead to subtract is the narrow one, not the
# whole-suite one
timeout 200 go test ./... -run <regex> -race -count=1 -timeout 120s
```

Each candidate regex was also cross-checked against the real test names with
`go test ./srv/... -list '<regex>'`, so a typo cannot silently degrade to a
whole-suite run and look like a pass.

**The real tree was never mutated.** The one whole-grid measurement (§5) ran
in a disposable `git worktree` (`/tmp/kki2/wt`, since removed), never on
`main`. Every invocation is wrapped in an explicit `timeout`.

## 3. The 41-row survey

Nine rows are `e2v.2.1`'s existing routing, carried here unchanged so the
table is complete. The 32 new rows are this task's measurement. "Narrow
baseline" is the unmutated cost of that same `-run`; "subtracted" is the part
attributable to the test actually running.

| `01-empty-code-accepted` | `TestAPICodeRejectsBadRequests` | caught | 1.650 | 1.298 | 0.352 |
| `02-unknown-json-field-allowed` | `TestAPICodeRejectsBadRequests` | caught | 1.620 | 1.298 | 0.322 |
| `03-trailing-json-allowed` | `TestAPICodeRejectsBadRequests` | caught | 1.654 | 1.298 | 0.356 |
| `04-payload-cap-removed` | `TestAPIPayloadLimit` | caught | 1.670 | 1.292 | 0.378 |
| `05-cap-reported-as-400` | `TestAPIPayloadLimit` | caught | 1.609 | 1.292 | 0.317 |
| `06-allow-header-dropped` | `TestAPI` | caught | 1.705 | 1.347 | 0.358 |
| `07-api-404-is-500` | `TestAPIUnknownPath` | caught | 1.596 | 1.283 | 0.313 |
| `08-api-catchall-too-broad` | `TestAPI` | caught | 1.710 | 1.347 | 0.363 |
| `09-static-mount-removed` | `TestAPIServesStaticAssets` | caught | 1.605 | 1.303 | 0.302 |
| `10-eval-ack-lies` | `TestAPIEvalResultSuccess` | caught | 1.609 | 1.298 | 0.311 |
| `11-message-bumps-version` | `TestAPIMessageNarratesWithoutBumpingVersion` | caught | 1.632 | 1.294 | 0.338 |
| `12-transport-accepts-any-body` | `TestAPITransportRejectsBodies` | caught | 1.607 | 1.286 | 0.321 |
| `13-hush-plays-instead` | `TestAPITransportHushAndPlay` | caught | 1.601 | 1.299 | 0.302 |
| `14-shell-render-silently-truncates` | `TestRootRendersToCompletion` | caught | 1.609 | 1.289 | 0.320 |
| `15-eval-accepts-future-version` | `TestConductorRecordEvalResult` | caught | 1.594 | 1.289 | 0.305 |
| `16-eval-result-not-stored` | `TestConductorRecordEvalResult` | caught | 1.618 | 1.289 | 0.329 |
| `17-stale-report-regresses` | `TestConductorRecordEvalResult` | caught | 1.597 | 1.289 | 0.308 |
| `18-same-version-report-ignored` | `TestAPIEvalResultSuccess` | caught | 1.590 | 1.298 | 0.292 |
| `19-version-not-monotonic` | `TestConductorBumpVersion` | caught | 1.606 | 1.284 | 0.322 |
| `20-version-skipped` | `TestConductorBumpVersion` | caught | 1.619 | 1.284 | 0.335 |
| `21-history-window-wrong-start` | `TestConductorHistoryBounding` | caught | 1.618 | 1.290 | 0.328 |
| `22-snapshot-omits-playing` | `TestConductorTransportPlaying` | caught | 1.602 | 1.300 | 0.302 |
| `24-ws-405-fallback-removed` | `TestWSRejectsNonGetVerbs` | caught | 1.602 | 1.297 | 0.305 |
| `25-ws-wrong-close-status` | `TestWSHubCloseEndsTheListenerWithACleanClose` | caught | 1.624 | 1.292 | 0.332 |
| `26-ws-origin-check-disabled` | `TestWSUpgradeIsSameOriginOnly` | caught | 1.613 | 1.289 | 0.324 |
| `27-ws-closes-without-handshake` | `TestWS` | caught | 4.203 | 2.575 | 1.628 |
| `28-hub-double-close-allowed` | `TestHub` | caught | 1.634 | 1.330 | 0.304 |
| `32-hub-count-escapes-the-hub-goroutine` | `TestHubConcurrentSubscribeUnsubscribeBroadcast` | caught | 1.694 | 1.309 | 0.385 |
| `34-hub-removal-not-recorded` | `TestHub` | caught | 1.634 | 1.330 | 0.304 |
| `38-ws-write-deadline-removed` | `TestWSWedgedListenerCannotHangTeardown` | caught | 3.661 | 2.309 | 1.352 |
| `39-ws-going-away-not-reported` | `TestWSConnectingAfterHubCloseIsClosedGoingAway` | caught | 1.606 | 1.288 | 0.318 |
| `41-ws-binary-frames` | `TestWSBroadcastReachesTheListenerAsExactBytes` | caught | 1.680 | 1.287 | 0.393 |

### The nine already routed by `e2v.2.1` (unchanged, for completeness)

| Mutation | 5th field (regex) | Verdict | Raw (s) | Source |
|---|---|---|---|---|
| `23-wedged-state-handler` | `TestIntegrationParallelPushesAreBoundedAndContiguous` | caught | 15.440 | `09-new-baseline.tsv` |
| `29-hub-unsubscribe-does-not-close` | `TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce` | caught | 5.361 | " |
| `30-hub-slow-client-blocks` | `TestHubSlowSubscriberCannotBlockOthers\|TestHubConcurrentSubscribeUnsubscribeBroadcast` | caught | 25.396 | " |
| `31-hub-broadcast-skips-a-subscriber` | `TestHubBroadcastReachesEverySubscriberWithIdenticalBytes` | caught | 3.427 | " |
| `33-hub-dropped-client-not-removed` | `TestHubSlowSubscriberCannotBlockOthers` | caught | 5.395 | " |
| `35-ws-listener-never-subscribes` | `TestWSUpgradeSucceedsAndStaysOpen` | caught | 8.370 | " |
| `36-ws-subscriber-leaked-on-exit` | `TestWSDisconnectUnsubscribesTheListener` | caught | 8.385 | " |
| `37-ws-broadcast-trimmed` | `TestWSBroadcastReachesTheListenerAsExactBytes` | caught | 3.364 | " |
| `40-ws-client-close-not-noticed` | `TestWSDisconnectUnsubscribesTheListener` | caught | 8.366 | " |

Mutation `38-ws-write-deadline-removed` was **deliberately left unrouted**
by `.2.1.2` on measured cost/benefit. This survey independently confirms
that decision: routed to its one catcher it costs **3.661s raw against a
2.309s narrow baseline**, so its test alone spends ~1.35s of real wall time
wedging a listener. Narrowing it saves little and couples the one test whose
correctness depends on real wall time. The decision stands.

## 4. The finding that decides the question

Two numbers from the 32 measured rows:

```bash
awk -F'\t' 'NR==FNR{nb[$1]=$2;next} {d=$4-nb[$2]; s_r+=$4; s_b+=nb[$2]; s_d+=d; n++} \
  END{printf "n=%d sum_raw_routed=%.3f sum_narrow_baseline=%.3f sum_subtracted=%.3f\n", n, s_r, s_b, s_d}' \
  /tmp/kki2/narrowbase.tsv /tmp/kki2/timed.tsv
# n=32 sum_raw_routed=56.672 sum_narrow_baseline=43.853 sum_baseline_subtracted=12.819
```

**77% of a routed run is not test execution.** 43.853s of the 56.672s is
the unmutated narrow baseline — `go test` process start plus compiling the
`srv` package — and that floor barely moves with the regex:

| Narrow baseline across the 23 distinct regexes | Value |
|---|---|
| min | 1.283s |
| median | 1.294s |
| max | 2.575s (`TestWS`, the widest) |
| mean | 1.397s |

A one-test run and a 20-test run both cost ~1.29s, because the work is
dominated by starting the toolchain, not by running tests. Only two regexes
break the floor: `TestWS` (2.575s, all 11 ws tests) and
`TestWSWedgedListenerCannotHangTeardown` (2.309s, one deliberately slow
test).

This is the structural reason routing cannot rescue the grid: the per-mutation
floor is ~1.3s × 41 ≈ 53s no matter how the tests are narrowed.

## 5. Projected vs measured — and why the projection was checked

A projection from the 32 timed rows would be:

- 32 newly routed, raw 56.672s
- 9 already routed, raw 82.303s (from `09-new-baseline.tsv`)
- **projected individual total ≈ 138.975s**

That projection is *not* the whole-grid number, and the reason is the same
baseline asymmetry `09-routing.md` §3 recorded: 41 separate invocations pay
41 process starts and much recompilation, whereas one whole-grid run pays
its baseline once. So the projection was **measured instead of trusted**,
in a throwaway worktree with all 41 rows given a 5th field:

```bash
git worktree add /tmp/kki2/wt HEAD
# append the 32 surveyed regexes as 5th fields
cd /tmp/kki2/wt && timeout 900 ./scripts/mutation-check.sh
# → 41 caught, 0 survived, 0 weak, 0 broken; 79.498s then 79.505s (two runs)
git worktree remove --force /tmp/kki2/wt
```

| Configuration | Whole-grid cold wall | Verdict |
|---|---|---|
| kki.1 before all fixes (`330b34d`) | 312.680s | 41 caught |
| `e2v.2.1` nine routed (`2d9834d`) | 128.042s | 41 caught |
| **All 41 routed (this task)** | **79.5s** | **41 caught** |

**One discarded measurement, reported honestly.** An intermediate run
reported `32.144s` with `rc=1` while its own log said `PASSED`. That run
raced a still-live earlier invocation (the 30s shell window had cut the
foreground command off without killing it), and `09-routing.md` §8 gotcha 4
says exactly this produces inconsistent results. It is discarded. The two
clean runs above (79.498s, 79.505s, both `rc=0`) are the numbers reported;
the 0.007s spread between them is the noise floor for this measurement.

So routing 32 more mutations saves a further **48.5s (−37.9%)**, and gets the
grid from "over budget" to "comfortably inside it". But §4 shows why that is
the end of the road: the remaining 79.5s is ~53s of irreducible per-invocation
`go test` startup.

## 6. Proposed routing table (PROPOSAL — NOT APPLIED)

Per this issue's constraint, **nothing was applied**: `scripts/mutation-check.sh`
and the `Makefile` are unmodified in `main` (`git diff --stat -- scripts/` is
empty). The 32 regexes above are a proposal for whoever owns that decision;
the 9 existing rows are already live.

If applied, this is the complete proposed table (41 rows):

| # | Mutation | Proposed regex |
|---|---|---|
| 1 | `01-empty-code-accepted` | `TestAPICodeRejectsBadRequests` |
| 2 | `02-unknown-json-field-allowed` | `TestAPICodeRejectsBadRequests` |
| 3 | `03-trailing-json-allowed` | `TestAPICodeRejectsBadRequests` |
| 4 | `04-payload-cap-removed` | `TestAPIPayloadLimit` |
| 5 | `05-cap-reported-as-400` | `TestAPIPayloadLimit` |
| 6 | `06-allow-header-dropped` | `TestAPI` |
| 7 | `07-api-404-is-500` | `TestAPIUnknownPath` |
| 8 | `08-api-catchall-too-broad` | `TestAPI` |
| 9 | `09-static-mount-removed` | `TestAPIServesStaticAssets` |
| 10 | `10-eval-ack-lies` | `TestAPIEvalResultSuccess` |
| 11 | `11-message-bumps-version` | `TestAPIMessageNarratesWithoutBumpingVersion` |
| 12 | `12-transport-accepts-any-body` | `TestAPITransportRejectsBodies` |
| 13 | `13-hush-plays-instead` | `TestAPITransportHushAndPlay` |
| 14 | `14-shell-render-silently-truncates` | `TestRootRendersToCompletion` |
| 15 | `15-eval-accepts-future-version` | `TestConductorRecordEvalResult` |
| 16 | `16-eval-result-not-stored` | `TestConductorRecordEvalResult` |
| 17 | `17-stale-report-regresses` | `TestConductorRecordEvalResult` |
| 18 | `18-same-version-report-ignored` | `TestAPIEvalResultSuccess` **(see §7 — NOT a Conductor test)** |
| 19 | `19-version-not-monotonic` | `TestConductorBumpVersion` |
| 20 | `20-version-skipped` | `TestConductorBumpVersion` |
| 21 | `21-history-window-wrong-start` | `TestConductorHistoryBounding` |
| 22 | `22-snapshot-omits-playing` | `TestConductorTransportPlaying` |
| 23 | `23-wedged-state-handler` | *(already routed)* |
| 24 | `24-ws-405-fallback-removed` | `TestWSRejectsNonGetVerbs` |
| 25 | `25-ws-wrong-close-status` | `TestWSHubCloseEndsTheListenerWithACleanClose` |
| 26 | `26-ws-origin-check-disabled` | `TestWSUpgradeIsSameOriginOnly` |
| 27 | `27-ws-closes-without-handshake` | `TestWS` |
| 28 | `28-hub-double-close-allowed` | `TestHub` |
| 29 | `29-hub-unsubscribe-does-not-close` | *(already routed)* |
| 30 | `30-hub-slow-client-blocks` | *(already routed, alternation)* |
| 31 | `31-hub-broadcast-skips-a-subscriber` | *(already routed)* |
| 32 | `32-hub-count-escapes-the-hub-goroutine` | `TestHubConcurrentSubscribeUnsubscribeBroadcast` |
| 33 | `33-hub-dropped-client-not-removed` | *(already routed)* |
| 34 | `34-hub-removal-not-recorded` | `TestHub` |
| 35 | `35-ws-listener-never-subscribes` | *(already routed)* |
| 36 | `36-ws-subscriber-leaked-on-exit` | *(already routed)* |
| 37 | `37-ws-broadcast-trimmed` | *(already routed)* |
| 38 | `38-ws-write-deadline-removed` | **unrouted on purpose** (§3) |
| 39 | `39-ws-going-away-not-reported` | `TestWSConnectingAfterHubCloseIsClosedGoingAway` |
| 40 | `40-ws-client-close-not-noticed` | *(already routed)* |
| 41 | `41-ws-binary-frames` | `TestWSBroadcastReachesTheListenerAsExactBytes` |

Three rows could not be narrowed below a whole-file prefix — `06` and `08`
need `TestAPI`, `27` needs `TestWS`, `28` and `34` need `TestHub`. That is a
real coverage observation, not a survey failure: those defects are caught by
more than one test in the file, and the cheapest honest regex is the prefix.

## 7. The one dangerous row: mutation 18

`18-same-version-report-ignored` flips `<` to `<=` in the `lastEval` staleness
check in `srv/conductor.go`. It mutates **Conductor** code, so the obvious
regex is a Conductor test — and that is **wrong**:

```bash
MUTATION_TEST_ARGS="-run TestConductor" ./scripts/mutation-check.sh 18-same-version-report-ignored
# → SURVIVED   (the entire Conductor test file does not notice)

MUTATION_TEST_ARGS="-run TestConductorRecordEvalResult" ./scripts/mutation-check.sh 18-same-version-report-ignored
# → SURVIVED   (the single most on-the-nose test, by name, does not notice)

MUTATION_TEST_ARGS="-run TestAPIEvalResultSuccess" ./scripts/mutation-check.sh 18-same-version-report-ignored
# → caught
#     --- FAIL: TestAPIEvalResultSuccess (0.00s)
#           api_test.go:556: stored ok = true, want false
```

**Routing 18 to any Conductor test would have deleted its coverage silently**
— the mutation would report `SURVIVED` at grid level only if the regex were
the *whole suite*, and would report `caught` for the wrong reason if the
regex happened to match an unrelated API test. The defect is only observable
through the HTTP surface.

This is the strongest argument in this report for the discipline `.2.1.3`
established: a routing regex is an assertion about *which test owns a
defect*, and it is wrong more often than the mutation's file suggests. It is
also why §2 cross-checks every regex with `go test -list` **and** requires a
real `caught` — a plausible-looking regex that silently stops covering
anything is the exact failure mode that keeps the grid honest.

> **Superseded in part by `10-mutation18.md` (`strudel-agent-bgb`).** The
> sentence above — "The defect is only observable through the HTTP surface" —
> was true of the suite as it stood and is **no longer true**.
> `TestConductorRecordEvalResultSameVersionReplaces` now distinguishes `<` from
> `<=` directly, and mutation 18 is caught by the conductor tests themselves
> (`SURVIVED` → `caught`). The warnings here still stand in general: routing 18
> to a Conductor test would have been wrong, and wrong *silently*. But the
> reason is now that the owning test did not exist, not that the boundary is
> unobservable from the conductor. Mutation 18 deliberately still carries **no**
> 5th routing field.

## 8. Verdict

**Threshold: 300s** for a full local mutation-check run — the budget that
keeps the grid inside an interactive review loop rather than a CI-only job.
The same threshold `e2v.3.2` judged against, used deliberately so the two
reports are comparable.

**Is routing alone enough? YES, and it has already been spent.**

- Against the threshold: yes, comfortably. The grid is **79.5s** fully
  routed, against a 300s budget and kki.1's original 312.680s. This is no
  longer a CI-only job.
- As a *strategy*: no, there is nothing left here. `e2v.2.1` already routed
  the nine that mattered; the remaining 32 yield 48.5s and then stop, because
  77% of a routed run is `go test` startup, not test execution (§4).

So the cheap win is real but bounded, and it is banked. Routing is not the
lever that makes mutation checking fast in this repo — process startup is.
That is the finding `kki.3`–`kki.6` now have to beat, and it sharpens their
question: the remaining 79.5s can only be attacked by a tool that does not
pay 41 separate `go test` startups.

## 9. Provenance

| Number | Source |
|---|---|
| 312.680s, 3.606s | `01-baseline.*` (kki.1, untouched — sha256 verified unchanged) |
| 128.042s, 82.303s, per-mutation raws for the 9 | `09-new-baseline.*` (`e2v.3.2`) |
| 56.672s, 43.853s, 12.819s, the 32-row table | this task, `/tmp/kki2/timed.tsv` + `narrowbase.tsv` |
| 79.498s / 79.505s, "41 caught" | two clean whole-grid runs in `/tmp/kki2/wt` (removed) |
| 1.283/1.294/2.575/1.397s baseline spread | this task, 23 distinct narrow baselines |
| mutation 18 SURVIVED under `TestConductor` | quoted in §7 |

Constraints honoured: no production file, no test, and no script modified;
`scripts/mutation-check.sh` and the `Makefile` untouched in `main`; the only
mutation of the script was in a throwaway worktree, since removed; every run
`timeout`-bounded; no gomutants benchmarking (that is `kki.3`); no follow-up
issues created.
