# Mutation-bench evidence set

Indexed evidence for the two mutation-testing epics: `strudel-agent-e2v`
(fix the unbounded test waits that wedged the grid, then re-baseline) and
`strudel-agent-kki` (does gomutants make mutation checking faster?).

Reports are numbered by the *question* they answer, not by commit order, so
gaps are expected while subtasks are still open.

| File | Issue | Question it answers | Status |
|---|---|---|---|
| `01-baseline.md`, `01-baseline.tsv` | kki.1 | What does the curated 41-mutation grid cost today, per mutation? (whole grid cold **312.680s**, 3.606s baseline pass, mutation 30 alone 48.0%) | ✅ closed, frozen as the historical record |
| `02-routing.md` | kki.2 | Is per-mutation routing alone enough to make the grid locally runnable? (41-row survey; all 41 routed = **79.5s**; but 77% of a routed run is `go test` startup, not tests). §7 superseded in part by `10-mutation18.md` | ✅ closed |
| `03-gomutants.md` | kki.3 | What does gomutants discover/measure on this codebase, and at what wall time? (360 mutants found; 231.32s cold bounded run; WARM 2.52s; efficacy 77.07% / coverage 94.86%) | ✅ closed |
| `04-timeout-semantics.md` | kki.4 | **Can gomutants catch a HANG-shaped defect?** (No. Wedge in the baseline → exit 1, no verdict, 600.30s. Wedge as a mutant → **3 infinite loops `TIMED OUT`, exit 0, efficacy 81.82%, coverage 100%** — and the verdict is cached and replayed. The curated grid catches both hang mutations with named assertions.) | ✅ closed |
| `05-traceability.md` | kki.5 | How much of the curated 41-mutation grid can gomutants *express*? (**18/41** traceable and killed — 10 exact, 8 equivalent at the same anchor; **23/41** NOT-GENERATED, 8 with no mutant on any anchor line and all 10 `ws.go` rows among them; 300 of the 357 judged mutants are off the anchor lines, 66 of them LIVED) | ✅ closed |
| `06-decision.md` | kki.6 | **The decision: faster or not, adopt or not.** gomutants is **1.8× slower cold** (231.32s for 242 mutants vs the routed grid's 128.042s for all 41) and only faster on a warm unchanged tree (2.52s; after a real edit: never measured). It **cannot replace the grid**: a gomutants gate passes a deliberate deadlock (exit 0, 81.82% efficacy, verdict cached) and 23/41 curated mutations are inexpressible. **Recommendation (d) hybrid** — the grid keeps exclusive ownership of the hang-shaped and domain invariants and remains the gate; gomutants is a non-gating breadth pass in the dev loop | ✅ closed |
| `07-unbounded-audit.md` | e2v.1.2 | Which test bounds sit *outside* the call they bound, so a wedge cannot reach them? (36 sites: 32 OK, 2 shape-A, 2 shape-B) | ✅ closed |
| `08-unbounded-fixes.md` | e2v.1.3.4 | Class-level proof for the unbounded-wait fixes: what fails, how fast, and with what message under a deliberately wedged hub? (10 clean failures at ~2.0s, **0** timeout panics) | ✅ closed |
| `08-non-vacuity.md` | e2v.3.1 | Did bounding the waits and adding routing silently delete coverage? (41/41 caught **routed** 128.858s and **unrouted** 352.302s; every fixed site re-wedged; no assertion weakened) | ✅ closed |
| `09-routing.md` | e2v.2.1.3, re-verified at e2v.2 | Did the per-mutation `-run` routing drop any coverage? (41/41 caught routed, 133.92s; routing-typo safety proven; §10 re-runs the grid at `0551908`: 41/41 caught, 128.14s, 0 panics, harness-only run vacuous for the 9 routed mutations) | ✅ closed |
| `09-new-baseline.md`, `09-new-baseline.tsv` | e2v.3.2 | New baseline after the fixes + routing, and how it compares to kki.1's 312.680s (whole grid cold **128.042s**, −59.1%, 41/41 caught) | ✅ closed |

| `10-mutation18.md` | bgb | Was mutation 18 caught only by an unrelated API test? (`SURVIVED` under `-run TestConductor` → now `caught` by `TestConductorRecordEvalResultSameVersionReplaces`; the old catch was partly a panic) | ✅ closed |

Not yet written: nothing — the set is complete (01–10).

## Where to start

- **Deciding whether to use gomutants at all?** Read `06-decision.md` alone; it
  synthesises 01–05 and states the recommendation. The other reports are its
  evidence.
- **Re-measuring the grid today?** `09-new-baseline.{md,tsv}` (128.042s, 41/41
  caught) is the current denominator; `01-baseline.*` is the frozen historical
  one, kept for the −59.1 % comparison.
- **Checking that a mutation is genuinely caught?** `08-non-vacuity.md` (41/41
  routed *and* unrouted) and `09-routing.md` (routing cannot delete coverage) are
  the two non-vacuity proofs; `07-unbounded-audit.md` and `08-unbounded-fixes.md`
  are the bounded-wait work that made the timings trustworthy.
- **Understanding gomutants' verdicts before quoting one?** `03-gomutants.md`
  §1 (efficacy excludes `TIMED OUT`) and §7 (a `PENDING` run still exits 0) are
  the two traps; `04-timeout-semantics.md` is the one that disqualifies it as a
  gate.

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
