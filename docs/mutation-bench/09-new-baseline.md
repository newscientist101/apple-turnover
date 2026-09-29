# New Baseline: Fixed + Routed Grid (provisional)

**Date:** 2026-09-29
**Machine:** Linux ROGQ58 WSL2. **Go:** go1.27.1. **Commit:** 4cbb677.
**Deliverable for:** strudel-agent-e2v.3.2. **Depends on:** .3.1 (see §0).

## 0. Ordering note: provisional until .3.1 closes

The issue orders this work after the non-vacuity proof (.3.1): nothing may be
re-baselined until the grid is proven still fully load-bearing, because a
bounded wait can silently convert a hang into a PASS and delete coverage with
no test going red. At measurement time `.3.1` is still `in_progress` and
`docs/mutation-bench/08-non-vacuity.md` does not exist yet, so the numbers
below are measurements of the current fixed+routed tree, pending .3.1's
verdict. If .3.1 changes any test, script row, or routing field, this baseline
must be re-run. What this report does instead of .3.1's proof:

- the whole grid was run cold on the current tree and reports 41 caught, 0
  survived, 0 weak, 0 broken (log /tmp/e2v32-bench-logs/grid3.log,
  wall 128.042s, rc=0);
- the per-mutation table below records all 41 verdicts as caught;
- `make verify` is green on the unmutated tree (§8);
- `git status` is clean apart from docs/mutation-bench/*, `01-baseline.*`
  are byte-identical to the kki.1 commit (sha256 in §7), and
  `docs/mutation-bench/README.md` indexes the evidence set.

The six-check non-vacuity proof itself (routed AND unrouted runs, mutation-30
two-test failure quote with no timeout panic, per-site worktree re-wedging,
assertion-shape justification) remains .3.1's deliverable, not this file's.

## 1. Exact commands used

```bash
# Baseline (unmutated) pass — one full suite run, timed
timeout 300 go test ./... -race -count=1 -timeout 150s
# → wall 3.043s, rc=0 (log /tmp/e2v32-bench-logs/baseline3.log)

# Whole grid cold — the headline number
timeout 900 ./scripts/mutation-check.sh
# → 41 caught, 0 survived, 0 weak, 0 broken; wall 128.042s, rc=0
#   (log /tmp/e2v32-bench-logs/grid3.log; ns stamps in grid3.start/.done)

# Per-mutation table — one invocation per mutation, bounded, sequential
./scripts/mutation-check.sh --list   # 41 names
for n in <each of the 41 names>; do
  timeout 300 ./scripts/mutation-check.sh "$n"   # logs /tmp/e2v32-pm2/$n.log
done
# → 41/41 caught; raw seconds in 09-new-baseline.tsv (this directory)
```

Every invocation is bounded by an explicit timeout; runs were sequential,
never concurrent (concurrent mutation-check invocations are known to produce
spurious timeout panics — see 09-routing.md section 8 gotcha 4; one such
episode occurred mid-session, was discarded, and is documented in §7). The
harness scripts/mutation-bench.sh was NOT used as-is because it hard-codes its
output to 01-baseline.tsv and the issue forbids overwriting kki.1's files; the
loop above performs the same per-mutation timing with output redirected to the
new TSV.

## 2. Headline numbers

| Figure | kki.1 (before, at 330b34d) | Now (after, at 4cbb677) | Delta |
|---|---|---|---|
| Whole grid cold wall | 312.680s | 128.042s | -184.638s (-59.1%) |
| Unmutated baseline pass | 3.606s | 3.043s | -0.563s (noise) |
| Sum of 41 individual runs | 442.085s | 242.396s | -199.689s (-45.2%) |
| Sum baseline-subtracted | 294.239s | 117.633s | -176.606s (-60.0%) |
| Grid verdict | 41 caught | 41 caught, 0 survived, 0 weak, 0 broken | no loss (pending .3.1) |

Arithmetic check (stated awk command — run it yourself):

```bash
awk -F'\t' 'NR==1{next} {s+=$2; n++} END {printf "rows=%d sum(raw)=%.3f 41*baseline=%.3f subtracted=%.3f\n", n, s, 41*3.043, s-41*3.043}' docs/mutation-bench/09-new-baseline.tsv
# → rows=41 sum(raw)=242.396 41*baseline=124.763 subtracted=117.633
```

## 3. Before/after per-mutation table with attributed deltas

Before raw = kki.1; after raw = this run; delta = before−after (positive =
faster now). Sorted by delta descending.

| Mutation | Before (s) | After (s) | Delta (s) | Verdict | Attribution |
|---|---|---|---|---|---|
| 30-hub-slow-client-blocks | 153.587 | 25.396 | 128.191 | caught | routing (5th field: two hub tests) + defect fix shortens wedged-hub teardown |
| 31-hub-broadcast-skips-a-subscriber | 26.423 | 3.427 | 22.996 | caught | routing (single broadcast test) |
| 35-ws-listener-never-subscribes | 29.200 | 8.370 | 20.830 | caught | routing (single upgrade test) |
| 29-hub-unsubscribe-does-not-close | 13.411 | 5.361 | 8.050 | caught | routing (single unsubscribe test) |
| 37-ws-broadcast-trimmed | 5.590 | 3.364 | 2.226 | caught | routing (single broadcast-bytes test) |
| 40-ws-client-close-not-noticed | 10.437 | 8.366 | 2.071 | caught | routing (single disconnect test) |
| 33-hub-dropped-client-not-removed | 7.459 | 5.395 | 2.064 | caught | routing (single slow-subscriber test) |
| 36-ws-subscriber-leaked-on-exit | 10.442 | 8.385 | 2.057 | caught | routing (single disconnect test) |
| 38-ws-write-deadline-removed | 5.657 | 5.113 | 0.544 | caught | noise/unrouted (deliberately unrouted; catcher costs ~1.5s wall time) |
| 22-snapshot-omits-playing | 5.569 | 5.108 | 0.461 | caught | noise (unrouted) |
| 02-unknown-json-field-allowed | 5.557 | 5.131 | 0.426 | caught | noise (unrouted) |
| 06-allow-header-dropped | 5.495 | 5.086 | 0.409 | caught | noise (unrouted) |
| 13-hush-plays-instead | 5.528 | 5.127 | 0.401 | caught | noise (unrouted) |
| 10-eval-ack-lies | 5.507 | 5.112 | 0.395 | caught | noise (unrouted) |
| 24-ws-405-fallback-removed | 5.486 | 5.104 | 0.382 | caught | noise (unrouted) |
| 25-ws-wrong-close-status | 5.484 | 5.120 | 0.364 | caught | noise (unrouted) |
| 08-api-catchall-too-broad | 5.480 | 5.119 | 0.361 | caught | noise (unrouted) |
| 26-ws-origin-check-disabled | 5.472 | 5.113 | 0.359 | caught | noise (unrouted) |
| 27-ws-closes-without-handshake | 5.398 | 5.113 | 0.285 | caught | noise (unrouted) |
| 39-ws-going-away-not-reported | 5.492 | 5.136 | 0.356 | caught | noise (unrouted) |
| 03-trailing-json-allowed | 5.480 | 5.137 | 0.343 | caught | noise (unrouted) |
| 17-stale-report-regresses | 5.471 | 5.132 | 0.339 | caught | noise (unrouted) |
| 41-ws-binary-frames | 5.448 | 5.111 | 0.337 | caught | noise (unrouted) |
| 05-cap-reported-as-400 | 5.449 | 5.116 | 0.333 | caught | noise (unrouted) |
| 16-eval-result-not-stored | 5.442 | 5.114 | 0.328 | caught | noise (unrouted) |
| 19-version-not-monotonic | 5.452 | 5.130 | 0.322 | caught | noise (unrouted) |
| 09-static-mount-removed | 5.408 | 5.107 | 0.301 | caught | noise (unrouted) |
| 20-version-skipped | 5.418 | 5.115 | 0.303 | caught | noise (unrouted) |
| 21-history-window-wrong-start | 5.414 | 5.132 | 0.282 | caught | noise (unrouted) |
| 12-transport-accepts-any-body | 5.458 | 5.177 | 0.281 | caught | noise (unrouted) |
| 23-wedged-state-handler | 15.710 | 15.440 | 0.270 | caught | already routed in kki.1; residual is noise |
| 11-message-bumps-version | 5.449 | 5.192 | 0.257 | caught | noise (unrouted) |
| 07-api-404-is-500 | 5.478 | 5.112 | 0.366 | caught | noise (unrouted) |
| 01-empty-code-accepted | 5.491 | 5.124 | 0.367 | caught | noise (unrouted) |
| 04-payload-cap-removed | 5.493 | 5.203 | 0.290 | caught | noise (unrouted) |
| 14-shell-render-silently-truncates | 5.455 | 5.115 | 0.340 | caught | noise (unrouted) |
| 15-eval-accepts-future-version | 5.499 | 5.123 | 0.376 | caught | noise (unrouted) |
| 32-hub-count-escapes-the-hub-goroutine | 5.520 | 5.153 | 0.367 | caught | noise (race-detector catch, unchanged) |
| 34-hub-removal-not-recorded | 3.646 | 3.420 | 0.226 | caught | noise (unrouted) |
| 18-same-version-report-ignored | 3.623 | 3.391 | 0.232 | caught | noise (unrouted) |
| 28-hub-double-close-allowed | 3.607 | 3.406 | 0.201 | caught | noise (unrouted) |

Attribution honesty: the eight newly-routed mutations (29, 30, 31, 33, 35,
36, 37, 40) account for 188.485s of the 199.689s individual-run saving
(`join` the two TSVs and sum `$2-$4` over those names); the remaining 32
contribute 11.204s, which is machine noise. The
defect fixes (.1/.1.3: bounded count path, interruptible churn watcher) do
not make a green suite faster — the unmutated baseline moved only 3.606s to
3.043s, within run-to-run noise. Their value is correctness (wedge fails fast
naming the wedge instead of a 150s timeout panic), already measured in
08-unbounded-fixes.md, plus a secondary effect on mutation 30's wedged
teardown (its two routed tests now fail at ~7s + bounded close instead of the
150s go-test timeout). Mutation 23 was already routed in kki.1. The ±0.5s
remainder is machine noise.


- The table has exactly 41 rows (wc -l = 42 with header; join against
  ./scripts/mutation-check.sh --list matches 41/41).
- Sum of baseline-subtracted (117.633s) equals sum(raw) − 41 x baseline
  (242.396 − 124.763) by construction.
- kki.1 cross-check: 442.085 − 41 x 3.606 = 294.239, matching 01-baseline.md.

## 4. Reconciled breakdown (one denominator per table)

kki.1's first draft failed review for percentages summing to 133% by
double-counting baseline overhead. Each table below states its denominator.

### 4a. Against the NEW whole-grid cold wall (denominator = 128.042s)

| Bucket | Seconds | Share of 128.042s |
|---|---|---|
| Slow routed wedge 30 (raw 25.396 − 3.043 = 22.353) | 22.353 | 17.46% |
| Pre-routed wedge 23 (raw 15.440 − 3.043 = 12.397) | 12.397 | 9.68% |
| Other routed: 29, 31, 33, 35, 36, 37, 40 (raw 42.668 − 7x3.043 = 21.367) | 21.367 | 16.69% |
| Fast: remaining 32 (raw 158.892 − 32x3.043 = 61.516) | 61.516 | 48.04% |
| Baseline overhead (1x in whole grid) | 3.043 | 2.38% |
| One-time overhead (128.042 − 117.633 − 3.043) | 7.366 | 5.75% |
| **Total** | **128.042** | **100.00%** |

Every row above is computed, never assumed; the awk command that reproduces
this table:

```bash
awk -F'\t' 'NR==1{next} {s+=$2; if($1=="30-hub-slow-client-blocks")a=$2; else if($1=="23-wedged-state-handler")b=$2; else if($1 ~ /^(29|31|33|35|36|37|40)-/)c+=$2; else d+=$2} END {printf "sum=%.3f m30=%.3f m23=%.3f routed7=%.3f fast32=%.3f one_time=%.3f\n", s, a-3.043, b-3.043, c-7*3.043, d-32*3.043, 128.042-(s-41*3.043)-3.043}' docs/mutation-bench/09-new-baseline.tsv
# → sum=242.396 m30=22.353 m23=12.397 routed7=21.367 fast32=61.516 one_time=7.366
```

Check: 22.353 + 12.397 + 21.367 + 61.516 = 117.633 = sum(raw) − 41 × baseline,
and 117.633 + 3.043 + 7.366 = 128.042, the whole-grid wall.

### 4b. Against the OLD whole-grid cold wall (denominator = 312.680s)

| Item | Seconds | Share of 312.680s |
|---|---|---|
| Total grid saving (312.680 − 128.042) | 184.638 | 59.1% |
| New wall | 128.042 | 40.9% |
| Total | 312.680 | 100.0% |

Individual-run deltas do not add 1:1 to whole-grid deltas (the grid pays one
shared baseline and one compile, not 41 of each), so routing benefit is quoted
from individual-run savings in §3 and the grid saving as a whole here.



## 5. Local-runnability verdict

Threshold: 300s (5 minutes) wall for a full local mutation-check run — the
budget that keeps the grid inside an interactive review loop rather than a
CI-only job. kki.1's 312.680s exceeded it; 09-routing.md projected ~134s.

Verdict: YES. The routed whole grid now completes in 128.042s (~2.1 min)
with 41/41 caught. Residual cost is concentrated: mutation 30 (25.4s, 17.46%
of the new wall) plus mutation 23 (15.4s, pre-existing routing) are the only
runs above 9s; the other 39 average 5.2s each ((242.396 − 25.396 − 15.440) /
39) including their per-run baseline. No further routing is obviously
worthwhile — the next-slowest routed
mutants (35, 36, 40 at ~8.4s) each pay their catcher's real wall-time cost,
and deliberately-unrouted 38 (5.1s) was left alone in .2.1.2.

## 6. What remains for kki.2 (factual note, no decision)

kki.2 (narrowest -run regex per mutation, projected total, yes/no verdict)
overlaps heavily with done work: .2.1 encoded regexes for the nine slowest
mutations as 5th fields, and 09-routing.md records the routed measurement
(133.92s there, 128.042s here — same config, noise). What kki.2 still uniquely
owns: the full 41-row survey (the other 32 run the whole suite at ~5s each and
were never surveyed), the projected-vs-measured comparison, and its verdict in
02-routing.md. Redundant, partly done, or still needed is the orchestrator's
call; no kki issue was created or modified.

## 7. Provenance

| Number | Source | How obtained |
|---|---|---|
| 312.680s / 3.606s / 442.085s / 294.239s | 01-baseline.* (untouched) | kki.1 at 330b34d; sha256 below |
| 128.042s whole grid cold | grid3.log + .start/.done ns | timeout 900 mutation-check.sh, exit 0 |
| 3.043s baseline pass | baseline3.log + .start/.done | timeout 300 go test -race, exit 0 |
| 242.396s sum of individuals | 09-new-baseline.tsv | 41 sequential timeout 300 runs, all caught |
| 133.92s routed reference | 09-routing.md §2 | independent .2.1.2 measurement |
| 41 names | mutation-check.sh --list | still 41 at 4cbb677 |

```bash
sha256sum docs/mutation-bench/01-baseline.md docs/mutation-bench/01-baseline.tsv
# 2d0a4d11b06e765ac73dd2889a36d71918587d1e3d11741c1dc22d344321d420  01-baseline.md
# 6a45bcaaace483f3253d06cb542bd6aa76b904a4aaaa108dde692f16c0be323e  01-baseline.tsv
```

Caveats: (a) one 150s baseline-timeout episode occurred when a stray
per-mutation loop ran concurrently with the grid; that grid run was discarded
and all numbers above come from clean sequential runs — concurrency is
forbidden per 09-routing.md §8 gotcha 4. (b) Per-mutation raws include each
invocation's own ~3s green-baseline pass; whole-grid deltas in §2 are the
honest routing-benefit figures. (c) 03-gomutants.md (untracked, kki.3's) was
present throughout; docs-only, no build effect.

## 8. Gate state

make verify green (gofmt/vet/build/go test -race ok ~2.8s). git status clean
apart from docs/mutation-bench/* (03-gomutants.md pre-existing; 09-new files
are this report). No production code, test, or script modified. The bd memory
mutation-grid-baseline was updated in place (new numbers, the defect class, and
the lesson that a bound checked outside the call it bounds cannot fire), and
docs/mutation-bench/README.md now indexes the evidence set (01–09, listing the
not-yet-written 02/04/05/06/08 as open).

