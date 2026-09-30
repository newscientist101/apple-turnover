# Mutation 18: from accidental catch to owned coverage

**Issue:** `strudel-agent-bgb` (found by `kki.2`, `02-routing.md` §7)
**Machine:** Linux ROGQ58 6.18.33.2-microsoft-standard-WSL2 (WSL2)
**Go version:** go1.27.1 linux/amd64
**Base commit:** `5f5b659`
**Change:** one new test, `TestConductorRecordEvalResultSameVersionReplaces`, in
`srv/conductor_eval_test.go`. No production code, no other test file, no change
to `scripts/mutation-check.sh`.

---

## 1. The gap

`18-same-version-report-ignored` flips the `lastEval` staleness guard in
`RecordEvalResult` (`srv/conductor.go:179`):

```go
-if c.lastEval != nil && res.Version < c.lastEval.Version {
+if c.lastEval != nil && res.Version <= c.lastEval.Version {
```

A report for the **same** version as the stored verdict is then dropped instead
of replacing it. The mutation was caught by the grid, but only by an API test
whose stated purpose is *"failure reports carry the error text verbatim"*
(`srv/api_test.go:548`). The conductor's own tests did not notice, because every
negative case they exercise reports a version **strictly older** than the stored
one (version 1 against a stored version 2), which both `<` and `<=` reject.

This is a real product behaviour, not a hypothetical: the browser client
re-reports a verdict for the current version, and "the latest report for a
version wins" is what makes a re-run's verdict replace the previous one.

## 2. RED — the before state, measured

In a throwaway worktree (`git worktree add /tmp/bgb18 5f5b659`), letting
`mutation-check.sh` apply the mutation itself:

```bash
MUTATION_TEST_ARGS="-run TestConductor" GO_TEST_TIMEOUT=60s MUTATION_TIMEOUT=150 \
  timeout 250 ./scripts/mutation-check.sh 18-same-version-report-ignored
```

```
18-same-version-report-ignored         SURVIVED  <-- the tests did not notice this defect
mutation-check: 0 caught, 1 survived, 0 weak, 0 broken anchor(s)   (3.16s, exit 1)
```

The accidental catcher, for contrast:

```bash
MUTATION_TEST_ARGS="-run TestAPIEvalResultSuccess" ... 18-same-version-report-ignored
```

```
18-same-version-report-ignored   --- FAIL: TestAPIEvalResultSuccess (0.00s)
          api_test.go:556: stored ok = true, want false
      panic: interface conversion: interface {} is nil, not string [recovered, repanicked]
caught
```

**Worth recording:** this catch is partly a **panic**, not only the assertion.
The assertion at `api_test.go:556` fires first, but the test then dies on a
type assertion while decoding the unchanged `lastEvalResult`. So the previous
proof that mutation 18 is caught at all rested on a test that fails partly for
the wrong reason — a weaker guarantee than a clean named assertion.

## 3. The fix

A new test rather than extra cases inside `TestConductorRecordEvalResult`, so the
boundary has a name of its own and the existing assertions stay byte-for-byte
unchanged. It stores `ok=true` for version 1, re-reports **the same version**
with `ok=false`, and asserts the stored verdict changed — both the `OK` flag and
the `Error` text, so it pins *replacement* rather than mere non-nil-ness.

## 4. GREEN — the after state, measured

Unmutated tree:

```bash
go test ./srv/... -race -count=1 -run TestConductor -timeout 60s
# → ok  srv.exe.dev/srv  1.014s
```

Same worktree, mutation 18 applied, new test present:

```
18-same-version-report-ignored   --- FAIL: TestConductorRecordEvalResultSameVersionReplaces (0.00s)
          conductor_eval_test.go:149: same-version re-report was ignored: stored OK = true, want false (the latest report for a version wins)
          conductor_eval_test.go:155: LastEvalResult.Error = "", want the re-reported text: a same-version report replaces the stored verdict wholesale
caught
mutation-check: 1 caught, 0 survived, 0 weak, 0 broken anchor(s)   (2.51s, exit 0)
```

`SURVIVED` → `caught`, with **two clean assertion failures and no panic**. The
mutation is now owned by the test whose name says what it pins.

Full grid, sequential, to confirm nothing else weakened:

```bash
timeout 900 ./scripts/mutation-check.sh
```

```
mutation-check: 41 caught, 0 survived, 0 weak, 0 broken anchor(s)
mutation-check: PASSED — every mutation was caught; the suite is non-vacuous.
WALL=146.05   RC=0
```

41/41 with **0** `panic: test timed out` and **0** `DATA RACE`. The 146.05s is
in line with the 128.14s measured minutes earlier on the same machine for the
same grid; the delta is noise plus the one extra test, not a regression. The
script's own sha256 revert check passed for all 41 mutations and left every
tracked file untouched. `make verify` green (`ok srv.exe.dev/srv 2.782s`).

The grid log still names `TestAPIEvalResultSuccess` for mutation 18, because
`run_tests` prints only the first 3 matching lines and the API test fails
first. The new test is a genuine *second* catcher — proved by the worktree run
in §4, where the API test was excluded entirely by `-run TestConductor` and
mutation 18 was still caught, cleanly, by the new test alone.

Unmutated, all 11 conductor tests pass, the new one included:

```
--- PASS: TestConductorRecordEvalResult (0.00s)
--- PASS: TestConductorRecordEvalResultSameVersionReplaces (0.00s)
ok   srv.exe.dev/srv  1.016s
```

## 5. No collateral damage

`git diff --stat` touches `srv/conductor_eval_test.go` and this file only.
`scripts/mutation-check.sh` is untouched, so mutation 18 gains **no** 5th routing
field: it is caught by the conductor tests themselves. That is deliberate. A
routing regex would have made the grid report `caught` while the *other* nine
conductor tests still failed to distinguish `<` from `<=`, which is precisely
the silent-coverage-loss mode this issue exists to remove.

## 6. Correction to `02-routing.md` §7

That section concluded *"The defect is only observable through the HTTP
surface."* That was true of the suite as it stood and is **no longer true**:
the defect is now observable directly, through
`TestConductorRecordEvalResultSameVersionReplaces`. Its warning still stands in
general — routing 18 to a Conductor test would have been wrong, and would have
been wrong *silently* — but the reason is now that the test did not exist, not
that the boundary is unobservable from the conductor. `02-routing.md` §7 has
been annotated in place rather than rewritten, since its measurement was correct
when taken.

## 7. Provenance

| Number | How obtained |
|---|---|
| `SURVIVED`, 3.16s, exit 1 | worktree `/tmp/bgb18` at `5f5b659`, script applied the mutation |
| API catch incl. panic | same worktree, `MUTATION_TEST_ARGS="-run TestAPIEvalResultSuccess"` |
| `ok srv 1.014s` | unmutated real tree, `-run TestConductor` |
| `caught`, 2.51s, exit 0 | worktree, new test copied in, mutation applied |
| 41 caught / 0 survived / 0 weak / 0 broken, 146.05s, exit 0 | §4, full grid on the real tree, run sequentially |
| 0 timeout panics, 0 data races | `grep -c` over the grid log |
| `ok srv 1.016s`, 11 conductor tests PASS | §4, unmutated real tree, `-run TestConductor -v` |
| 46 insertions, 0 deletions | `git diff --stat srv/conductor_eval_test.go` |
| mutation 18 still has 4 fields | `grep '^18-' scripts/mutation-check.sh \| awk -F'\|' '{print NF}'` |

All measurements were bounded with `timeout`. The worktree was removed with
`git worktree remove --force` afterwards; no mutation was ever applied to the
real tree.