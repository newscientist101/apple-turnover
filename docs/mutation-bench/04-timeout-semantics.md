# Timeout semantics: can gomutants catch a HANG-shaped defect?

**Issue:** strudel-agent-kki.4 (epic strudel-agent-kki)
**Date:** 2026-09-30
**Machine:** Linux ROGQ58 6.18.33.2-microsoft-standard-WSL2 (WSL2), 12th Gen Intel(R)
Core(TM) i7-12700K exposed as **6 vCPUs** (`cpuset 0-5`), **7.6 GiB RAM** (7,816 MB total),
2 GiB swap. Load average at every timed run's start: `0.08`–`0.19` (idle).
**Go:** go1.27.1 linux/amd64 · **Repo commit:** `b2d4280` (main)
**Tool under test:** `gomutants` **v0.6.1** (`gomutants v0.6.1 (commit: none, built: unknown)`),
binary at `/home/exedev/go/bin/gomutants` — **not on the default `PATH`**; `go` lives in
`/usr/local/go/bin`, also not on `PATH`. Both are prepended explicitly in every command below.
**Incumbent:** `scripts/mutation-check.sh` at `b2d4280`.

All measurements are single, serialized, bounded runs. No two timed runs overlapped
(`03-gomutants.md` rule 4). Raw logs: `/tmp/kki4/{m23,m23b,m30}/`, `/tmp/kki4/synth/`,
`/tmp/kki4/incumbent.log`.

---

## 0. Conclusion

**gomutants CANNOT gate the hang-shaped invariants.** It exits **0** while three
genuine infinite-loop mutants sit in the tree as `TIMED OUT`, and that blindness is then
**cached and replayed** on every subsequent run. A CI gate built on gomutants would pass a
deliberate deadlock.

This is measured two independent ways, and the two failure modes are different:

| # | Setup | What gomutants did | Exit | Efficacy reported |
|---|---|---|---|---|
| A | wedge **in the code under test** (mutations 23 / 30) | aborted at its coverage baseline; **never judged a single mutant** | **1** | *none — no report file written* |
| B | wedge **as a mutant** (synthetic) | `TIMED OUT` × 3, excluded from the denominator | **0** | **81.82 %** |

Mode A is the *loud* failure: the hang is in the baseline, so gomutants cannot even
establish coverage and dies with `coverage run failed`. Mode B is the *quiet* failure, and
it is the one that matters for a gate: a hang that is reachable only as a mutant is scored
as "not a failure at all".

---

## 1. Mode A — the wedge in the code under test

Both mutations were applied by hand **in throwaway git worktrees** (`git worktree add
/tmp/kki4-m23 HEAD`, `/tmp/kki4-m30 HEAD`), using the exact `old`→`new` text from the
mutation table in `scripts/mutation-check.sh` and the script's own `\n`/`\t` unescape
logic. Both anchors were asserted to occur **exactly once**; a non-matching anchor would
have been reported `BROKEN`, not treated as a result. The real tree was never mutated.

**Mutation 23 — `23-wedged-state-handler`** (`srv/api.go`), diff in the worktree:

```diff
 func (s *Server) handleAPIState(w http.ResponseWriter, r *http.Request) {
+	select {} // deliberately wedged: never responds
 	writeJSON(w, http.StatusOK, s.Conductor.Snapshot())
 }
```

**Mutation 30 — `30-hub-slow-client-blocks`** (`srv/hub.go`):

```diff
 func (h *Hub) deliver(sub *Subscriber, msg []byte) bool {
-	select {
-	case sub.ch <- msg:
-		return true
-	default:
-		return false
-	}
+	sub.ch <- msg
+	return true
 }
```

The mutant gomutants generates *at* each injected site were identified from the line
numbers in kki.3's `report-B.json` (ids are stable across runs, so they are directly
reusable): for 23 the injected line is `srv/api.go:112`, whose mutant is
`srv/api.go:(*Server).handleAPIState:STATEMENT_REMOVE#1`; for 30 the mutants in `deliver`
are `srv/hub.go:(*Hub).deliver:RETURN_FALSE#1` (line 332) and `…RETURN_TRUE#1` (line 334).
Each was then run **on its own** with `-run-mutant-id`, at the bounded `-w 1` config from
`03-gomutants.md` §11 inside a capped cgroup (`MemoryMax=2500M`, `CPUQuota=200%`,
`RuntimeMaxSec=1500`, `OOMPolicy=continue`, `GOMAXPROCS=2`, `GOFLAGS=-p=2`).

### 1.1 Mutation 23

```bash
gomutants -w 1 --exclude-files 'conductor\.go$' \
  -run-mutant-id 'srv/api.go:(*Server).handleAPIState:STATEMENT_REMOVE#1' \
  -o=/tmp/kki4/m23b/report.json -cache=/tmp/kki4/m23b/cache.json ./srv/...
```

| | |
|---|---|
| Mutant status | **none — never reached.** No `report.json` was written. |
| Efficacy printed | **none.** Output ends `gomutants: coverage run failed: exit status 1` |
| **Exit code** | **1** |
| Wall time | **600.30 s** (inner `go test` hit its own `-test.timeout=10m0s`) |

gomutants spends its first phase running the suite to build a coverage profile, *before*
any mutant is applied. The wedge is in that baseline, so the coverage run hangs. The
10-minute ceiling is gomutants' own: the inner command was

```
go test -count=1 -coverprofile=… ./srv/...
  … srv.test -test.timeout=10m0s -test.count=1 -test.coverprofile=…
```

and the log ends `FAIL srv.exe.dev/srv 600.029s` → `gomutants: coverage run failed`.

Tightening gomutants' per-mutant timeout knobs does **not** help, because they size the
*per-mutant* deadline, not the coverage baseline:

```bash
… -timeout-coefficient=1 -timeout-margin=1 -timeout-min=1s   # still 600.30 s
```

`-timeout-coefficient` (default 10) is documented as "multiply baseline test time for the
global timeout ceiling" and `-timeout-margin` (default 3) "scale per-test sums into the

### 1.2 Mutation 30

```bash
gomutants -w 1 --exclude-files 'conductor\.go$' \
  -run-mutant-id 'srv/hub.go:(*Hub).deliver:RETURN_FALSE#1' \
  -o=/tmp/kki4/m30/report.json -cache=/tmp/kki4/m30/cache.json ./srv/...
```

| | |
|---|---|
| Mutant status | **none — never reached.** No `report.json`. |
| Efficacy printed | **none.** `gomutants: coverage run failed: exit status 1` |
| **Exit code** | **1** |
| Wall time | **32.33 s** |

Same mode, far cheaper, because the e2v work made the suite *fail fast* instead of hang.
The coverage run now fails on its own within 32 s — and the failure messages are exactly
the ones epic e2v was built to produce:

```
--- FAIL: TestHubSlowSubscriberCannotBlockOthers (15.00s)
    hub_test.go:401: Broadcast 3 blocked for 10s: a non-reading subscriber must be dropped, never waited for
    hub_test.go:73: Hub.Close did not return within 5s: the hub goroutine is wedged
--- FAIL: TestHubConcurrentSubscribeUnsubscribeBroadcast (15.01s)
    hub_test.go:486: SubscriberCount did not answer within 2s: the hub goroutine is wedged …
    hub_test.go:573: concurrent churn did not finish within 10s: subscribe/unsubscribe/broadcast is wedged
```

This is a nice accidental confirmation of e2v.1: a wedged hub is now a **32 s clean
failure**, not a 150 s hang. It also shows mode A's cost is entirely a function of how
long the *suite* takes to fail — gomutants pays it before it ever mutates anything.

---

## 2. Mode B — the wedge as a mutant (the decisive experiment)

Mode A cannot answer the question, because gomutants never gets as far as a verdict. The
real question is what happens when the hang is something gomutants *itself* introduces,
which is the normal case for a mutation gate: the committed code is green, and a mutant
turns out to hang.

That cannot be provoked on this repo — no mutator reaches `deliver` or the handler in a
way that hangs — so it was measured on a **synthetic module outside the repo**
(`/tmp/kki4/synth`, throwaway, never in the tree). The hang is reachable by a stock
mutator:

```go
// hang/hang.go
func Sum(n int) int {
	total := 0
	for i := n; i > 0; i -= 1 {
		total += i
	}
	return total
}
```

`go test ./...` is green (`ok synth/hang 0.001s`). `--dry-run` finds 14 mutants, three of
which are unbounded loops:

```
[PENDING] hang.go:7:23  -= → +=   (INVERT_ASSIGNMENTS)
[PENDING] hang.go:7:23  -= → =    (REMOVE_SELF_ASSIGNMENTS)
[PENDING] hang.go:7:26  1 → 0     (INTEGER_DECREMENT)   ← i -= 0 never terminates
```

COLD run:

```bash
gomutants -w 1 -o=/tmp/kki4/synth/report.json -cache=/tmp/kki4/synth/cache.json ./...
```


### 2.1 The blind spot is sticky (cache replay)

The three `TIMED OUT` outcomes are written to the cache with `"status": "TIMED OUT"` and
replayed on a warm rerun:

```json
{"rel_file": "hang.go", "line": 7, "col": 26, "type": "INTEGER_DECREMENT",
 "original": "1", "replacement": "0", "status": "TIMED OUT", "duration_ms": 856}
```

Re-running the **identical** command:

```
Cache: 14 mutant outcomes reused from /tmp/kki4/synth/cache.json
  Killed: 9 … Lived: 2 … Timed out: 3
  Cached: 14  (skipped)
  Efficacy: 81.82%
```

**0.32 s, exit 0.** All 14 mutants — including the 3 infinite loops — are skipped, and the
summary is byte-identical to the cold run. So the hang is not a one-off omission that a
later run would catch: once gomutants has decided a hang is `TIMED OUT`, the cache makes
that decision permanent for as long as the source and tests are unchanged. Note also that
`-run-mutant-id` explicitly *skips* the cache for that mutant ("so the verdict is always
freshly measured"), which is the only documented way to force re-judgement.

---

## 3. The incumbent, same two mutations

Run in the **real** tree via the script's own self-reverting selection mode:

```bash
timeout 600 ./scripts/mutation-check.sh 23-wedged-state-handler 30-hub-slow-client-blocks
```

```
23-wedged-state-handler   --- FAIL: TestIntegrationParallelPushesAreBoundedAndContiguous (12.05s)
                             integration_test.go:979: GET /api/state: … timeout awaiting response headers
                             integration_test.go:884: httptest.Server.Close did not return within 10s: a handler is wedged
                           caught
30-hub-slow-client-blocks --- FAIL: TestHubSlowSubscriberCannotBlockOthers (15.01s)
                             hub_test.go:401: Broadcast 3 blocked for 10s: a non-reading subscriber must be dropped, never waited for
                             hub_test.go:73: Hub.Close did not return within 5s: the hub goroutine is wedged
                           caught

mutation-check: 2 caught, 0 survived, 0 weak, 0 broken anchor(s)
mutation-check: PASSED — every mutation was caught; the suite is non-vacuous.
```

| | Incumbent | gomutants (mode A) | gomutants (mode B) |
|---|---|---|---|
| mutation 23 | **caught** (assertion names the wedge) | no verdict, exit 1, 600.30 s | n/a |
| mutation 30 | **caught** (assertion names the wedge) | no verdict, exit 1, 32.33 s | n/a |
| a hang that is a *mutant* | caught (watchdog assertion fails) | — | **`TIMED OUT`, exit 0** |
| wall time, this arm | **45.90 s** (both mutations + baseline) | 632.63 s | 4.08 s |
| gate verdict | **PASSES correctly** | fails, but for the wrong reason | **PASSES a deadlock** |

`git status --porcelain` was empty immediately after this run — the script reverts, and that
was verified rather than assumed. So the incumbent does the one thing gomutants cannot:
it turns a hang into a **named, failing assertion** rather than a status line.

---

## 4. Answering the question as posed

> would a CI/review gate built on gomutants PASS a deliberate deadlock?

**Yes — if the deadlock is reachable as a mutant.** Measured: exit code **0**, efficacy
**81.82 %**, coverage **100 %**, with three infinite loops in the tree, and the verdict
then replayed from cache on every later run. The epic's stated risk #1 is confirmed, and
it is not hypothetical: it is the default behaviour of the tool on a hang, not an edge case
requiring unusual configuration.

> Does it at least fail loudly when the hang is in the committed code?

**Yes, but only incidentally, and it is not a hang check.** gomutants exits 1 because its
*coverage baseline* cannot complete — the same exit 1 it would give for a compile error or
a missing test binary. It never classifies the defect, never writes a report, and for
mutation 23 it burns **600 s** doing so. A reviewer reading "exit 1, no report" learns that
something is wrong, but not that a deadlock was found. And the mode-A protection does not
generalise: it depends on the hang being in the *baseline*, which is exactly what a
mutation-testing gate assumes it is not.

### What this means for epic kki


---

## 5. Method notes and caveats

- **The real tree was never mutated.** Both mutations were applied in `/tmp/kki4-m23` and
  `/tmp/kki4-m30`, each removed with `git worktree remove --force` afterwards;
  `git worktree list` shows only `/home/exedev/strudel-agent`. The one place the real tree
  is touched is the §3 contrast arm, run through the script's own reverting selection
  mode, with `git status --porcelain` verified empty straight after.
- **Two stale worktrees from e2v.1.3.4** (`/tmp/nv/wt`, `/tmp/nv/w2`) were pruned so
  `git worktree list` was unambiguous. `/tmp/nv/wt` held one untracked file,
  `scripts/mutation-check-unrouted.sh`; it is a documented, regenerable artefact
  (`08-non-vacuity.md` gives the derivation) and was copied to
  `/tmp/kki4/preserve/mutation-check-unrouted.sh` before removal. **No repo file depends
  on it** — it existed only inside that deleted worktree, and the recipe to rebuild it is
  in the docs.
- **Anchor matching was asserted.** Both mutations reported `anchor occurrences: 1`. A
  `BROKEN` anchor would have invalidated the run; none occurred.
- **Mutant-id lookup** used the stable `id` plus `line`/`column` fields in kki.3's
  `report-B.json`, not a guess: mutation 23's injected line 112 maps to
  `handleAPIState:STATEMENT_REMOVE#1` and mutation 30's `deliver` mutants are at lines
  332/334. Ids are documented as stable ("this stable id … a unique prefix is accepted").
- **Two harness bugs were found and fixed while measuring**, both worth recording because
  they would silently corrupt a rerun: (a) `go` is at `/usr/local/go/bin` and is **not on
  `PATH`** inside a `systemd-run` unit — 03-gomutants.md §11's `PATH` omits it, which
  worked there only because the build cache was warm; the first unit died in 4 ms with
  `go list: exec: "go": executable file not found in $PATH`. (b) mutant ids contain
  `(*Server)`, so an unquoted `(` is a shell syntax error inside the unit — the command
  must be `printf %q`-quoted or the unit exits 2 in 1 ms. Both are harness defects, not
  findings about gomutants.
- **The 600 s mode-A cost is a property of the wedged suite, not of gomutants' per-mutant
  timeout**, and the two are not separable with the flags available: `-timeout-coefficient`,
  `-timeout-margin` and `-timeout-min` size the per-mutant deadline, and `--test-flags`
  refuses `-timeout` outright. Reported as measured, not extrapolated.
- **`-w 1` on an idle box throughout.** The `-w 6` default produced 120/360 false
  `TIMED OUT` verdicts in kki.3 §5.2 by starving the inner `go test`; that confound is
  absent here (load 0.08–0.19, single worker, 4.08 s run), so the three `TIMED OUT`
  verdicts in §2 are real hangs and not contention artefacts. This is the check that makes
  mode B trustworthy.
- **Machine memory was never at risk**: peak `used` stayed ≈1.5 GB of 7,816 MB throughout,
  well above the 5,216 MB floor that 03-gomutants.md identified as the stall tripwire, and
  the cgroup cap held.

## 6. Reproducing

```bash
export PATH="$PATH:$(go env GOPATH)/bin:/usr/local/go/bin"

# Mode B — the decisive experiment (4 s, exit 0, three hangs reported as TIMED OUT)
mkdir -p /tmp/kki4/synth/hang && cd /tmp/kki4/synth
printf 'module synth\n\ngo 1.26.1\n' > go.mod
cat > hang/hang.go <<'EOF'
package hang

func Sum(n int) int {
	total := 0
	for i := n; i > 0; i -= 1 {
		total += i
	}
	return total
}
EOF
cat > hang/hang_test.go <<'EOF'
package hang

import "testing"

func TestSum(t *testing.T) {
	if got := Sum(3); got != 6 {
		t.Fatalf("Sum(3) = %d, want 6", got)
	}
}
EOF
go test ./...                                   # green baseline
gomutants -w 1 -o=report.json  -cache=cache.json  ./...   # → Timed out: 3, Efficacy 81.82%, exit 0
gomutants -w 1 -o=report2.json -cache=cache.json ./...   # → Cached: 14 (skipped), exit 0

# Mode A — wedge in the tree, in a throwaway worktree (NEVER the real tree)
git worktree add /tmp/kki4-m23 HEAD
#   apply mutation 23's old→new from the table in scripts/mutation-check.sh, then:
cd /tmp/kki4-m23
gomutants -w 1 -run-mutant-id 'srv/api.go:(*Server).handleAPIState:STATEMENT_REMOVE#1' \
  -o=/tmp/kki4/m23/report.json -cache=/tmp/kki4/m23/cache.json ./srv/...
#   → gomutants: coverage run failed: exit status 1, 600.30s, no report
cd - && git worktree remove --force /tmp/kki4-m23

# Contrast arm — the incumbent, in the real tree (self-reverting)
timeout 600 ./scripts/mutation-check.sh 23-wedged-state-handler 30-hub-slow-client-blocks
git status --porcelain        # must be empty
```

`-run-mutant-id` needs `printf %q` quoting when passed through `systemd-run`; run it
directly in a shell and the problem disappears.

## 7. State left behind

- `make verify` green (`gofmt` clean, `go vet`, `go build`, `go test ./... -race -count=1`
  `ok srv.exe.dev/srv 2.762s`), **3.36 s**.
- `git status --porcelain` clean apart from this document and the
  `docs/mutation-bench/README.md` index row.
- No `.gomutants.yml`, no `mutation-report.json`, no `.gomutants-cache.json`, no `*.test`
  anywhere in the repo; all `-o`/`-cache` output was redirected under `/tmp/kki4`.
- No `gomutants` / `go test` / `mutation-check` process left running.
- No production file, no `scripts/mutation-check.sh`, no Makefile target was modified.
- kki.3's non-durable `/tmp/kki3` artefacts were copied to `/tmp/kki4/kki3-precious/`
  (`report-B.json`, `cache-B.json`, `report-srv.json`, `dry-srv.txt`, `list-mutators.txt`,
  `conductor-permutant.tsv`) — still under `/tmp`, so kki.5/.6 should re-run §11 of
  `03-gomutants.md` rather than rely on them.

Recorded here as a measured input to kki.6, not as a decision — kki.6 owns the decision.

- The hang class is **disqualified** for gomutants as a primary gate. The curated grid
  catches both hang mutations with named assertions; gomutants either cannot judge them
  (mode A) or passes them (mode B).
- This is *independent* of the speed question. gomutants is genuinely fast on this
  codebase (kki.3: 231.32 s cold for 256 mutants, 2.52 s warm) and its efficacy arithmetic
  is the problem, not its wall time. A fast gate that cannot see a class of defect is not
  a faster gate; for the hang class it is a **blind** gate.
- Any adoption would have to **additionally** keep a hang-shaped check outside gomutants
  — which is most of what `scripts/mutation-check.sh` already is.

```
  Killed:       9  (81.8%)
  Lived:        2  (18.2%)
  Not covered:  0
  Not viable:   0
  Timed out:    3
  Efficacy:     81.82%
```

| | |
|---|---|
| Mutant status of the three hangs | **`TIMED OUT`** |
| **Exit code** | **0** |
| Efficacy | **81.82 %** — computed as `9/(9+2)`; the 3 hangs are **not in the denominator** |
| Mutant coverage | **100 %** — also flattered: `(K+L)/(K+L+NOT_COVERED)` counts timed-out mutants |
| Wall time | 4.08 s |

From the JSON report:

```
TIMED OUT -> hang/hang.go:Sum:INVERT_ASSIGNMENTS#1        line 7 col 23  '-=' -> '+='
TIMED OUT -> hang/hang.go:Sum:REMOVE_SELF_ASSIGNMENTS#1   line 7 col 23  '-=' -> '='
TIMED OUT -> hang/hang.go:Sum:INTEGER_DECREMENT#3         line 7 col 26  '1'  -> '0'
```

`INTEGER_DECREMENT#3` is `i -= 1` → `i -= 0`: the loop provably never terminates. gomutants
detects that it hangs, and then **reports it as a pass**: exit 0, 81.82 % efficacy,
100 % coverage, while three infinite loops are present. Worse, the 2 mutants that *were*
judged include `LIVED` equivalents that drag the number down, so the hangs are not even
costing gomutants anything — they are invisible in both headline figures.

The `-w 6` contention artefact from `03-gomutants.md` §5 cannot explain this: this run was
`-w 1` on an idle box (load 0.08) and took 4 s. These three are real hangs.

per-mutant adaptive timeout" — neither reaches the `-test.timeout=10m0s` the coverage run
is launched with. `--test-flags=-timeout=90s` is rejected outright:

```
gomutants: --test-flags: -timeout is managed by gomutants and cannot be overridden
```

**The wedge is real, proven by goroutine dump** (`SIGQUIT` on the inner `srv.test`):

```
goroutine 8 gp=0x2de151ab45a0 m=nil [select (no cases)]:
runtime.block()
srv.exe.dev/srv.(*Server).handleAPIState(0x427da9?, {0x428c9d?, 0x2de151a4e1b4?}, 0x0?)
	/tmp/kki4-m23/srv/api.go:112 +0x36
srv.exe.dev/srv.(*Server).handleAPIState-fm(…)
net/http.HandlerFunc.ServeHTTP(…)
```

`[select (no cases)]` parked at `api.go:112` is the injected `select {}` — an unrecoverable
wedge with no case to ever be ready.
