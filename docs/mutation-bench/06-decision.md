# Decision: is gomutants faster, and may it replace the curated grid?

**Issue:** strudel-agent-kki.6 (epic strudel-agent-kki)
**Date:** 2026-09-30
**Machine:** Linux ROGQ58 6.18.33.2-microsoft-standard-WSL2 (WSL2), 12th Gen
Intel(R) Core(TM) i7-12700K as **6 vCPUs**, **7.6 GiB RAM**. Same host as every
measurement cited here.
**Go:** go1.27.1 linux/amd64 · **Tool under test:** `gomutants` v0.6.1 ·
**Incumbent:** `scripts/mutation-check.sh` (41 curated mutations)

**This document ran no measurement.** Every number is cited to the subtask that
produced it, with the commit that number was taken at. That matters here because
the reports were taken at four different commits: `330b34d` (kki.1), `b2d4280`
(kki.3, kki.4), `19ea479`/`8a908d7` (kki.5) and `4cbb677` (e2v.3.2). Per the
epic's own cross-epic note, kki.3's figures predate epic `rka`; this decision
therefore rests on a *pre-rka* measurement of gomutants and says so where it
matters. No production file was modified, nothing was adopted, and no follow-up
issue was created.

---

## 0. Conclusion

**Recommendation: (d) hybrid.** `scripts/mutation-check.sh` stays the gate and
keeps *exclusive* ownership of the hang-shaped and domain invariants;
`gomutants` is worth running for **breadth in the dev loop only**, at kki.3's
bounded configuration, and never as a `make verify` step.

The two halves of the question have opposite answers, and that is the whole
finding:

* **Faster? No, except on a warm, unchanged tree.** Cold, gomutants costs
  **231.32 s** for 242 mutants against the routed grid's **128.042 s** for all
  41 — **1.8x slower**. Warm on a tree nobody has touched it is **2.52 s**,
  which is real and reproducible, but it is the wrong regime: a gate runs *after*
  an edit, and no gomutants run after a real edit was ever timed (§1).
* **Adoptable as the gate? No, decisively.** A gomutants gate **passes a
  deliberate deadlock** — measured exit **0**, efficacy **81.82 %**, coverage
  **100 %** with three infinite loops in the tree, and the verdict then cached
  and replayed (kki.4 §4). Independently, **23 of the 41** curated mutations have
  no gomutants mutant expressing the same defect, including every `ws.go` row and
  both hang rows (kki.5 §4).

So the speed argument for replacing the grid does not exist, and the coverage
argument against it is measured rather than inferred. Breadth is real, cheap to
harvest non-gating, and is the one thing the curated grid structurally cannot
give (kki.5 §6) — which is exactly what a hybrid keeps.

---

## 1. Timing: all five rows, one machine, wall-clock seconds

Every row cites the subtask, the exact command, and the commit it was taken at.
No row is a projection except where the table says so.

| # | Configuration | Wall | Source (issue → doc → commit) | Command |
|---|---|---:|---|---|
| 1 | Current grid, **cold, unrouted** | **312.680 s** | kki.1 → `01-baseline.md` @ `330b34d` | `timeout 900 ./scripts/mutation-check.sh` |
| 2 | Current grid, cold, **routed as the tree stands** (9 of 41 have a 5th `-run` field) | **128.042 s** | e2v.3.2 → `09-new-baseline.md` @ `4cbb677` | same command, sequential per-mutation |
| 2b | Current grid, cold, **all 41 routed** — measured in a throwaway worktree, **not adopted into `main`** | **79.5 s** (79.498 / 79.505, noise floor 0.007 s) | kki.2 → `02-routing.md` §5 @ `2d9834d` | `git worktree add /tmp/kki2/wt HEAD` + 5th fields, `timeout 900 ./scripts/mutation-check.sh` |
| 3 | gomutants, **cold** | **231.32 s** (242-mutant sweep) | kki.3 → `03-gomutants.md` §5.1 @ `b2d4280` | `gomutants -w 1 --exclude-files "conductor\.go$" -o=… -cache=… ./srv/...` |
| 4 | gomutants, **warm, unchanged tree** | **2.52 s** (91.8x) | kki.3 → `03-gomutants.md` §5.1 @ `b2d4280` | *the identical command again* — all 242 verdicts cached |
| 5 | gomutants, **warm after a real edit** | **NOT MEASURED** | — | — |

**Row 5 is a gap, stated rather than guessed.** kki.3 §7 documents the cache key
— content hash of the mutated production file, the covering tests, the package
and the `--test-flags` value; whitespace-only edits do not invalidate — so the
*mechanism* by which a real edit narrows the warm run is known and reproduced
from the tool's own help output. The *wall time* of such a run was never
measured: kki.3 reports the unchanged-tree rerun only. kki.3 §5.1 does argue from
the keying rule that this is "the relevant one for a pre-push gate and the cold
number is the one that matters after a rebase" — an argument, not a number. No
estimate is offered here.

**Row 3 is not a whole-codebase number, and must not be read as one.** It
excludes `conductor.go` deliberately: one of that file's mutants
(`(*Conductor).snapshotLocked:STATEMENT_REMOVE#1`) reaches **2.5 GB in ~7 s** and
OOM-kills any cgroup smaller than that, so no single sweep can judge the file's
101 testable mutants. They were measured individually instead — **101
invocations, 493 s all-in, ~4.9 s mean, 7 s max** — but each invocation re-pays
gomutants' fixed phases (~5.6 s: package resolve, coverage, baseline, discovery),
so that series is **not summable** into a sweep wall time. kki.3 §6's own
instruction to `.6` is explicit: *"Any `.6` denominator must use this reading, not
a projection."* The honest aggregate is therefore: **a complete one-command
gomutants sweep of all 343 testable mutants is not achievable on this machine,
and the measured cost of judging them is ≥ 231 s plus the `conductor.go` test
time.** The comparison in row 3 is deliberately the most favourable honest one
available — 242 auto-generated mutants against 41 hand-written ones — and gomutants
still loses it.

### The literal question, in one sentence

**No: gomutants is faster only in the warm-unchanged-tree regime (2.52 s vs
128.042 s, ~51x), it is 1.8x *slower* than the routed grid when cold
(231.32 s vs 128.042 s), and in the only regime that matters to a gate — after a
real edit — it was never measured.**

Row 2b deserves a note, because it is the strongest honest argument *against*
this report's own conclusion. Full 41-row routing buys 128.042 s → 79.5 s
(−37.9 %), and kki.2's verdict is that the cheap option is then exhausted: of
79.5 s, ~53 s is irreducible per-invocation `go test` startup (41 invocations × a
~1.3 s floor; median narrow baseline 1.294 s, min 1.283 s, max 2.575 s), and only
12.819 s of 56.672 s is attributable to tests actually running. That is why
kki.2 said buying a different tool was "the remaining question for
`kki.3`–`kki.6`". This report answers it: the different tool is not cheaper, and
the floor that routing cannot touch is the same floor gomutants cannot touch,
because gomutants re-pays the same coverage and baseline phases on every run.

---

## 2. Coverage: could gomutants replace the grid with zero invariant loss?

### 2.1 Hang-shaped invariants — the deciding evidence

From kki.4 (`04-timeout-semantics.md` §4), measured two independent ways:

| Setup | What gomutants did | Exit | Efficacy reported |
|---|---|---:|---|
| Wedge in the code under test (mutations 23 / 30) | aborted at its coverage baseline; **never judged a single mutant**, no report file | **1** | none |
| Wedge as a mutant (synthetic, stock mutator) | 3 infinite loops `TIMED OUT`, excluded from the denominator, **verdict cached and replayed** | **0** | **81.82 %** (coverage 100 %) |

Wall times: mutation 23 → **600.30 s** (gomutants' own `-test.timeout=10m0s` on
the coverage run); mutation 30 → **32.33 s**. The incumbent, same two mutations,
in the real tree: **both `caught`**, with assertions that *name the wedge*
(`hub_test.go:401: Broadcast 3 blocked for 10s: a non-reading subscriber must be
dropped, never waited for`; `integration_test.go:884: httptest.Server.Close did
not return within 10s: a handler is wedged`), in **45.90 s** total, tree clean
afterwards.

The quiet failure is the disqualifying one. Mode A's exit 1 is incidental — it is
the same exit a compile error or a missing test binary gives, it never classifies
the defect, and it depends on the hang being in the *baseline*, which is exactly
what a mutation gate assumes it is not. Mode B is the default behaviour of the
tool on a hang: **a gate built on gomutants passes a deliberate deadlock**, and
kki.4 §2.1 shows the blindness is sticky — re-running the identical command
replays all 14 cached outcomes, 3 of them hangs, in **0.32 s, exit 0**, with a
byte-identical summary. kki.3 §7 measures a second, independent instance of the
same class: an interrupted run left 197 of 343 verdicts cached, and the next
identical invocation reported those, `Timed out: 0`, `Efficacy: 91.05%`,
**exit 0**, while 146 mutants sat `PENDING`. `PENDING` is neither caught nor
survived and does not stop a zero exit.

### 2.2 Curated-grid reach — 18 of 41

From kki.5 (`05-traceability.md` §4), a per-row audit of all 41 curated
mutations against gomutants' generated set:

| Class | Rows |
|---|---:|
| `GENERATED-AND-KILLED` | **18** (10 `exact`, 8 `equivalent`, every one verified `KILLED`) |
| `GENERATED-AND-LIVED` | **0** |
| `NOT-GENERATED` | **23** (8 with *no* mutant on any anchor line; 15 with only a *related* mutant expressing a different defect) |

Per file: `api.go` 8/15 traceable, `conductor.go` 6/8, `hub.go` 4/7, `ws.go`
**0/10**, `server.go` **0/1**. The premise "gomutants auto-generates what we
hand-crafted" holds for the **deletion and guard classes** — statement removal,
guard-body neutralisation, operator flips, boundary flips — and fails for every
**substitution** class the grid uses: HTTP status codes, websocket close codes
and opcodes, route patterns, callees, boolean literals, loop bookkeeping,
struct-field values, message bytes, constructors with different arguments, and a
deliberate wedge (kki.5 §5 classes A–F). kki.5 §7 gives the strict reading: the
four debatable rows (04, 15, 17, 21) could be reclassified, giving **14/27**
instead of 18/23. The count does not move outside that range.

### 2.3 Breadth gained — the inverse finding, and its honest reading

kki.5 §6: of the 357 judged mutants, **57 (16 %) sit on a line one of the 41
anchors spans and 300 (84 %) do not** — `api.go` 94, `server.go` 60,
`conductor.go` 79, `hub.go` 49, `ws.go` 18; by verdict KILLED 193, LIVED 66,
NOT VIABLE 26, NOT COVERED 14, INFRA ERROR 1. That is the real prize: **66
survivors on lines the curated grid never asks about**, led by
`INTEGER_INCREMENT` 13, `INTEGER_DECREMENT` 12, `BRANCH_IF` 12,
`CONDITIONALS_NEGATION` 7. The 5 `ERRORF_WRAP` mutants (`%w` → `%v`) are the part
with **no analogue in the curated grid at all**.

kki.5 states the two ways not to over-claim it, and they are adopted here in
substance: this counts *lines gomutants mutates*, not *behaviours the grid fails
to test* (a curated test does cover many of those lines — the 193 KILLED
off-anchor mutants are killed by this same suite), and it is *narrower* than
coverage, since much of the 300 is mechanical (50 literal ±1 edits, 84
`STATEMENT_REMOVE`s).

### 2.4 Survivor noise — 72 lives, mostly unkillable

kki.3 §6's categorisation of the 72 `LIVED` mutants, which is what a gate would
have to read as a permanent red bar:

* **18 — unobservable numeric literals** (`api.go:20 64 → 63`, `ws.go:19 5 → 4`,
  buffer sizes and channel capacities). Not a test-quality gap: the tool's own
  self-test documents the same class as not worth chasing.
* **21 — `server.go` header/host forwarding** (`server.go:121 host == "exe.cloud"`,
  `:150 strings.HasPrefix(lower, "x-exedev-")`, the `:161` sort comparator). The
  largest cluster, and the **one genuinely actionable gap**: the suite asserts
  neither the forwarded-header set nor header ordering, and the deployment host
  names are untested.
* **10 — `hub.go` close/unsubscribe branches.** Real: delivery is covered, these
  branches are not.
* **5 — log-only branches** (`slog.Debug`/`slog.Warn` bodies) that survive *by
  construction*: `exclude-calls` suppresses mutants *inside* the call but not the
  branch around it. These are the same four lines kki.5 §5.1 records as
  `LIVED`-on-anchor-line.
* **3 — error wrapping/classification** (`%w` → `%v`; `errors.Is(err,
  io.ErrUnexpectedEOF)` removed).
* the remainder — return-path shapes.

Against that, the verdict mix that a gomutants gate reports: **efficacy 77.07 %,
coverage 94.86 %** over 357 judged mutants (KILLED 242, LIVED 72, NOT VIABLE 28,
NOT COVERED 17, TIMED OUT 0, INFRA ERROR 1, suppressed 8). Both percentages
**exclude `TIMED OUT`** — the same exclusion that makes kki.4's Mode B gate
report 81.82 % while three loops spin.

### 2.5 The verdict

**Could gomutants replace the grid with zero invariant loss? No.**

Two citations decide it independently, so the answer does not rest on a
judgement call about expressibility:

1. **kki.4 §4** — a gomutants gate would have passed mutations 23 and 30, the two
   hang-shaped invariants the epic `e2v` was built to protect, with exit 0 and a
   *higher-reported* efficacy than the honest one. Replacing the grid would not
   lose coverage gradually; it would lose the repository's hardest invariants
   **on the first run**.
2. **kki.5 §4** — 23 of 41 curated mutations, including all 10 `ws.go` rows, the
   single `server.go` row, and both hang rows, have no gomutants mutant
   expressing the same defect. There is no subset of the grid to hand over.

For completeness, the direction of the *verdict-vocabulary* comparison runs the
other way and is not a reason to switch: the curated grid reports caught /
`SURVIVED` / `WEAK` / `BROKEN`, where a compile failure is **WEAK** and never a
catch and a non-matching anchor is **BROKEN** — a self-invalidating result. The
41 and the 360 answer different questions and are never added or averaged here.

---

## 3. Recommendation

**Exactly one: (d) hybrid** — gomutants for breadth in the dev loop, the curated
grid keeping **exclusive** ownership of the hang-shaped and domain invariants and
remaining the gate.

Concretely, what that means:

* `scripts/mutation-check.sh` stays exactly as it is. It is `make mutation-check`,
  it costs 128.042 s (or 79.5 s if full routing is ever adopted), it exits 0 only
  when 41/41 are caught, and it is the only one of the two harnesses that turns a
  hang into a *named failing assertion* rather than a status line (kki.4 §3).
* gomutants is run **by hand, for breadth, non-gating**: at the bounded `-w 1`
  configuration, with `-cache`/`-o` outside the repo, expected to be *noisy*
  (77.07 % efficacy, a 2.5 GB runaway in `conductor.go`, 84 % of its mutants off
  any anchor line), and its output is triage input, never a pass/fail. Its
  `KILLED`/`LIVED` split on the 300 off-anchor mutants is how new curated
  mutations get *found*.
* No `make verify` step, no `.gomutants.yml`, no pinned gomutants dependency. A
  tool whose `TIMED OUT` and `PENDING` verdicts both exit 0 (kki.3 §7, kki.4
  §2.1) must not be the thing that says "the suite is non-vacuous".

**Why, in the numbers and the citations:**

| Argument | Evidence |
|---|---|
| Speed does not justify replacement | §1 rows 3 vs 2: 231.32 s vs 128.042 s cold. gomutants is 1.8x slower, and ~53 s of the grid's remaining cost is a per-invocation floor kki.2 §4 showed no routing can remove — the same floor gomutants re-pays as coverage+baseline phases (~5.6 s per run) |
| The 2.52 s warm number is not a gate number | §1 rows 4/5: it needs an unchanged tree and a populated cache. A gate runs after an edit; that regime was never timed |
| Replacement loses the hardest invariants immediately | kki.4 §4: exit **0**, efficacy 81.82 %, coverage 100 %, three infinite loops, verdict cached and replayed. The incumbent catches both hang mutations in 45.90 s with named assertions |
| Replacement loses 23 hand-written claims outright | kki.5 §4: 18/41 traceable, 0/10 `ws.go` rows, 0/1 `server.go` rows; strict reading 14/27 |
| gomutants is unreliable even on its own terms | kki.3 §2 (default `-w 6` stalled this 6-vCPU VM twice; 120/360 `TIMED OUT` = 33 %, efficacy reported as a *cleaner* 90.19 % than the honest 72.35 %), §3 (2.5 GB runaway), §7 (green exit 0 on a run that judged 146 of 360 mutants) |
| But the breadth is real and unharvestable otherwise | kki.5 §6: 300/357 judged mutants off-anchor, 66 of them `LIVED`, 5 `ERRORF_WRAP` with no curated analogue. kki.3 §6 cluster 2 (`server.go` header forwarding, 21 survivors) is untested behaviour the curated grid structurally never mutates |
| So keep the grid as the gate and take the breadth for free | the hybrid loses nothing kki.1–kki.5 measured, and captures the one thing kki.5 says the curated grid cannot express |

**Rejected alternatives, and why:**

* **(a) keep `mutation-check.sh` as-is** — defensible, and strictly worse than
  (d): it declines the 66 off-anchor survivors and the 21-mutant `server.go`
  cluster, which cost nothing to harvest *provided* gomutants is explicitly
  non-gating. (a) is (d) minus a free benefit.
* **(b) keep it and add per-mutation test routing** — **already done**, twice:
  nine mutations by e2v.2.1.3 and all 41 by kki.2's survey (which proposed the
  remaining 32 as 5th fields; `02-routing.md` §6, not merged into `main`).
  Measured at 128.042 s in the tree and 79.5 s fully routed in a worktree.
  Recommending it as a *change* would be recommending finished work. Its finding
  is instead an input to this report: the startup floor is the wall.
* **(c) adopt gomutants as the primary gate** — ruled out by §2.5, on two
  independent citations, one of which (kki.4) is the epic's stated risk #1
  confirmed by measurement.

**What would change this recommendation** (stated so the next agent does not have
to re-derive it):

1. A gomutants release that treats `TIMED OUT` and `PENDING` as **failures**
   rather than excluding them from the denominator and exiting 0. That alone
   removes the disqualifying evidence in §2.1 and the resume trap in kki.3 §7.
2. Mutators for the substitution classes kki.5 §5 shows are inexpressible —
   named-constant/enum rewrite, string and route-pattern edits, boolean literals
   in struct fields and call arguments. That would move 23 rows of the grid from
   NOT-GENERATED toward traceable and make (d)'s split a genuine judgement call
   rather than a foregone conclusion.
3. A bounded full sweep that includes `conductor.go` (i.e. the
   `snapshotLocked:STATEMENT_REMOVE#1` runaway no longer needing a 2.5 GB cgroup),
   which would give §1 a real whole-codebase gomutants denominator.
4. A measured warm-after-a-real-edit wall time, per §1 row 5. If a real edit's
   warm run came in materially under 128.042 s **and** item 1 held, the speed
   half of the argument would flip.

---

## 4. Follow-up issues implied by (d)

Listed for the orchestrator's triage only. **None of these was created or
started.**

1. **Run gomutants as a documented non-gating breadth pass** — a short section in
   the mutation-testing part of `AGENTS.md` giving the exact bounded command
   (`-w 1`, `--exclude-files 'conductor\.go$'`, `-cache`/`-o` under `/tmp`, a
   fresh cache path per run) and stating that its exit code is not a gate.
2. **Encode the `TIMED OUT` / `PENDING` trap wherever gomutants output is read** —
   a check that a report's JSON contains no `TIMED OUT` and no `PENDING` records
   before any verdict is quoted, so a partial run can never be cited as a pass.
3. **Adopt the 32 remaining routing regexes from kki.2 §6** — 128.042 s → ~79.5 s
   measured, 41/41 caught, at the cost of coupling each mutation to a named
   owning test. Independent of gomutants; the cheapest remaining win in the grid.
4. **Triage the 66 off-anchor `LIVED` mutants into new curated mutations** —
   starting with the 5 `ERRORF_WRAP` (`%w` → `%v`) mutants, which have no curated
   analogue, and the 10 `hub.go` close/unsubscribe-branch survivors.
5. **Assert the `server.go` forwarded-header set and header ordering** — the one
   cluster kki.3 §6 calls real untested behaviour (21 of its 360 mutants survive
   there; 40 % efficacy for the file). Note the epic `rka` cross-epic note: that
   header forwarding is the code epic `rka` removes, so this may be deleted work
   — confirm with the `rka` owner before writing the test.
6. **Re-measure gomutants after epic `rka` lands** — 03/04/05's figures are
   pre-`rka`, and if `rka` deletes `server.go`'s header forwarding, kki.3's
   largest survivor cluster disappears and the breadth argument in §2.3 changes
   size. Do not present the current numbers as post-`rka`.
7. **Close §1 row 5: time a warm gomutants run after a real edit** — the only
   regime in which gomutants is faster than the grid, and the one a dev-loop
   integration would actually live in.
8. **Re-audit the 23 NOT-GENERATED rows when a substitution mutator appears** —
   `05-traceability.md` §3 is row-addressable, so the re-audit is a
   reclassification, not a fresh pass.

---

## 5. Method notes, and what this report does not answer

* **No measurement was run.** This is a synthesis of kki.1–kki.5 plus the e2v
  reports, with every figure cited to its source document, section and commit.
  The comparison is therefore between measurements taken at four different
  commits (`330b34d`, `b2d4280`, `19ea479`/`8a908d7`, `4cbb677`). kki.5's header
  establishes that `19ea479..8a908d7` touches nothing under `srv/`, `cmd/` or
  `scripts/mutation-check.sh`, and `5a47cf5` is the grid's own last change, so
  the curated-grid rows are all taken against effectively one tree. The gomutants
  rows are all from one session on one machine at `b2d4280` — internally
  comparable, which is the stronger guarantee, and weaker when set against the
  later grid numbers.
* **Verdict vocabularies are not merged.** `scripts/mutation-check.sh` reports
  caught / `SURVIVED` / `WEAK` / `BROKEN`, where a compile failure is **WEAK**,
  never a catch, and a non-matching anchor is **BROKEN** — a self-invalidating
  result. gomutants reports KILLED / LIVED / NOT VIABLE / NOT COVERED / TIMED OUT
  / INFRA ERROR / EQUIVALENT, where NOT VIABLE is a compile-level outcome and
  EQUIVALENT is 0 only because `--detect-equivalent` was never enabled (kki.3
  §8). `TIMED OUT` and `NOT COVERED` are excluded from gomutants' efficacy
  denominator; nothing equivalent is excluded from the curated grid's.
* **Nothing was adopted.** No `make verify` step, no `.gomutants.yml`, no pinned
  gomutants dependency, no gomutants cache or report inside the repo, no edit to
  `Makefile`, `scripts/mutation-check.sh` or `srv/`. The only files this task
  writes are this document and the index row in `docs/mutation-bench/README.md`.
* **Not answered here:** the `-race` comparability of gomutants' numbers (kki.3
  §9 — none used `-race`, and the flags change what `TIMED OUT` means);
  `--integration`, `--detect-equivalent` and `--changed-since` (all unexercised,
  kki.3 §9 — `--changed-since` is the one that would make the warm cache usable
  as a pre-push gate, i.e. it bears on §1 row 5); whether a gomutants sweep of
  the current post-`rka` tree would reproduce these verdicts; and whether the
  curated grid's 41 mutations are the *right* 41 — this report compares
  harnesses, it does not audit the grid's own coverage choices.
