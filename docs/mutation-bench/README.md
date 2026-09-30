# Mutation-bench evidence set

Indexed evidence for the two mutation-testing epics: `strudel-agent-e2v`
(fix the unbounded test waits that wedged the grid, then re-baseline) and
`strudel-agent-kki` (does gomutants make mutation checking faster?).

Reports are numbered by the *question* they answer, not by commit order, so
gaps are expected while subtasks are still open.

| File | Issue | Question it answers | Status |
|---|---|---|---|
| `01-baseline.md`, `01-baseline.tsv` | kki.1 | What does the curated 41-mutation grid cost today, per mutation? (whole grid cold **312.680s**, 3.606s baseline pass, mutation 30 alone 48.0%) | ✅ closed, frozen as the historical record |
| `03-gomutants.md` | kki.3 | What does gomutants discover/measure on this codebase, and at what wall time? (360 mutants found; 231.32s cold bounded run) | in progress, untracked |
| `07-unbounded-audit.md` | e2v.1.2 | Which test bounds sit *outside* the call they bound, so a wedge cannot reach them? (36 sites: 32 OK, 2 shape-A, 2 shape-B) | ✅ closed |
| `08-unbounded-fixes.md` | e2v.1.3.4 | Class-level proof for the unbounded-wait fixes: what fails, how fast, and with what message under a deliberately wedged hub? (10 clean failures at ~2.0s, **0** timeout panics) | ✅ closed |
| `08-non-vacuity.md` | e2v.3.1 | Did bounding the waits and adding routing silently delete coverage? (41/41 caught **routed** 128.858s and **unrouted** 352.302s; every fixed site re-wedged; no assertion weakened) | ✅ closed |
| `09-routing.md` | e2v.2.1.3 | Did the per-mutation `-run` routing drop any coverage? (41/41 caught routed, 133.92s; routing-typo safety proven) | ✅ closed |
| `09-new-baseline.md`, `09-new-baseline.tsv` | e2v.3.2 | New baseline after the fixes + routing, and how it compares to kki.1's 312.680s (whole grid cold **128.042s**, −59.1%, 41/41 caught) | ✅ closed |

Not yet written: `02-routing.md` (kki.2), `04-timeout-semantics.md` (kki.4),
`05-traceability.md` (kki.5), `06-decision.md` (kki.6).

## How to read the two baselines

- `01-baseline.*` — before: `330b34d`, unrouted grid, unbounded count path.
  Do not edit; it is the comparison denominator.
- `09-new-baseline.*` — after: `4cbb677`, routed grid, bounded count path.
  Raw per-mutation seconds and verdicts; sums and reconciliation are in the
  `.md`, verified with the stated `awk` commands.

## Reproducing

```bash
# Whole grid, cold (the headline number). Bound it; it needs no network.
timeout 900 ./scripts/mutation-check.sh

# One mutation, with the harness's own selection mode
timeout 300 ./scripts/mutation-check.sh 30-hub-slow-client-blocks

# Narrow the test run (5th pipe field per mutation takes precedence)
MUTATION_TEST_ARGS="-run TestIntegration" timeout 900 ./scripts/mutation-check.sh

make verify          # the repo gate every report must leave green
```

Never run two `mutation-check` invocations concurrently (see `09-routing.md`
gotcha 4 — it produces spurious timeout panics). Measure in a git worktree or
clone when a mutation must be applied by hand; a plain copy cannot build
`./cmd/srv`.
