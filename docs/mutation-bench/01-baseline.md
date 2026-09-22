# Mutation Grid Baseline Benchmark

## Machine and Environment

- **System**: Linux ROGQ58 6.18.33.2-microsoft-standard-WSL2 (WSL2)
- **CPU**: 12th Gen Intel(R) Core(TM) i7-12700K
- **Memory**: 7.6Gi total, 6.9Gi available
- **Go version**: go1.27.1 linux/amd64
- **Date**: 2026-09-22

## Exact Commands Used

```bash
# Run the benchmark harness
timeout 2700 ./scripts/mutation-bench.sh 2>&1 | tee /tmp/bench.log

# The harness internally runs:
# 1. Baseline measurement: timeout 300 go test ./... -race -count=1 -timeout 150s
# 2. Whole grid cold: timeout 900 ./scripts/mutation-check.sh
# 3. Per-mutation: timeout 300 ./scripts/mutation-check.sh <name> (for each of 41 mutations)
```

## Whole Grid Cold Total

**312.680 seconds** (exit 0, all mutations caught)

This is within 1% of the remembered ~315s, confirming the baseline estimate was accurate. The small difference (2.3s) is within normal run-to-run variance for a test suite of this size.

## Baseline Pass Overhead

**3.606 seconds** for the unmutated baseline test run.

This is the cost that `mutation-check.sh` pays before mutating anything — it runs the full test suite once to prove it was green to begin with. Every individual mutation invocation pays this overhead, so per-mutation timings are inflated by ~3.6s each.

## Full 41-Mutation Table (Sorted by Raw Seconds Descending)

| Name | Raw Seconds | Baseline-Subtracted | Verdict |
|------|-------------|---------------------|---------|
| 30-hub-slow-client-blocks | 153.587 | 149.981 | caught |
| 35-ws-listener-never-subscribes | 29.200 | 25.594 | caught |
| 31-hub-broadcast-skips-a-subscriber | 26.423 | 22.817 | caught |
| 23-wedged-state-handler | 15.710 | 12.104 | caught |
| 29-hub-unsubscribe-does-not-close | 13.411 | 9.805 | caught |
| 36-ws-subscriber-leaked-on-exit | 10.442 | 6.836 | caught |
| 40-ws-client-close-not-noticed | 10.437 | 6.831 | caught |
| 33-hub-dropped-client-not-removed | 7.459 | 3.853 | caught |
| 38-ws-write-deadline-removed | 5.657 | 2.051 | caught |
| 37-ws-broadcast-trimmed | 5.590 | 1.984 | caught |
| 22-snapshot-omits-playing | 5.569 | 1.963 | caught |
| 02-unknown-json-field-allowed | 5.557 | 1.951 | caught |
| 13-hush-plays-instead | 5.528 | 1.922 | caught |
| 32-hub-count-escapes-the-hub-goroutine | 5.520 | 1.914 | caught |
| 10-eval-ack-lies | 5.507 | 1.901 | caught |
| 15-eval-accepts-future-version | 5.499 | 1.893 | caught |
| 06-allow-header-dropped | 5.495 | 1.889 | caught |
| 04-payload-cap-removed | 5.493 | 1.887 | caught |
| 39-ws-going-away-not-reported | 5.492 | 1.886 | caught |
| 01-empty-code-accepted | 5.491 | 1.885 | caught |
| 24-ws-405-fallback-removed | 5.486 | 1.880 | caught |
| 25-ws-wrong-close-status | 5.484 | 1.878 | caught |
| 08-api-catchall-too-broad | 5.480 | 1.874 | caught |
| 03-trailing-json-allowed | 5.480 | 1.874 | caught |
| 07-api-404-is-500 | 5.478 | 1.872 | caught |
| 26-ws-origin-check-disabled | 5.472 | 1.866 | caught |
| 17-stale-report-regresses | 5.471 | 1.865 | caught |
| 12-transport-accepts-any-body | 5.458 | 1.852 | caught |
| 14-shell-render-silently-truncates | 5.455 | 1.849 | caught |
| 19-version-not-monotonic | 5.452 | 1.846 | caught |
| 11-message-bumps-version | 5.449 | 1.843 | caught |
| 05-cap-reported-as-400 | 5.449 | 1.843 | caught |
| 41-ws-binary-frames | 5.448 | 1.842 | caught |
| 16-eval-result-not-stored | 5.442 | 1.836 | caught |
| 20-version-skipped | 5.418 | 1.812 | caught |
| 21-history-window-wrong-start | 5.414 | 1.808 | caught |
| 09-static-mount-removed | 5.408 | 1.802 | caught |
| 27-ws-closes-without-handshake | 5.398 | 1.792 | caught |
| 34-hub-removal-not-recorded | 3.646 | 0.040 | caught |
| 18-same-version-report-ignored | 3.623 | 0.017 | caught |
| 28-hub-double-close-allowed | 3.607 | -0.000 | caught |

**Sum of individual runs**: 442.085s  
**Sum of baseline-subtracted**: 294.239s  
**Baseline overhead total**: 41 × 3.606s = 147.846s (each of the 41 individual runs pays its own baseline cost)

## TIMEOUT-BOUND Mutants

Mutants whose runtime is pinned near `GO_TEST_TIMEOUT` (150s) or `MUTATION_TIMEOUT` (180s) rather than near a normal test run:

- **30-hub-slow-client-blocks**: 153.587s raw (149.981s baseline-subtracted)

This single mutant's share of wall time:
- **49.1% of the whole-grid cold run** (153.587s / 312.680s)
- **34.7% of the sum-of-individual-runs** (153.587s / 442.085s)

All other mutants complete well below the timeout thresholds. The next slowest is 35-ws-listener-never-subscribes at 29.200s, which is 5× faster than the timeout-bound mutant.

## Reconciled Breakdown: Where the Time Goes

Using baseline-subtracted seconds for per-mutation cost, with aggregate baseline overhead as a separate bucket. Components sum to 442.085s (the sum of 41 individual runs).

| Bucket | Mutants | Seconds | Share |
|--------|---------|---------|-------|
| Timeout-bound (>100s baseline-subtracted): 30 | 1 | 149.981 | 33.9% |
| Slow (5–100s baseline-subtracted): 35, 31, 23, 29, 36, 40 | 6 | 83.987 | 19.0% |
| Elevated (2–5s baseline-subtracted): 33, 38 | 2 | 5.904 | 1.3% |
| Fast (<2s baseline-subtracted): remaining 32 | 32 | 54.367 | 12.3% |
| Baseline overhead: 41 × 3.606s | — | 147.846 | 33.4% |
| **Total** | **41** | **442.085** | **100.0%** |

Against the **whole-grid cold wall time** of 312.680s (which pays the baseline only once, plus ~14.8s of one-time overhead for initial compile and script startup):

| Bucket | Seconds | Share of 312.680s |
|--------|---------|-------------------|
| Timeout-bound: 30 | 149.981 | 48.0% |
| Slow: 35, 31, 23, 29, 36, 40 | 83.987 | 26.9% |
| Elevated: 33, 38 | 5.904 | 1.9% |
| Fast: remaining 32 | 54.367 | 17.4% |
| Baseline overhead (1× in whole grid) | 3.606 | 1.2% |
| One-time overhead (compile, startup) | 14.835 | 4.7% |
| **Total** | **312.680** | **100.0%** |

Note: 312.680 − 294.239 (sum of baseline-subtracted) − 3.606 (one baseline pass) = 14.835s of one-time overhead. The whole grid saves 40 redundant baseline runs (144.240s) compared to running each mutation individually.

## Where Does the Grid's Time Actually Go?

The grid's time is dominated by a single timeout-bound mutant: **30-hub-slow-client-blocks** consumes 149.981s baseline-subtracted — **48.0% of the whole-grid cold wall time** (312.680s) — because the mutation turns the hub's non-blocking broadcast into a blocking send, causing tests to hang until the `GO_TEST_TIMEOUT` of 150s expires. Seven mutants in total (30, 35, 31, 23, 29, 36, 40) account for 233.968s baseline-subtracted, or 74.9% of the whole-grid wall time. The remaining 34 mutants average 1.77s each baseline-subtracted and are not the problem.

Crucially, mutation 30 has **no** per-mutation `-run` routing in the mutation table (its pipe-delimited record has only 4 fields). It runs the whole suite and pays the full timeout. It is mutation **23**-wedged-state-handler that already carries a 5th `-run` field (`TestIntegrationParallelPushesAreBoundedAndContiguous`) to limit its deliberately-wedged `select {}` to one test. This makes 30-hub-slow-client-blocks the single best candidate for adding `-run` routing in subtask .2.

The baseline overhead (3.606s per individual run, 147.8s across 41 runs) is the second-largest cost bucket at 33.4% of the sum-of-individuals, though in the whole-grid run it is paid only once (3.606s, 1.2% of 312.680s).