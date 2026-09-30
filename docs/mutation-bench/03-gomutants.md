# gomutants on this codebase: measured cost, output, and what it does to the machine

**Issue:** strudel-agent-kki.3 (epic strudel-agent-kki)
**Date:** 2026-09-29
**Machine:** Linux ROGQ58 6.18.33.2-microsoft-standard-WSL2 (WSL2), 12th Gen Intel(R)
Core(TM) i7-12700K exposed as **6 vCPUs** (`cpuset 0-5`), **7.6 GiB RAM**, 2 GiB swap
**Go:** go1.27.1 linux/amd64 · **Repo commit:** `4cbb677`
**Tool under test:** `gomutants` **v0.6.1**, module `github.com/szhekpisov/gomutants`,
proxy origin SHA `76c6e5989b7f5e252acc4641366a7091392a9733`, built with
`go install github.com/szhekpisov/gomutants@v0.6.1`; `gomutants --version` reports
`gomutants v0.6.1 (commit: none, built: unknown)`. Its `go.mod` declares `go 1.26.1`
(host toolchain 1.27.1 satisfies it — verified, not assumed).

**Headline numbers** (details and caveats below):

| Measurement | Value |
|---|---|
| Mutants discovered (`./srv/...`) | **360** (343 testable, 17 not covered, 8 suppressed by `exclude-calls`) |
| COLD wall time, 242 mutants in api/hub/server/ws, bounded config | **231.32 s** |
| COLD wall time, tool defaults (`-w 6`), *contaminated measurement — see §5.2* | 585.14 s, with **120/360 TIMED OUT (33 %)** and no verdict for 119 mutants the bounded run judged |
| WARM wall time, identical command, cache hit | **2.52 s** (**91.8×**) |
| `conductor.go` testable mutants | 101, measured individually (§6.1); 1 memory runaway (§3) |
| Verdicts on all 343 testable mutants (bounded) | 242 KILLED, 72 LIVED, 28 NOT VIABLE, 17 NOT COVERED, **0 TIMED OUT**, 1 INFRA ERROR; efficacy **77.07 %**, coverage **94.86 %** |
| Machine impact at defaults | **stalled this VM twice** (see §2); at the bounded config, peak unit memory **617 MB** (sweep B; the 2.5 GB peak in §3 only appears on the `conductor.go` runaway), ≤2 CPUs, and the VM never below **5,216 MB** available during sweep B (**2,916 MB** across every run incl. the two stalls) |

---

## 1. What "caught", "efficacy" and "coverage" mean here

`gomutants` computes `efficacy = KILLED / (KILLED + LIVED)` and
`mutant coverage = (KILLED + LIVED) / (KILLED + LIVED + NOT_COVERED)`
(its own `docs/MUTATION_COVERAGE.md`). **`NOT VIABLE`, `NOT COVERED` and
`TIMED OUT` are excluded from the efficacy denominator**, so a mutant that is never
judged cannot lower the score. That is the single most important caveat when reading
any efficacy number below, and §5 shows it being exploited by CPU contention.

`NOT VIABLE` here is a compile-level outcome (the mutated package no longer builds or
the mutation cannot be applied), not a test verdict; this repo's own
`scripts/mutation-check.sh` deliberately reports that class as **WEAK** rather than as
a catch, and that distinction should be kept when comparing the two harnesses.

## 2. Resource profile, and the two times this VM locked up

The first two attempts to run this tool on this machine made it unresponsive.

**Attempt 1 — tool defaults (`-w 6`, `-test-cpu` unset).** `-w` defaults to `NumCPU`,
i.e. 6 workers, and each worker's inner `go test` inherits `GOMAXPROCS=6`. That is up
to six concurrent package builds (each with its own parallel compiler pool) plus six
test binaries on a 6-vCPU box. It did complete — `ELAPSED=585.28`, `elapsed_time`
585.14 s, exit 0 — but with a verdict distribution that is an artefact of the
configuration, not of the code (see §5), and the machine was unusable while it ran.

**Attempt 2 — `-w 1`, `GOMAXPROCS=2`, uncapped.** Reached 181 of 343 verdicts in
~69 s and then the VM stalled; no final report was written.

**What was actually happening** was found later, by capping the run and reading the
kernel log. With the unit capped at `MemoryMax=2500M`, the sweep died at exactly
197 of 343 verdicts and the kernel said why:

```
[  620.289121] [  18877]  1000 18877  1244918   632154   631813      341         0  5226496        0           200 srv.test
[  620.289123] oom-kill:constraint=CONSTRAINT_MEMCG,...,oom_memcg=/user.slice/user-1000.slice/user@1000.service/app.slice/kki3-cold.service,task=srv.test,pid=18877,uid=1000
[  620.289170] Memory cgroup out of memory: Killed process 18877 (srv.test)
               total-vm:4979672kB, anon-rss:2527252kB, file-rss:1364kB, ... UID:1000
```

**A single mutant's test binary grows to 2.5 GB RSS / 5 GB virtual address space in
about seven seconds** (see §3). Uncapped, one such process takes a third of this VM;
at `-w 6`, six of them are ~15 GB of demand on a 7.6 GiB machine with 2 GiB of swap —
which is exactly the thrash that stalled it. The curated grid never exposed this
because its 41 mutations are different edits (see §8).

**The bounded configuration that was used for every number in this report** (a
systemd user transient unit; the user manager has `cpu memory pids` delegated, so no
root is needed):

```bash
systemd-run --user --unit=kki3-sweepB --collect \
  -p MemoryMax=2500M -p MemorySwapMax=0 -p CPUQuota=200% -p TasksMax=128 \
  -p RuntimeMaxSec=1500 -p OOMPolicy=continue \
  -p WorkingDirectory=/home/exedev/strudel-agent \
  --setenv=GOMAXPROCS=2 --setenv=GOFLAGS=-p=2 \
  --setenv=PATH=/home/exedev/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  /bin/bash -lc 'timeout 1440 /usr/bin/time -f "ELAPSED=%e" -o /tmp/kki3/sweepB.time \
    gomutants -w 1 --exclude-files "conductor\.go$" -o=/tmp/kki3/report-B.json \
    -cache=/tmp/kki3/cache-B.json ./srv/... > /tmp/kki3/sweepB.log 2>&1'
```

Two properties matter: the cgroup makes an overload **structurally impossible** (2.5
GiB / 2 cores / 128 tasks / 25 min ceilings), and `-p OOMPolicy=continue` is required —
systemd's default `OOMPolicy=stop` kills the whole unit when the kernel OOM-kills any
process in it, which is what silently ended two of my earlier attempts.

Measured profile with that configuration (2 s sampling of the unit's cgroup during
sweep B): peak unit memory **617 MB**, peak tasks **8**, `test_binaries` ≤1, memory PSI
`full` at most **0.04** throughout, and the VM never dropped below **5,216 MB** (5.1 GiB)
available.
The 2.5 GB peak in §3 only appears on the `conductor.go` runaway mutant.

## 3. The runaway: one mutant kills a 2.5 GB cgroup in ~7 s

The first mutant the bounded sweep never reached was, in discovery order:

```
srv/conductor.go:(*Conductor).snapshotLocked:STATEMENT_REMOVE#1
```

It reproduces in isolation, in about seven seconds, in **three** configurations:

| Arm | Configuration | Result |
|---|---|---|
| A | cap 2.5 GB, no `GOMEMLIMIT` | unit OOM-killed after **7 s**†; gomutants then reports `produced no verdict: the mutant is PENDING` |
| C1 | cap 2.5 GB, `-timeout-coefficient 2` | unit OOM-killed after **7 s**; `produced no verdict: the mutant is INFRA ERROR` |
| C2 | cap 2.5 GB, `-timeout-coefficient 2`, `GOMEMLIMIT=1GiB` | unit OOM-killed after **9 s**; `produced no verdict: the mutant is INFRA ERROR` |

† C1 and C2 record their own elapsed time in `/tmp/kki3/killer3.log` (`ARM label=c1
exit=1 seconds=7`, `ARM label=c2 exit=1 seconds=9`). Arm A's 7 s is not in
`killer.log`, which holds only its start line: the whole unit, including the arm's
own writer, was OOM-killed before it could append a result. The 7 s comes from the
watchdog's 2 s samples for `kki3-killer` (active 15:13:16, `POST … unit_mem=0MB
psi_mem_full=1.26` at 15:13:24), and its `PENDING` verdict from
`/tmp/kki3/killer-nomemlimit.out`. The observed peak for that arm is only **285 MB**
in the samples, i.e. the sampling interval missed the spike — which is the point of
the hard cap, not evidence the spike was small.

`GOMEMLIMIT` does not save it, because the growth is live memory (the Go runtime GCs
until the heap limit and keeps allocating past it); and shortening the per-mutant wall
ceiling does not save it either, because the binary reaches the cap before the ceiling
fires. A **hard cgroup memory cap** is the only thing that stops it, and the cap is
what makes the failure safe: the process dies, the VM does not.

Reproduce exactly:

```bash
systemd-run --user --unit=kki3-killer --collect \
  -p MemoryMax=2500M -p MemorySwapMax=0 -p CPUQuota=200% -p TasksMax=128 \
  -p RuntimeMaxSec=400 -p OOMPolicy=continue \
  -p WorkingDirectory=/home/exedev/strudel-agent \
  --setenv=GOMAXPROCS=2 --setenv=GOFLAGS=-p=2 --setenv=PATH=/home/exedev/go/bin:/usr/bin:/bin \
  /bin/bash -lc 'timeout 150 gomutants -w 1 -run-mutant-id \
     "srv/conductor.go:(*Conductor).snapshotLocked:STATEMENT_REMOVE#1" -cache=off \
     -o=/tmp/kki3/killer-nomemlimit.json ./srv/...'
journalctl --user -u kki3-killer --no-pager | tail -5   # "killed by the OOM killer"
# NB: this arm is expected to be OOM-killed along with its own unit, so the
# wrapper never appends a result line — see the † note above.
```

This is a **defect in the test suite, surfaced by the tool**: a conductor history /
snapshot path that, when a statement is removed, makes a test allocate without bound
instead of failing an assertion. It is the memory-axis sibling of the unbounded-wait
defects fixed in epic `e2v`. The curated grid does not reach it — its closest mutation,
`21-history-window-wrong-start` (`start := c.histNext - c.histLen` → `start := 0`), is
caught normally in 5.4 s raw (1.8 s baseline-subtracted, `01-baseline.md`), which is why 41/41 stayed green in 133.92 s.

**Consequence for the product decision (kki.6):** on this codebase, gomutants cannot be
run on a developer machine without a memory-capped runner *and* without fixing that
suite defect. "Run the mutation tool on your laptop before pushing" — the workflow the
tool's README sells — is not available here as things stand.

## 4. Mutant count and discovery

```
$ gomutants --dry-run ./srv/...
Discovering mutants... 360 found (17 not covered, 343 to test)
Collecting coverage... srv.exe.dev/srv  1.65s  coverage: 96.2% of statements
Workers: 6 | Mutations: 28 types enabled
```

`./...` discovers the identical 360 mutants (`cmd/srv` yields 0 mutants and 0.0 %
statement coverage), so the two scopes are interchangeable for this question.

Per mutator type (dry run, all 360 discovered mutants):

| Type | n | Type | n | Type | n |
|---|---:|---|---:|---|---:|
| STATEMENT_REMOVE | 101 | RETURN_ZERO | 20 | RETURN_FALSE | 4 |
| BRANCH_IF | 50 | RETURN_ERROR_NIL | 14 | RANGE_BREAK | 4 |
| CONDITIONALS_NEGATION | 48 | EXPRESSION_REMOVE | 12 | REMOVE_LOGICAL_NOT | 3 |
| INTEGER_INCREMENT | 27 | CONDITIONALS_BOUNDARY | 12 | INCREMENT_DECREMENT | 3 |
| INTEGER_DECREMENT | 27 | ARITHMETIC_BASE | 10 | LOOP_CONDITION | 1 |
| RETURN_TRUE | 7 | INVERT_LOGICAL | 6 | INVERT_NEGATIVES | 1 |
| ERRORF_WRAP | 5 | REMOVE_SELF_ASSIGNMENTS | 1 | INVERT_BITWISE | 1 |
| INVERT_ASSIGNMENTS | 1 | FLOAT_INCREMENT | 1 | FLOAT_DECREMENT | 1 |

`gomutants --list-mutators` registers 28 mutator types in total
(`ARITHMETIC_BASE` … `STATEMENT_REMOVE`); the table above lists only those that
produced at least one mutant here. The full catalogue is saved at
`/tmp/kki3/list-mutators.txt` and `.5` needs it for the reach comparison.


## 5. Wall time and verdicts, and what the defaults really report

### 5.1 Measured, bounded configuration

Measured on the 242-mutant subset that excludes `conductor.go` (see §3 for why it must
be excluded from a single sweep), identical command for both rows:

| | Command | Wall time | Outcome |
|---|---|---|---|
| **COLD** | `gomutants -w 1 --exclude-files "conductor\.go$" -o=... -cache=/tmp/kki3/cache-B.json ./srv/...` | **231.32 s** | Killed 157, Lived 60, Not viable 25, Not covered 14, Timed out 0, Suppressed 8, efficacy **72.35 %**, coverage **94.53 %** |
| **WARM** | same command again (all 242 verdicts in cache) | **2.52 s** | `Cache: 242 mutant outcomes reused`, same verdicts |

`mutants_total` in the JSON is 256 (the discovery count for that scope); 242 were
tested, 14 were not covered.

The 91.8× warm speedup is real and reproducible here; it is the tool's headline claim
(its README advertises 120–150× on larger projects) measured on this small codebase
instead of quoted. Cache identity is content-addressed on the mutated production file,
the covering tests, and the `--test-flags` value, so **any edit to `srv/*.go` or to the
tests that cover a mutant invalidates exactly those entries** — which is exactly the
edit-review loop this repo cares about, and why the warm number is the relevant one for
a pre-push gate and the cold number is the one that matters after a rebase.

### 5.2 The same mutants at the tool's defaults — and why the numbers lie

Running the same 256-mutant scope with the **tool's defaults** (`-w 6`, 6 vCPUs
available, no cap) gave `ELAPSED=585.28`, `elapsed_time` 585.14 s, exit 0, and a
globally clean-looking summary: `Killed: 193 (90.2 %)`, `Lived: 21 (9.8 %)`,
`Timed out: 120`, outcome efficacy **90.19 %**.

Joining the two reports per mutant id (same code, same commit, same machine) shows what
those 120 timeouts were:

| bounded run (this report) | default run | mutants |
|---|---|---:|
| KILLED | KILLED | 106 |
| KILLED | **TIMED OUT** | 51 |
| LIVED | **TIMED OUT** | 49 |
| NOT VIABLE | **TIMED OUT** | 19 |
| LIVED | LIVED | 10 |
| NOT VIABLE | NOT VIABLE | 6 |
| NOT COVERED | NOT COVERED | 14 |
| LIVED | KILLED | 1 |

**119 of 256 mutants (46 %) had no verdict at all under the defaults**, and because
`TIMED OUT` is excluded from the efficacy denominator, the tool reported a *cleaner*
90.19 % while silently dropping a third of the verdicts — including **49 mutants that
are genuine survivors**. The bounded run reports 72.35 % on the same code. The
difference is entirely configuration (concurrent `go test` builds starving each other past
the adaptive per-mutant timeout), not code quality and not tool capability.

This is the same failure mode the tool's own README warns about for `--test-flags`:
"*under -race a deadline measured without it would fire early, turning survivors into
TIMED_OUT and quietly dropping them out of the efficacy denominator*". Here the trigger
is CPU starvation rather than a flag, and the mitigation is concurrency, not flags.


## 6. The 72 survivors, categorised (60 sweep + 12 conductor; §6.1)

Per file (sweep B, the 256 discovery mutants of api/hub/server/ws; conductor.go's 12 survivors are listed in §6.1):

| File | KILLED | LIVED | NOT VIABLE | NOT COVERED | efficacy |
|---|---:|---:|---:|---:|---:|
| api.go | 100 | 6 | 4 | 0 | 94.3 % |
| hub.go | 27 | 15 | 12 | 7 | 64.3 % |
| server.go | 20 | 30 | 6 | 6 | 40.0 % |
| ws.go | 10 | 9 | 3 | 1 | 52.6 % |
| subtotal (sweep B, 256 discovery mutants) | **157** | **60** | **25** | **14** | **72.35 %** |

Clusters, with representative examples (`file:line type original → replacement`):

**1. Unobservable numeric literals — 18 survivors.** Buffer sizes and channel
capacities whose exact value no test reads:
`api.go:20 INTEGER_DECREMENT 64 → 63`, `hub.go:66 64 → 63`, `hub.go:66 64 → 65`,
`hub.go:180 1 → 0`/`1 → 2`, `hub.go:211`, `hub.go:232`, `server.go:19 32 → 31`,
`server.go:55 0 → 1`, `ws.go:19 5 → 4`/`5 → 6`.
These are not test-quality gaps in any meaningful sense: `HubDefaultSendBuffer` and
friends behave identically at any sane value. The tool's own self-test documents the
same class (`0o644` → `0o645` file modes) as not worth chasing.

**2. `server.go` header/host forwarding — 21 survivors, the largest cluster.**
`server.go:121 EXPRESSION_REMOVE host == "exe.cloud" → false`,
`server.go:125 host == "exe.dev" → false`, `server.go:150 EXPRESSION_REMOVE
strings.HasPrefix(lower, "x-exedev-") → false`,
`server.go:150 INVERT_LOGICAL || → &&`,
`server.go:161 RETURN_TRUE / RETURN_FALSE / CONDITIONALS_BOUNDARY / CONDITIONALS_NEGATION
on the header sort comparator`, `server.go:153/154 Host-header append`,
`server.go:147 STATEMENT_REMOVE of the header append`.
The suite does not assert the forwarded-header set or the header ordering, and the
deployment-specific host names (`exe.cloud`, `exe.dev`) are untested. This *is* real
untested behaviour — the most interesting cluster in the report — but it is also the
kind of thing the curated grid never tries to mutate (see §9).

**3. `hub.go` close/unsubscribe paths — 10 survivors.**
`hub.go:256 STATEMENT_REMOVE <-reply → _ = 0`, `hub.go:258 <-h.done → _ = 0`,
`hub.go:188 BRANCH_IF` (closed-hub `return nil, ErrHubClosed`), `hub.go:208 BRANCH_IF`
(`return false`), `hub.go:134` (`<=` → `<`, `0` → `-1`, the default-send-buffer branch).
The unsubscribe/close races are covered for *delivery* but not for these branches.

**4. Log-only branches and messages — 5 survivors.** `ws.go:86`, `ws.go:179`,
`ws.go:189`, `ws.go:149` (each `BRANCH_IF` wrapping a single `slog.Debug`),
`server.go:84` (`slog.Warn`). The tool's built-in `exclude-calls` defaults suppress
mutants *inside* `slog.Debug*/slog.Warn*` calls but not the *branch* around one, so
these survive by construction.

**5. Error wrapping and error classification — 3 survivors.**
`api.go:253 ERRORF_WRAP %w → %v`, `api.go:261 %w → %v`,
`api.go:305 EXPRESSION_REMOVE errors.Is(err, io.ErrUnexpectedEOF) → false`.
`%v` vs `%w` is observable only via `errors.Is`; the suite checks the status code, not
the chain — the same class as the curated grid's `17-stale-report-regresses` neighbour.

**6. Return-path shapes — the remainder**, e.g. `api.go:241 BRANCH_IF { return nil }`,
`server.go:103`/`server.go:106` (template parse/execute error returns, which are also
`NOT COVERED` neighbours), `server.go:163 RETURN_ZERO headers → nil`,
`hub.go:188`/`208` above, `server.go:145 RANGE_BREAK` (inserted `break` in the header
loop), `server.go:161` sort comparators.

### 6.1 conductor.go: per-mutant completion

`conductor.go` holds 104 of the 360 mutants (3 not covered) but **one of its mutants
kills any cgroup with under ~2.5 GB** (§3), so no single sweep can judge the file's 101
testable mutants. They were measured individually instead, one bounded
`--run-mutant-id` invocation per mutant (`/tmp/kki3/conductor-permutant.sh`, each under
`timeout 90` inside the same `OOMPolicy=continue` caps):

| Verdict | n | Notes |
|---|---|---|
| KILLED | 85 | full per-mutant JSON in `/tmp/kki3/mut/<n>.json` |
| LIVED | 12 | 4 `INTEGER_INCREMENT`, 3 `INTEGER_DECREMENT`, 3 `CONDITIONALS_BOUNDARY`, 2 `BRANCH_IF` |
| NOT VIABLE | 3 | `RecordEvalResult:BRANCH_IF#1`, `RecordEvalResult:RETURN_ERROR_NIL#1`, `snapshotLocked:INTEGER_DECREMENT#1` |
| INFRA ERROR (memory runaway) | 1 | `(*Conductor).snapshotLocked:STATEMENT_REMOVE#1` — the §3 mutant, OOM-killed at 2.5 GB in ~7 s in isolation |
| TIMED OUT | 0 | |

Effort: 101 invocations, 493 s total (≈4.9 s each), measured out-of-band in
`/tmp/kki3/conductor-permutant.tsv` (`id, status, seconds, exit`). Each invocation
re-pays gomutants' fixed phases (package resolve, coverage 1.9 s, baseline 1.9 s,
discovery, per-test coverage map ≈ 5.6 s in all), so the per-mutant verdicts are
measured in the tool's documented single-mutant workflow but are **not** directly
summable into a sweep wall time. `snapshotLocked` boundary and numeric-literal mutants
dominate its 12 survivors, matching the repo's own experience that version/counter
boundaries are assertion-thin territory (cf. curated mutations 17–20).

**Combined, whole-codebase verdict set** (242 sweep mutants + 101 per-mutant):

| | KILLED | LIVED | NOT VIABLE | NOT COVERED | TIMED OUT | INFRA ERROR | suppressed |
|---|---:|---:|---:|---:|---:|---:|---:|
| 343 testable (of 360 discovered) | 242 | 72 | 28 | 17 | **0** | 1 | 8 |

Efficacy and coverage over the judged mutants, computed with the tool's own formulas
(see §1) and verified with `awk`:

```awk
# K=242 L=72 NC=17
printf "efficacy=%d/(%d+%d)=%.2f%%\ncoverage=(%d+%d)/(%d+%d+%d)=%.2f%%\n" \
  242 242 72 100*242/314 242 72 242 72 17 100*314/331
# -> efficacy=242/(242+72)=77.07%
# -> coverage=(242+72)/(242+72+17)=94.86%
```

Effort for the wall clock on the record: 231.32 s (sweep B, 242 mutants) + the
conductor per-mutant series at 493 s — but the per-mutant series' 493 s re-pays
fixed phases ~101 times, so a *single* conductor sweep would have cost a fraction of
it; the honest aggregate statement is "a complete one-command sweep of all 343 mutants
is not achievable on this machine, and the measured cost of judging them is ≥231 s
plus the conductor mutants' test time". Any `.6` denominator must use this reading,
not a projection.

Clusters 1–6 above are drawn from sweep B's 60 survivors; conductor.go's 12 survivors
(§6.1) are numeric-literal and boundary mutants of the same families and change the
picture only in degree, not in kind.


**Reading of the 77.07 %, not the interim 72.35 %:** most of the 72 survivors are mutants
details that no test can reasonably assert (buffer sizes, log branches), not evidence of
a weak suite. The exception is cluster 2 (header/host forwarding): 21 mutants in one
function with no assertions is worth a follow-up issue *on its own merits*, independent
of which mutation tool produced it. Anyone comparing gomutants' 77 % against the
curated grid's 41/41 must remember the two sets answer different questions.


## 7. Cache behaviour, and a resume trap that can report green on an incomplete run

What the cache is keyed on, verified from the tool's own `--help`/config and from
observation: per-mutant outcomes are keyed by content hash of the mutated production
file, the covering tests, the package, and the `--test-flags` value. Whitespace-only
edits do not invalidate; changing `--test-flags` (including its order) does. This is
what makes the 2.52 s warm rerun legitimate, and it also means an edit to `srv/*.go`
re-runs only the mutants that touch the edited file and its covering tests.

The cache file is a JSON object; `jq -r '.entries|length'` is a reliable completion
counter (343 = every testable mutant judged). Post-run reports in chunk 3 remain valid,
`mutants_cached` in the JSON records what was reused.

**The trap, measured.** gomutants writes a partial cache when a run is interrupted (its
10 s checkpoint, and a graceful flush on SIGTERM/OOM), but **a later run with that same
cache does not continue the unfinished work**: it reports the cached verdicts and exits
0, leaving the rest `PENDING`. Evidence, from two independent incidents in this session:

* A run killed mid-flight (by the watchdog, §2) left a cache with **197 of 343**
  verdicts. The immediately following identical invocation printed
  `Cache: 197 mutant outcomes reused from /tmp/kki3/cache-w1.json`, `Cached: 197
  (skipped)`, `Timed out: 0`, `Efficacy: 91.05%`, `elapsed_time 19.7` — **exit 0** — and
  its JSON report shows `146 PENDING` of 360 (`/tmp/kki3/report-chunk-2.json`,
  status histogram `KILLED 173, PENDING 146, NOT COVERED 17, LIVED 17, NOT VIABLE 7`).
* The first capped sweep was OOM-killed at 197 verdicts for the same reason and left
  `146 PENDING` in `/tmp/kki3/report-cold.json`.

`PENDING` is not counted as survived, not counted as caught, and does not stop the
process from exiting 0 with a summary line. Any CI gate built on a cache-enabled
gomutants run can therefore be **green on a run that judged a third of the mutants** —
which is worth knowing *before* anyone wires this into `make verify`.

**How this report avoided it:** each sweep used a **fresh cache path** and was allowed
to finish uninterrupted inside the memory-capped unit; the one file that cannot finish
in a sweep (`conductor.go`) was measured with `--run-mutant-id`, which bypasses the
cache for that mutant by design.

## 8. gomutants versus the repo's own curated grid

For the decision in `.6`, the honest side-by-side with what the repo already has:

| | `scripts/mutation-check.sh` (routed) | gomutants v0.6.1 (bounded) |
|---|---|---|
| mutants | 41, hand-written, each one behavioural claim | 360 generated, 343 testable |
| cold wall time | **133.92 s** (whole grid, routed; 312.68 s unrouted) | **231.32 s** for 242 mutants in api/hub/server/ws (§5.1) **plus** 101 `conductor.go` mutants measured individually at 493 s all-in / 4.9 s mean, 7 s max (§6.1); a complete one-command sweep is not achievable on this machine (§3) |
| rerun after no change | 133.92 s (no cache) | **2.52 s** (cache) |
| verdict vocabulary | caught / SURVIVED / WEAK / BROKEN; a compile failure is WEAK, never a catch | KILLED (242) / LIVED (72) / NOT VIABLE (28) / NOT COVERED (17) / TIMED OUT (**0**) / EQUIVALENT (**0**, §8) / INFRA ERROR (1, §3); efficacy 77.07 %, coverage 94.86 %, both excluding TIMED OUT |
| failure mode on this VM | none observed (sequential `go test`, ~3.6 s per mutation, no cap needed) | **default `-w 6` stalls a 6-vCPU box**; one mutant's test binary reaches 2.5 GB |
| setup | bash + python3, no network | `go install` (network), plus cache/report files that must be redirected out of the repo |

`--detect-equivalent` was **not** enabled (its default is false), so `EQUIVALENT` is 0 by
construction in every table here — not because none exist. Enabling it costs one package
compile per survivor and would shrink the efficacy denominator further; if `.4`/`.6`
want that number it must be measured separately and labelled.


## 9. What this report deliberately does NOT answer

* **Can gomutants catch a HANG-shaped defect?** Not tested here — the epic's decisive
  experiment is `.4`. What this report contributes to it: `TIMED OUT` is excluded from
  efficacy, and on this codebase the hang-shaped mutant class did **not** produce
  timeouts when run one-at-a-time on an idle machine (0 timeouts in 242 bounded
  verdicts), while the *same* mutants produced 119 timeouts under CPU contention. Any
  `.4` experiment must pin `-w 1` and an idle machine or it will measure scheduling, not
  hangs.
* **Traceability of the 41 curated mutations against gomutants' generated set** — that
  is `.5`. Inputs are saved for it (§10).
* **Adopt / don't adopt** — that is `.6`. This report gives it numbers, not a verdict.
* **`-race` comparability.** The repo's gate runs `go test ./... -race -count=1`; none
  of the numbers here used `-race`, because `--test-flags` also stands adaptive timeouts
  down to the global ceiling (`baseline × -timeout-coefficient`), which changes what
  `TIMED OUT` means. A `-race` figure is a different measurement with a worse memory
  profile (the runaway would hit the cap sooner) and should be measured deliberately,
  not blended in.
* **`--integration`, `--detect-equivalent`, `--changed-since`** were not exercised.
  `--changed-since` is what would make the warm cache usable as a pre-push gate and is a
  natural follow-up measurement.

## 10. Artifacts (all outside the repo, as `kki.3` requires)

Nothing gomutants produced was written into the working tree: mutations are applied with
`go test -overlay`, and both `-o` and `-cache` were redirected to `/tmp/kki3`.

| Path | What it is |
|---|---|
| `/tmp/kki3/report-B.json` | sweep B report: 256 mutants, verdicts + `mutator_statistics`, `test_efficacy`, `mutations_coverage` |
| `/tmp/kki3/report-B-warm.json` | the warm (cache-hit) rerun of the same command |
| `/tmp/kki3/cache-B.json` | sweep B's incremental cache (242 entries) |
| `/tmp/kki3/sweepB.log`, `sweepB.time` | sweep B output and `ELAPSED=231.32` |
| `/tmp/kki3/warmB.log`, `warmB.time` | warm run output and `ELAPSED=2.52` |
| `/tmp/kki3/report-srv.json`, `cache-srv.json`, `cold.log`, `cold.time` | the tool-defaults run (`-w 6`), `ELAPSED=585.28`, 120 TIMED OUT |
| `/tmp/kki3/report-cold.json`, `cache-cold.json` | the first capped sweep, OOM-aborted at 197/343 with 146 `PENDING` |
| `/tmp/kki3/report-chunk-2.json` | the resume trap's evidence (146 `PENDING`, exit 0) |
| `/tmp/kki3/killer*.out`, `killer*.log`, `killer*.json` | the three runaway-mutant arms (§3) |
| `/tmp/kki3/list-mutators.txt`, `help.txt`, `version.txt` | mutator catalogue (28 types) and the pinned tool's flag surface, for `.5` |
| `/tmp/kki3/dry-srv.txt`, `dry-all.txt` | `--dry-run` discovery for `./srv/...` and `./...` (identical 360 mutants) |
| `/tmp/kki3/mut/1..101.json` (+`.out`) | per-mutant `conductor.go` verdicts (101 ids in `conductor-permutant.tsv` order) |
| `/tmp/kki3/conductor-permutant.tsv` | `conductor.go` per-mutant completion: `id, status, seconds, exit` |
| `/tmp/kki3/watchdog.log` | 2 s resource samples (unit memory, tasks, test binaries, memory PSI) for every run |
| `/tmp/kki3/watchdog.sh`, `probe.sh`, `killer-probe*.sh`, `conductor-permutant.sh` | the bounded-run harness scripts (kept out of the repo on purpose: `.3` may add no repo file but this document) |

`/tmp` is not durable. `.5`/`.6` should copy what they need (at minimum
`report-B.json`, `cache-B.json`, `list-mutators.txt`, `report-srv.json`,
`conductor-permutant.tsv`) somewhere that survives, or re-run the documented commands.


## 11. Reproducing, and the safety rules that make it repeatable

Install and version-pin (Go ≥ 1.26.1 required by the module; host is 1.27.1):

```bash
timeout 300 go install github.com/szhekpisov/gomutants@v0.6.1
export PATH="$PATH:$(go env GOPATH)/bin"     # $GOPATH/bin is NOT on PATH on this VM
gomutants --version          # gomutants v0.6.1 (commit: none, built: unknown)
gomutants --help > /tmp/kki3/help.txt 2>&1
gomutants --list-mutators > /tmp/kki3/list-mutators.txt 2>&1
gomutants --dry-run ./srv/... > /tmp/kki3/dry-srv.txt 2>&1   # 360 found, 17 not covered
```

Cold and warm, exactly as measured (`conductor\.go$` excluded, see §3):

```bash
RUN='gomutants -w 1 --exclude-files "conductor\.go$" -o=/tmp/kki3/report-B.json \
       -cache=/tmp/kki3/cache-B.json ./srv/...'

# COLD  -> ELAPSED=231.32
systemd-run --user --unit=kki3-sweepB --collect \
  -p MemoryMax=2500M -p MemorySwapMax=0 -p CPUQuota=200% -p TasksMax=128 \
  -p RuntimeMaxSec=1500 -p OOMPolicy=continue -p WorkingDirectory=/home/exedev/strudel-agent \
  --setenv=GOMAXPROCS=2 --setenv=GOFLAGS=-p=2 \
  --setenv=PATH=/home/exedev/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  /bin/bash -lc "timeout 1440 /usr/bin/time -f 'ELAPSED=%e' -o /tmp/kki3/sweepB.time $RUN \
                 > /tmp/kki3/sweepB.log 2>&1"

# WARM  -> ELAPSED=2.52 (identical command again)
```

Rules that must not be relaxed on this VM (each is here because breaking it produced a
measurable failure):

1. **Never run gomutants unbounded, and never at the default `-w`.** `-w 1` plus a
   memory cap. `-w 6` on a 6-vCPU box starves the inner `go test` runs into 119 false
   `TIMED OUT` verdicts and makes the machine unusable.
2. **Cap memory in a cgroup** (`MemoryMax`), not with `GOMAXPROCS`/`GOMEMLIMIT` — the
   runaway allocation is live memory, so only a hard cap stops it.
3. **`OOMPolicy=continue`**, or the first OOM kill silently stops the whole unit.
4. **One measurement at a time.** Do not run the curated grid, `make verify`, or another
   sweep concurrently with a timed run; wall-clock numbers and `TIMED OUT` verdicts both
   move.
5. **Fresh cache path per sweep** when completeness matters (§7), and check
   `jq -r '.entries|length'` plus the `PENDING` count before believing an `exit 0`.
6. **Redirect `-o` and `-cache` outside the repo** and assert `git status --porcelain`
   is clean afterwards; nothing else may write into the tree.

Verification state at the end of this subtask: `make verify` green, `git status
--porcelain` showing only this document, no `.gomutants.yml` and no
`mutation-report.json`/`.gomutants-cache.json` anywhere in the repo, and no
`gomutants`/`*.test` process left running.

