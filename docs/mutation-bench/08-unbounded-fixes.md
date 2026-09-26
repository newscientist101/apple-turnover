# Unbounded-Wait Fixes: Class-Level Proof for the Count Path

**Date:** 2026-09-25
**Tree reported on:** `45617ca` ("Bound every remaining plain SubscriberCount assertion (issue strudel-agent-e2v.1.3.3)"), main worktree, clean.
**Fixes measured:** commits `bb9e138` (.1.3.1), `189f0be` (.1.3.2), `45617ca` (.1.3.3) — `srv/*_test.go` only.
**Machine:** `Linux ROGQ58 6.18.33.2-microsoft-standard-WSL2 #1 SMP PREEMPT_DYNAMIC Thu Jun 18 21:54:43 UTC 2026 x86_64 x86_64 x86_64 GNU/Linux`; 12th Gen Intel(R) Core(TM) i7-12700K, 20 CPUs, 7 GiB RAM; `go version go1.27.1 linux/amd64`.

This is the sweep-level proof that closes `strudel-agent-e2v.1.3`. It re-runs the proofs of the three closed subtasks as one class-level result, corrects the audit's RED-tool claim, and records the non-vacuity and wall-time numbers. It changes no test file: every measurement below was taken in a disposable `git worktree`, and no `srv/*_test.go` edit is proposed.

---

## 0. The defect, stated once

`Hub.SubscriberCount()` (`srv/hub.go:231`) posts a reply channel to the hub goroutine and then receives the answer inside a second `select`:

```go
func (h *Hub) SubscriberCount() int {
	reply := make(chan int, 1)
	select {
	case h.count <- reply:
	case <-h.done:
		return 0
	}
	select {
	case n := <-reply:      // srv/hub.go:238 — parks here if the hub never answers
		return n
	case <-h.done:
		return 0
	}
}
```

If the hub goroutine stops servicing `h.count`, the receive at `srv/hub.go:238` blocks forever. Any test-side bound placed *around* the call is ineffective, because control never returns to the code that checks the bound. That is the class: every unguarded `SubscriberCount()` call in test code was an unbounded wait, whatever comment sat next to it.

## 1. The RED tool: a count-only wedge

Every measurement below uses one hand-applied mutation of `srv/hub.go` in a worktree, applied and then discarded:

```python
old = "\t\tcase reply := <-h.count:\n\t\t\treply <- len(subs)\n"
new = "\t\tcase reply := <-h.count:\n\t\t\t_ = reply // deliberately wedged: the count is never answered, but the hub keeps serving\n"
```

The hub keeps serving subscribe, unsubscribe, broadcast and close; it only stops answering count. That is exactly the state the count-path sites cannot survive, and it is the state mutation `30-hub-slow-client-blocks` does **not** produce (Section 5).

## 2. Sites fixed

Line numbers as of `45617ca`, grep-verified (`grep -n 'hubSubscriberCount' srv/*_test.go`).

| Site (as of `45617ca`) | Site before the fix | Shape | Fix | Subtask |
|---|---|---|---|---|
| `srv/hub_test.go:58` | — | helper | new bound `hubCountTimeout = 2 * time.Second` | .1.3.1 |
| `srv/hub_test.go:116` | — | helper | new `hubSubscriberCountWithin(h, d) (int, bool)` — runs the call on a goroutine, selects against `time.After(d)` | .1.3.1 |
| `srv/hub_test.go:131` | — | helper | new `hubSubscriberCount(t, h) int` — same under `hubCountTimeout`, fails naming the wedge | .1.3.1 |
| `srv/ws_test.go:179` (`wsWaitForSubscribers`) | `srv/ws_test.go:170` | **B** | loop now polls through `hubSubscriberCountWithin`; the deadline-after-the-call shape is gone, and the second unbounded call in the `t.Fatalf` argument (`ws_test.go:174`) is dropped | .1.3.1 |
| `srv/hub_test.go:484` (churn watcher) | `srv/hub_test.go:432` | **A+B** | watcher reads through `hubSubscriberCountWithin` and **returns** on `!ok`, so `close(stop)` can now actually reach the watcher's exit instead of leaving it parked | .1.3.2 |
| `srv/hub_test.go:586` (post-churn assertion) | `srv/hub_test.go:529` at `81c80e5` (`570` at `189f0be`, `583` mid-.1.3.2) | bare | `hubSubscriberCount(t, h)` | .1.3.2 |
| `srv/hub_test.go:603-607` (success-path `<-watchDone`) | `srv/hub_test.go:542` | **A** | bare receive replaced by the same `select { case <-watchDone: case <-time.After(hubWatchdogTimeout): }` the watchdog branch already used | .1.3.2 |
| `srv/hub_test.go:204` | `srv/hub_test.go:160` | B | `hubSubscriberCount(t, h)` | .1.3.3 |
| `srv/hub_test.go:253` | `srv/hub_test.go:209` | B | `hubSubscriberCount(t, h)` | .1.3.3 |
| `srv/hub_test.go:290` | `srv/hub_test.go:246` | B | `hubSubscriberCount(t, h)` | .1.3.3 |
| `srv/hub_test.go:324` | `srv/hub_test.go:280` | B (latent) | `hubSubscriberCount(t, h)` | .1.3.3 |
| `srv/hub_test.go:383` | `srv/hub_test.go:339` | B | `hubSubscriberCount(t, h)` | .1.3.3 |
| `srv/hub_test.go:417` | `srv/hub_test.go:373` | B (latent) | `hubSubscriberCount(t, h)` | .1.3.3 |
| `srv/ws_test.go:578` | `srv/ws_test.go:556` | B (latent-only) | `hubSubscriberCount(t, s.Hub)` | .1.3.3 |

Shape notes. **B** is the audit's Shape B: a bound whose check sits after the blocking call, so a wedged hub never lets control reach it (`wsWaitForSubscribers`, the churn watcher's stop check). **A** is the audit's Shape A: an unbounded wait on a goroutine that is itself parked in the blocking call (the success-path `<-watchDone`). The seven rows labelled plain **B** were not a bound placed wrongly, they had no bound at all — control could not reach *any* check, which is B taken to its limit; the audit's A/B taxonomy only covers sites where a bound existed, so the audit classified them BOUNDED-OK rather than B, and that is the classification this sweep corrects.

**Latent rows.** The two rows marked latent behave as the verified-facts table says: `srv/hub_test.go:280` (`324` after .1.3.3) runs after `Hub.Close`, so `SubscriberCount` returns 0 through the `Hub.Done` branch instead of blocking; `srv/hub_test.go:373` (`417`) runs after the slow subscriber was dropped, so the hub is healthy and answers. Neither hung before the sweep. In the whole-suite wedge run both of their tests still fail, but each fails earlier — at `hub_test.go:204` and `hub_test.go:383`, the pre-condition assertions — so those two latent lines are never reached under the wedge at all. The same is true of `srv/ws_test.go:578` (Section 7), which is latent-only and genuinely cannot block today.

Nothing in `srv/hub.go` or `srv/ws.go` changed. The implementation was correct in every commit measured; only the tests' waits changed.

**One discrepancy in the task's sites table.** The verified-facts table lists the post-churn assertion as `srv/hub_test.go:586`, shape **B**, subtask .1.3.2. The diff `bb9e138..45617ca` makes .1.3.2's own edit `srv/hub_test.go:570` -> `hub_test.go:583` -> (after .1.3.3's insertions above it) `hub_test.go:586`, and that line is a plain one-shot assertion, not a loop with a deadline checked after the call. It is attributed to .1.3.2 in this table and labelled plain rather than Shape B. Every other row of that table matches the diff. No code defect; a labeling difference only.

## 3. Class-level result: the whole suite under the count wedge

```bash
git worktree add /tmp/wedge-count 45617ca
# apply the count-only wedge from Section 1 to /tmp/wedge-count/srv/hub.go
cd /tmp/wedge-count && timeout 300 go test ./srv/... -race -count=1 -timeout 90s 2>&1 \
  | tee /tmp/wedge-count-full.log | tail -40
grep -c 'panic: test timed out' /tmp/wedge-count-full.log
```

Result: **`grep -c` prints `0`**. Wall time 21.57s (exit 1); `go test` reports `FAIL srv.exe.dev/srv 20.767s`. A verbose repeat of the same wedge (`-v`, `-timeout 90s`) gives `PASS=54 FAIL=10 SKIP=0`, wall 21.15s, `go test` 20.8s, still zero timeout panics. The same run at the tighter `-timeout 30s` also completes clean: 10 `--- FAIL:`, zero panics, wall 21.11s. Every test that notices the wedge reports it as a failure naming the wedge:

```
--- FAIL: TestHubBroadcastReachesEverySubscriberWithIdenticalBytes (2.00s)
    hub_test.go:204: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the subscriber count cannot be read at all
--- FAIL: TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce (2.00s)
    hub_test.go:253: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the subscriber count cannot be read at all
--- FAIL: TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine (2.00s)
    hub_test.go:290: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the subscriber count cannot be read at all
--- FAIL: TestHubSlowSubscriberCannotBlockOthers (2.00s)
    hub_test.go:383: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the subscriber count cannot be read at all
--- FAIL: TestHubConcurrentSubscribeUnsubscribeBroadcast (2.02s)
    hub_test.go:486: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the watcher cannot tell how many subscribers are live
    hub_test.go:586: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the subscriber count cannot be read at all
--- FAIL: TestWSUpgradeSucceedsAndStaysOpen (2.02s)
    ws_test.go:265: hub did not answer SubscriberCount within 2s (last count read: 0, want 1 after 5s): the hub goroutine is wedged, so the subscriber count cannot be read at all
--- FAIL: TestWSBroadcastReachesTheListenerAsExactBytes (2.00s)
    ws_test.go:295: hub did not answer SubscriberCount within 2s (last count read: 0, want 1 after 5s): the hub goroutine is wedged, so the subscriber count cannot be read at all
--- FAIL: TestWSDisconnectUnsubscribesTheListener (2.00s)
    ws_test.go:326: hub did not answer SubscriberCount within 2s (last count read: 0, want 1 after 5s): the hub goroutine is wedged, so the subscriber count cannot be read at all
--- FAIL: TestWSHubCloseEndsTheListenerWithACleanClose (2.00s)
    ws_test.go:355: hub did not answer SubscriberCount within 2s (last count read: 0, want 1 after 5s): the hub goroutine is wedged, so the subscriber count cannot be read at all
--- FAIL: TestWSWedgedListenerCannotHangTeardown (2.00s)
    ws_test.go:436: hub did not answer SubscriberCount within 2s (last count read: 0, want 1 after 5s): the hub goroutine is wedged, so the subscriber count cannot be read at all
FAIL
FAIL	srv.exe.dev/srv	20.767s
FAIL
```

Ten failures, each at ~2.0s: the bound is `hubCountTimeout = 2s` in every case. Ten tests fail; the other 54 pass; nothing hangs; no goroutine dump is produced.

The same suite on the pre-fix tree (`330b34d`, the commit `01-baseline.md` measured) under the identical wedge yields one timeout panic and no `--- FAIL:` at all:

```
panic: test timed out after 2m30s
	running tests:
		TestHubBroadcastReachesEverySubscriberWithIdenticalBytes (2m30s)
	/tmp/wbase/srv/hub_test.go:160 +0x27f
FAIL	srv.exe.dev/srv	150.014s
```

(measured: exit 1, wall 150.78s at `-timeout 150s`; and 30.46s wall / one panic at `-timeout 30s`, again parked at `hub_test.go:160`.) On the audit's own tree (`81c80e5`) the same wedge at `-timeout 90s` gives exit 1, wall 90.42s, one panic naming `TestHubBroadcastReachesEverySubscriberWithIdenticalBytes` and the frame `/tmp/wedge-pre/srv/hub_test.go:160 +0x27f`. At `189f0be` (.1.3.2 done, .1.3.3 not) the wedge still panics at wall 90.40s, this time parked at `srv/hub_test.go:201` — the first of the seven bare assertions .1.3.3 then converted.

## 4. BEFORE / AFTER, per site

All runs below are mine, in worktrees, `-race`, `-count=1`, `-timeout 30s`, **one test per process** via `-run '^Name$'`. BEFORE = `/tmp/w30-pre` @ `81c80e5` + the count wedge of Section 1. AFTER = `/tmp/wedge-count` @ `45617ca` + the same wedge.

The one-test-per-process framing matters, and it is the reason the BEFORE column is a per-test figure while the class-level run in Section 3 is a single timeout: `go test` aborts the whole process on the first test to hit `-timeout`, so in a whole-suite run only the *first* parked test pays the timeout and the process dies before any other test can report. Pre-fix, the suite therefore told a reviewer nothing at all — one panic naming one test, no `--- FAIL:` anywhere (Section 3). Running each test in its own process is what exposes that all ten hang; a bound is what lets them all fail and name themselves in one run.

| Test | Site parked (BEFORE) | BEFORE wall | BEFORE panic | Site reported (AFTER) | AFTER wall | AFTER panic |
|---|---|---|---|---|---|---|
| `TestHubBroadcastReachesEverySubscriberWithIdenticalBytes` | `hub_test.go:160` | 30.76s | 1 | `hub_test.go:204` | 2.33s | 0 |
| `TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce` | `hub_test.go:209` | 30.33s | 1 | `hub_test.go:253` | 2.31s | 0 |
| `TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine` | `hub_test.go:246` | 30.33s | 1 | `hub_test.go:290` | 2.31s | 0 |
| `TestHubSlowSubscriberCannotBlockOthers` | `hub_test.go:339` | 30.33s | 1 | `hub_test.go:383` | 2.33s | 0 |
| `TestHubConcurrentSubscribeUnsubscribeBroadcast` | `hub_test.go:529` and `hub_test.go:432` | 30.31s | 1 | `hub_test.go:486`, `hub_test.go:586` | 2.36s | 0 |
| `TestWSUpgradeSucceedsAndStaysOpen` | `ws_test.go:170` | 30.33s | 1 | `ws_test.go:265` | 2.35s | 0 |
| `TestWSBroadcastReachesTheListenerAsExactBytes` | `ws_test.go:170` | 30.32s | 1 | `ws_test.go:295` | 2.38s | 0 |
| `TestWSDisconnectUnsubscribesTheListener` | `ws_test.go:170` | 30.33s | 1 | `ws_test.go:326` | 2.36s | 0 |
| `TestWSHubCloseEndsTheListenerWithACleanClose` | `ws_test.go:170` | 30.33s | 1 | `ws_test.go:355` | 2.36s | 0 |
| `TestWSWedgedListenerCannotHangTeardown` | `ws_test.go:170` | 30.33s | 1 | `ws_test.go:436` | 2.35s | 0 |

The BEFORE wall times are the `go test -timeout 30s` value: without a valid bound, the go-test timeout is the only thing that ends these tests, so at the suite's default configuration (~150s) each of the nine costs ~150s. AFTER, each is ended by `hubCountTimeout = 2s`.

### 4.1 `wsWaitForSubscribers` (`srv/ws_test.go:179`; was `:170`)

BEFORE (`/tmp/w30-pre` @ `81c80e5` + count wedge, `-run '^TestWSUpgradeSucceedsAndStaysOpen$'`, exit 1, wall 30.35s):

```
panic: test timed out after 30s
	running tests:
		TestWSUpgradeSucceedsAndStaysOpen (30s)
...
goroutine 8 [select]:
srv.exe.dev/srv.(*Hub).SubscriberCount(...)
	/tmp/w30-pre/srv/hub.go:238
srv.exe.dev/srv.wsWaitForSubscribers(0xc00020c488, 0xc0000282d0, 0x1)
	/tmp/w30-pre/srv/ws_test.go:170 +0x24f
srv.exe.dev/srv.TestWSUpgradeSucceedsAndStaysOpen(0xc00020c488)
	/tmp/w30-pre/srv/ws_test.go:248 +0x176
```

AFTER (`/tmp/wedge-count` @ `45617ca` + count wedge, exit 1, wall 2.35s, zero panics):

```
--- FAIL: TestWSUpgradeSucceedsAndStaysOpen (2.04s)
    ws_test.go:265: hub did not answer SubscriberCount within 2s (last count read: 0, want 1 after 5s): the hub goroutine is wedged, so the subscriber count cannot be read at all
FAIL
FAIL	srv.exe.dev/srv	2.049s
FAIL
```

The five ws tests that call `wsWaitForSubscribers` while the hub is wedged all move the same way (rows 6-10 above). The helper has nine callers in all (`grep -n 'wsWaitForSubscribers(t' srv/ws_test.go`): five expect `want == 1` (lines 265, 295, 326, 355, 436, all shown above) and four expect `want == 0` (lines 338, 343, 396, 477), the latter asking the hub to *empty*. Under the wedge the `want == 1` call is where each of those tests parks, so they fail before reaching the `want == 0` call; either way every line goes through the same bounded read, so no caller of the helper can hang, whatever `want` is.

### 4.2 The churn watcher (`srv/hub_test.go:484`; was `:432`)

BEFORE (`/tmp/w30-pre` @ `81c80e5` + count wedge, `-run '^TestHubConcurrentSubscribeUnsubscribeBroadcast$' -timeout 45s`, exit 1, wall 45.78s):

```
panic: test timed out after 45s
	running tests:
		TestHubConcurrentSubscribeUnsubscribeBroadcast (45s)
...
goroutine 21 [select]:
srv.exe.dev/srv.(*Hub).SubscriberCount(...)
	/tmp/w30-pre/srv/hub.go:238
srv.exe.dev/srv.TestHubConcurrentSubscribeUnsubscribeBroadcast(0xc000164488)
	/tmp/w30-pre/srv/hub_test.go:529 +0xbff
...
goroutine 23 [select]:
srv.exe.dev/srv.(*Hub).SubscriberCount(...)
	/tmp/w30-pre/srv/hub.go:238
srv.exe.dev/srv.TestHubConcurrentSubscribeUnsubscribeBroadcast.func1()
	/tmp/w30-pre/srv/hub_test.go:432 +0x2a9
created by srv.exe.dev/srv.TestHubConcurrentSubscribeUnsubscribeBroadcast in goroutine 21
	/tmp/w30-pre/srv/hub_test.go:424 +0x2ab
```

Two goroutines parked: the main test in the post-churn assertion (`:529`) and the watcher (`:432`). `close(stop)` could not reach either.

AFTER (`/tmp/wedge-count` @ `45617ca` + count wedge, exit 1, wall 2.36s, zero panics):

```
--- FAIL: TestHubConcurrentSubscribeUnsubscribeBroadcast (2.03s)
    hub_test.go:486: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the watcher cannot tell how many subscribers are live
    hub_test.go:586: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the subscriber count cannot be read at all
FAIL
FAIL	srv.exe.dev/srv	2.036s
FAIL
```

The intermediate state is worth recording because it shows which commit did which half. At `189f0be` (.1.3.2 done, .1.3.3 not) the same test under the same wedge fails in 2.03s with `hub_test.go:483` (watcher) and `hub_test.go:583` (post-churn assertion) — the watcher row is already fixed there while the bare assertion still needed .1.3.3.

### 4.3 `hub_test.go:160` (`srv/hub_test.go:204` after .1.3.3)

BEFORE (`/tmp/w30-pre` @ `81c80e5` + count wedge, `-run '^TestHubBroadcastReachesEverySubscriberWithIdenticalBytes$'`, exit 1, wall 30.76s):

```
panic: test timed out after 30s
	running tests:
		TestHubBroadcastReachesEverySubscriberWithIdenticalBytes (30s)
...
goroutine 8 [select]:
srv.exe.dev/srv.(*Hub).SubscriberCount(...)
	/tmp/w30-pre/srv/hub.go:238
srv.exe.dev/srv.TestHubBroadcastReachesEverySubscriberWithIdenticalBytes(0xc00018c488)
	/tmp/w30-pre/srv/hub_test.go:160 +0x27f
```

AFTER (exit 1, wall 2.33s, zero panics):

```
--- FAIL: TestHubBroadcastReachesEverySubscriberWithIdenticalBytes (2.00s)
    hub_test.go:204: SubscriberCount did not answer within 2s: the hub goroutine is wedged (it is not answering the count command), so the subscriber count cannot be read at all
FAIL
FAIL	srv.exe.dev/srv	2.009s
FAIL
```

This is the site the audit classified BOUNDED-OK with the note "After subscribes complete; hub cannot be wedged yet" — a claim about one mutation, not a structural property, and false under the count wedge.

## 5. Correction to the audit: mutation 30 does not prove the count-path hangs

`docs/mutation-bench/07-unbounded-audit.md` states that its two Shape-B sites are "**PROVEN** via mutation 30 (`hub-slow-client-blocks`)" and quotes a goroutine dump for `hub_test.go:432` parked in `SubscriberCount`. Mutation 30 replaces the non-blocking send in `deliver` with `sub.ch <- msg` (`srv/hub.go:330` at `45617ca`), which wedges the hub only when a subscriber's buffer is full and a broadcast runs. No ws test drives the hub into that state.

Measured, in the clean tree at both `81c80e5` and `45617ca`:

```bash
cd /home/exedev/strudel-agent
MUTATION_TEST_ARGS="-run TestWS" GO_TEST_TIMEOUT=45s MUTATION_TIMEOUT=150 \
  timeout 250 ./scripts/mutation-check.sh 30-hub-slow-client-blocks 2>&1 | tail -12
```

```
mutation-check: baseline (unmutated) run [go test ./... -run TestWS]
mutation-check: baseline green

30-hub-slow-client-blocks              SURVIVED  <-- the tests did not notice this defect

mutation-check: 0 caught, 1 survived, 0 weak, 0 broken anchor(s)
  NOT CAUGHT (the suite is vacuous for these):
    - 30-hub-slow-client-blocks
mutation-check: FAILED
```

Wall time 5.91s. A direct confirmation on a worktree with mutation 30 hand-applied (`/tmp/w30post` @ `45617ca`) running the ws tests alone:

```
--- PASS: TestWSUpgradeSucceedsAndStaysOpen (0.29s)
--- PASS: TestWSBroadcastReachesTheListenerAsExactBytes (0.00s)
--- PASS: TestWSDisconnectUnsubscribesTheListener (0.00s)
--- PASS: TestWSHubCloseEndsTheListenerWithACleanClose (0.02s)
--- PASS: TestWSWedgedListenerCannotHangTeardown (1.03s)
...
ok  	srv.exe.dev/srv	2.411s
```

exit 0, wall 2.94s, zero panics: every ws test passes under mutation 30.

**Correction.** Mutation 30 is the wrong RED tool for the count-path sites. The Site-B claims in the audit's own evidence block were produced by a *different* wedge than the mutation named there; the count path needs the count-only wedge in Section 1 (the hub answers subscribe/unsubscribe/broadcast/close, and never answers count). Under that wedge the sites hang exactly as the audit describes, with frames pinned to `srv/hub.go:238` and the test files (Section 4). The audit's *site inventory* is sound — .1.3.3 confirmed four of its seven BOUNDED-OK rows hang (rows 1-4 of the BEFORE table above), with row 5 (the churn test) and the two ws rows hanging by the routes the audit called Shape A/B. Only the evidence linking those sites to mutation 30 is wrong.

A second, smaller correction: the goroutine frame quoted in the audit is `srv/hub.go:87`. At `81c80e5` line 87 is a blank line inside the `Subscriber` type declaration; the blocking receive has been at `srv/hub.go:238` in every revision of the file (`4aad387` onward). The line number in the quoted dump does not correspond to any commit in this repository.

## 6. Non-vacuity: mutations 28-36

```bash
timeout 900 ./scripts/mutation-check.sh 28-hub-double-close-allowed \
  29-hub-unsubscribe-does-not-close 30-hub-slow-client-blocks \
  31-hub-broadcast-skips-a-subscriber 32-hub-count-escapes-the-hub-goroutine \
  33-hub-dropped-client-not-removed 34-hub-removal-not-recorded \
  35-ws-listener-never-subscribes 36-ws-subscriber-leaked-on-exit
```

Exit 0, wall 109.61s, on `45617ca`:

```
28-hub-double-close-allowed                  panic: close of closed channel
      FAIL	srv.exe.dev/srv	0.069s
caught
29-hub-unsubscribe-does-not-close            --- FAIL: TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce (2.00s)
          hub_test.go:252: subscriber channel is still open after 2s, want it closed (last message "")
caught
30-hub-slow-client-blocks                    --- FAIL: TestHubSlowSubscriberCannotBlockOthers (15.01s)
          hub_test.go:401: Broadcast 3 blocked for 10s: a non-reading subscriber must be dropped, never waited for
          hub_test.go:73: Hub.Close did not return within 5s: the hub goroutine is wedged
caught
31-hub-broadcast-skips-a-subscriber          --- FAIL: TestHubBroadcastReachesEverySubscriberWithIdenticalBytes (0.00s)
          hub_test.go:224: subscriber 0 message 4 = "{\"kind\":\"code\",\"version\":6}", want "{\"kind\":\"code\",\"version\":5}"
caught
32-hub-count-escapes-the-hub-goroutine       --- FAIL: TestHubConcurrentSubscribeUnsubscribeBroadcast (0.07s)
          testing.go:1865: race detected during execution of test
caught
33-hub-dropped-client-not-removed            --- FAIL: TestHubSlowSubscriberCannotBlockOthers (2.00s)
          hub_test.go:413: subscriber channel is still open after 2s, want it closed (last message "")
caught
34-hub-removal-not-recorded                  panic: close of closed channel
      FAIL	srv.exe.dev/srv	0.067s
caught
35-ws-listener-never-subscribes              --- FAIL: TestWSUpgradeSucceedsAndStaysOpen (5.01s)
          ws_test.go:265: hub holds 0 subscribers after 5s, want 1
caught
36-ws-subscriber-leaked-on-exit              --- FAIL: TestWSDisconnectUnsubscribesTheListener (5.00s)
          ws_test.go:338: hub holds 1 subscribers after 5s, want 0
caught

mutation-check: 9 caught, 0 survived, 0 weak, 0 broken anchor(s)
mutation-check: PASSED — every mutation was caught; the suite is non-vacuous.
```

The same nine mutations also pass on the two pre-fix trees, so nothing below was vacuous before the sweep either: `330b34d` 9 caught / wall 232.47s, `81c80e5` 9 caught / wall 124.46s. The wall time of that nine-mutation set falls 232.47s (`330b34d`) -> 124.46s (`81c80e5`) -> 109.61s (`45617ca`): the first drop is `1690ff0` (.1.1, which bounded the churn test's watchdog) plus the audit commit, the second is this sweep's conversion of the seven bare count assertions into 2s-bounded reads.

### 6.1 Mutation 32 is caught by `-race`, not by a count assertion

Mutation 32 replaces `reply <- len(subs)` with `go func() { reply <- len(subs) }()`, i.e. the count is read from a goroutine that escapes the hub's single owner. It is caught by `-race` and only by `-race`:

```
32-hub-count-escapes-the-hub-goroutine       --- FAIL: TestHubConcurrentSubscribeUnsubscribeBroadcast (0.07s)
          testing.go:1865: race detected during execution of test
```

With `-race` off, the whole `srv` suite PASSES under mutation 32, both before and after the sweep:

```
/tmp/w32-pre @ 189f0be + mutation 32, go test ./srv/ -count=1 -timeout 120s:
  exit=0  wall=2.32s   ok  	srv.exe.dev/srv	1.887s    (0 fails, 0 panics)
/tmp/w32     @ 45617ca + mutation 32, go test ./srv/ -count=1 -timeout 120s:
  exit=0  wall=2.29s   ok  	srv.exe.dev/srv	1.869s    (0 fails, 0 panics)
```

The catch is a DATA RACE report in `TestHubConcurrentSubscribeUnsubscribeBroadcast`, 3 warnings in both trees (`grep -c 'WARNING: DATA RACE'` = 3), from the racing `map` read that follows the escape:

```
WARNING: DATA RACE
Read at 0x00c000120000 by goroutine 26:
  srv.exe.dev/srv.(*Hub).run.func1()
      /tmp/w32/srv/hub.go:281 +0x38

Previous write at 0x00c000120000 by goroutine 10:
  runtime.mapdelete_fast64()
  srv.exe.dev/srv.removeSubscriber()
      /tmp/w32/srv/hub.go:316 +0x390
  srv.exe.dev/srv.(*Hub).run()
      /tmp/w32/srv/hub.go:278 +0x30e
...
--- FAIL: TestHubConcurrentSubscribeUnsubscribeBroadcast (0.07s)
FAIL	srv.exe.dev/srv	0.076s
```

No count assertion appears in the FAIL set. The bounded helper does not mask the mutation: `hubSubscriberCountWithin` runs `h.SubscriberCount()` on a goroutine, and that goroutine performs exactly the channel round-trip `SubscriberCount` already performed, so the escape it exposes is the same one the unwrapped call exposed. Mutation 32's verdict is identical before (`189f0be`) and after (`45617ca`) the sweep: caught, by the race detector. It must not be cited as a count-assertion catch.

### 6.2 `make verify`

```
==> gofmt -l .
gofmt: clean
==> go vet ./...
==> go build ./...
==> go test ./... -race -count=1
?   	srv.exe.dev/cmd/srv	[no test files]
ok  	srv.exe.dev/srv	2.902s
==> verify: all checks passed
```

Wall 3.54s, exit 0, at `45617ca` with the clean tree.

## 7. Sites deliberately left alone

| Site | Reason |
|---|---|
| `srv/hub_test.go:118` `go func() { got <- h.SubscriberCount() }()` | The helper's own goroutine. It is the deliberate, documented leak: a blocked channel send cannot be cancelled, and the test that triggered the bound is about to fail and exit. |
| `srv/hub.go`, `srv/ws.go` (all of both files) | Production code is correct. `SubscriberCount` is the documented reply-channel pattern, `deliver` has no blocking send, and `Close` is bounded. The defect was in how tests waited, so no implementation change was made or is needed. |
| `srv/hub_test.go:73` (hub `Close` in `hubTestHub` cleanup), `srv/ws_test.go:132` (hub `Close` in `wsTestServerWith` cleanup), `srv/ws_test.go:157` (`ts.Close`) | Already bounded by a goroutine plus `select`/`time.After`, from `4aad387` (.3.3), `fca1b3d` (.3.4.2) and `772ca71` (.3.2) respectively. They fail naming the wedge rather than hanging, and they still do under the count wedge: the whole-suite run produces no panic and no extra failure from them. |
| `srv/api_test.go`, `srv/conductor_test.go`, `srv/conductor_eval_test.go` | No `SubscriberCount` call. Their listener-count assertions go through `Conductor.SetListenerCount`/`Snapshot`, an in-process field write, not a hub round-trip. |
| `srv/integration_test.go` | No `SubscriberCount` call (grep count 0). All 15 of its tests pass under the count wedge. |
| `srv/ws_test.go:578` (`TestWSConnectingAfterHubCloseIsClosedGoingAway`) | Converted by .1.3.3 even though it is latent-only: it runs after the hub is closed, so `SubscriberCount` returns 0 through `Hub.Done` and cannot block. Re-measured: passes under the count wedge, `--- PASS: TestWSConnectingAfterHubCloseIsClosedGoingAway (0.05s)`, wall 1.36s, zero panics. The conversion keeps every count call in the suite on one guarded path. |
| `scripts/mutation-check.sh` | Out of scope. Per-mutation `-run` routing is `strudel-agent-e2v.2.1`. No file in `scripts/` was touched by this subtask. |

## 8. Wall time saved

The arithmetic uses `docs/mutation-bench/01-baseline.md` (whole 41-mutation grid 312.680s; `30-hub-slow-client-blocks` 153.587s raw / 149.981s baseline-subtracted, 49.1% of the grid) and the numbers measured for this report. The whole grid was **not** re-run (that is `strudel-agent-e2v.3.2`).

**(a) The timeout-bound mutation.** Same per-mutation invocation as the baseline (`./scripts/mutation-check.sh 30-hub-slow-client-blocks`, whole suite, `MUTATION_TIMEOUT=180` and `GO_TEST_TIMEOUT=150s` defaults, plus the script's own ~3-6s green baseline pass; the only difference is my outer `timeout 250` vs the harness's 300, immaterial because the run finishes well inside both):

| | Wall time |
|---|---|
| `01-baseline.md`, same machine and command | 153.587s |
| measured at `45617ca`, 5 runs | 35.49s, 27.48s, 35.48s, 35.53s, 27.60s (mean 32.32s) |

Saving per invocation: `153.587 - 27.48 = 126.11s` at the fast end, `153.587 - 35.53 = 118.06s` at the slow end, `153.587 - 32.32 = 121.27s` on the mean. That is 77-82% of the cost of the single slowest grid member. The residual ~27-35s is not the hang: it is the test's own wedge failure path in `TestHubSlowSubscriberCannotBlockOthers` (15.01s when the 10s broadcast watchdog fires first, 7.01s when the 2s receive bound fires first) plus the `Hub.Close` bound, plus the script's baseline pass. The two message shapes were both observed and both name the wedge:

```
30-hub-slow-client-blocks  --- FAIL: TestHubSlowSubscriberCannotBlockOthers (15.01s)
         hub_test.go:401: Broadcast 3 blocked for 10s: a non-reading subscriber must be dropped, never waited for
         hub_test.go:73: Hub.Close did not return within 5s: the hub goroutine is wedged

the same mutation on another run:

30-hub-slow-client-blocks  --- FAIL: TestHubSlowSubscriberCannotBlockOthers (7.00s)
         hub_test.go:405: no message within 2s, want "{\"kind\":\"code\",\"version\":2}"
         hub_test.go:73: Hub.Close did not return within 5s: the hub goroutine is wedged
```

both `caught`; the bimodality is scheduler-dependent which bound fires first, not a difference in outcome. Neither shape is a timeout panic. For contrast, the same mutation hand-applied to the pre-fix tree (`330b34d`) and run as `go test ./srv/... -race -count=1 -timeout 150s` took 150.35s and produced `panic: test timed out after 2m30s` naming `TestHubConcurrentSubscribeUnsubscribeBroadcast` — the `GO_TEST_TIMEOUT` cost the grid's 153.587s figure is made of.

**(b) Projection for the whole grid.** Mutation 30 still runs the whole suite in the grid (no `-run` routing yet), so substituting the measured cost into the baseline grid:

```
312.680 - 153.587 + 27.48 = 186.57s
312.680 - 153.587 + 35.53 = 194.62s
```

Projected whole-grid wall time ~186.6-194.6s, i.e. ~118-126s (38-40%) below the 312.680s baseline. This is arithmetic on `01-baseline.md` plus the runs in (a), not a re-baseline.

**(c) The class-level run.** Whole suite under the count wedge, same `-timeout 90s`:

| Tree | Wall | `panic: test timed out` | `--- FAIL:` |
|---|---|---|---|
| `81c80e5` (post-audit, pre-fix) | 90.42s | 1 | 0 |
| `189f0be` (.1.3.2 only) | 90.40s | 1 | 0 |
| `330b34d` (baseline tree) | 150.78s at `-timeout 150s`; 30.46s at `-timeout 30s` | 1 | 0 |
| `45617ca` (fixed) | 21.57s | 0 | 10 |

`90.42 - 21.57 = 68.85s` saved on that run, and the result changes from "one timeout panic, no failure message" to "ten failures, each naming the wedge".

**(d) Per-test.** The ten hang-capable tests cost the full go-test timeout each before the fix (30.33s median / 30.37s mean at `-timeout 30s`; ~150s each at the suite default) and are now each ended by the 2s bound. Per the per-test table in Section 4, over the same ten-test set: BEFORE sum 303.70s, AFTER sum 23.44s (`303.70 - 23.44 = 280.26s`); BEFORE median 30.33s vs AFTER median 2.35s, a factor of 12.9. The whole suite (64 tests) now finishes in 20.8s of test time.

**(e) The churn test under mutation 30.** Restricted to `-run '^TestHubConcurrentSubscribeUnsubscribeBroadcast$'`: `81c80e5` 25.02s test time / 26.02s wall, `45617ca` 15.01s / 15.32s. The watcher now exits at the 2s count bound instead of burning the extra `hubWatchdogTimeout`; both messages are still present at `81c80e5` (`hub_test.go:521` watcher, `:522` churn) and at `45617ca` (`hub_test.go:486` watcher, `:573` churn).

## 9. Invariant: hangs got faster, nothing stopped failing

**Statement.** Every test that failed before the fixes still fails, with a message naming what wedged; every fix only made a hang faster and never turned a hang into a pass. Nothing was deleted or weakened.

Evidence:

1. **Same failing set, now with messages.** Under the count wedge, the pre-fix tree produces zero `--- FAIL:` lines and one timeout panic; the fixed tree produces ten `--- FAIL:` lines, one per test that used to hang. The tests are the same tests — the sweep added no failure and removed none: baseline (unmutated) is `PASS=64 FAIL=0`; under the wedge it is `PASS=54 FAIL=10`; the ten are exactly the ten rows of the Section 3 table.
2. **The assertions themselves are unchanged.** The diff `81c80e5..45617ca` touches only the *read* of the count, not the comparison: `if got := h.SubscriberCount(); got != N` became `if got := hubSubscriberCount(t, h); got != N` with the same `N` and the same message. The watcher still updates `maxCount` by the same CAS loop, and the `maxCount == 0` check ("the watcher proved nothing") is untouched.
3. **The mutations those tests exist for are still caught.** Section 6: 28-36, nine mutations, 9 caught / 0 survived / 0 weak / 0 broken, at `45617ca` and (identically) at `330b34d` and `81c80e5`.
4. **Mutation 30's message naming the wedge survives in the churn test** (`hub_test.go:486` names the wedged hub, `hub_test.go:573` names the wedge in the churn) as it did at `81c80e5` (`hub_test.go:521`, `:522`).
5. **`make verify` green** (`gofmt` clean, `go vet`, `go build`, `go test -race -count=1`).

## 10. Provenance of the numbers

| Number | Source |
|---|---|
| 312.680s grid, 153.587s mutation 30, 3.606s baseline pass | `docs/mutation-bench/01-baseline.md`, machine-identical |
| Whole-suite count-wedge runs, per-test BEFORE/AFTER, churn-under-30, mutation-32 `-race` off/on, nine-mutation set, mutation-30 `-run TestWS` SURVIVED | re-run for this report in throwaway worktrees; logs quoted inline |
| `make verify` final line | re-run at `45617ca` |
| Per-site BEFORE line numbers (`hub_test.go:160/209/246/280/339/373/432/529`, `ws_test.go:170/174/556`) | `git show 81c80e5:srv/*_test.go`, verified by grep |

The fixes' own BEFORE/AFTER numbers (in the commit messages of `bb9e138`, `189f0be`, `45617ca`) were not collated: every BEFORE and AFTER figure in Section 4 was measured here in this session. The worktrees used (`/tmp/wedge-count`, `/tmp/wedge-pre`, `/tmp/w30*`, `/tmp/w32*`, `/tmp/wbase`, `/tmp/w189`) were created with `git worktree add` and removed with `git worktree remove --force`; `git worktree list` afterwards shows only the main tree, and `git status --porcelain` was empty at each measurement.
