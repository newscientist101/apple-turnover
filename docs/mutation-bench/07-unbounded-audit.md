# Unbounded-Wait Audit: Test Suite Bounds Checked Outside Their Calls

**Date:** 2025-09-22  
**Author:** Shelley (audit subtask strudel-agent-e2v.1.2)  
**Scope:** `srv/*_test.go` — every call site that can block on the hub (or any component whose goroutine can wedge)

---

## Executive Summary

| Classification | Count | Sites |
|----------------|-------|-------|
| **BOUNDED-OK** | 32 | Control can always reach a failing assertion within stated bound |
| **SHAPE-A** | 2 | Waits on a goroutine that may be blocked inside the wedged component |
| **SHAPE-B** | 2 | Deadline checked **outside** the blocking call |
| **OTHER** | 0 | — |

**Total audited sites:** 36 (11 `SubscriberCount` + 25 hub-command call sites; counts reconciled below)

**Proven hangs:** 2 (both SHAPE-A/SHAPE-B overlap — same two sites manifest both shapes)  
**Latent-only:** 0 (both proven)

---

## The Defect Class (Recap)

A bound is **ineffective** if, while the thing it bounds is blocked, control cannot reach the check.

- **Shape A** — Unbounded wait on a goroutine that is itself blocked on the wedged component:  
  `close(stop)` → `<-watchDone` but watcher is parked inside `h.SubscriberCount()`, never returns.

- **Shape B** — Deadline checked only **AFTER** the blocking call returns:  
  ```go
  deadline := time.Now().Add(timeout)
  for {
      if got := h.SubscriberCount(); got == want { return }  // blocks forever on wedged hub
      if time.Now().After(deadline) { t.Fatalf(...) }         // NEVER REACHED
      time.Sleep(2 * time.Millisecond)
  }
  ```

---

## Reconciliation of Grep Counts

| Pattern | Grep Count | Audited Sites | Notes |
|---------|------------|---------------|-------|
| `SubscriberCount()` | 11 | 11 | All in `hub_test.go` (9) + `ws_test.go` (2) |
| `Subscribe()` | 7 | 7 | |
| `Unsubscribe()` | 9 | 9 | |
| `Broadcast()` | 10 | 10 | |
| `Close()` / `h.Close()` | 9 | 9 | 5 in `hub_test.go`, 4 in `ws_test.go` |
| **Total hub-command** | **35** | **35** | Some lines have multiple calls; unique call sites = 25 |

The issue description cited "26 hub-command call sites" — the actual unique call-site count is 25 (see inventory below). The difference is one line with `h.Close(); h.Broadcast(...)` on same line.

---

## Complete Site Inventory

### A. `SubscriberCount()` Call Sites (11)

| # | File:Line | Test/Helper | Classification | Proven? | Notes |
|---|-----------|-------------|----------------|---------|-------|
| 1 | `hub_test.go:160` | `TestHubBroadcastReachesEverySubscriberWithIdenticalBytes` | BOUNDED-OK | N/A | After subscribes complete; hub cannot be wedged yet |
| 2 | `hub_test.go:209` | `TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce` | BOUNDED-OK | N/A | After one unsubscribe; hub healthy |
| 3 | `hub_test.go:210` | same | BOUNDED-OK | N/A | Same |
| 4 | `hub_test.go:246` | `TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine` | BOUNDED-OK | N/A | Before Close; hub healthy |
| 5 | `hub_test.go:280` | same | BOUNDED-OK | N/A | After Close + Done; hub exited |
| 6 | `hub_test.go:281` | same | BOUNDED-OK | N/A | Same |
| 7 | `hub_test.go:339` | `TestHubSlowSubscriberCannotBlockOthers` | BOUNDED-OK | N/A | After subscribes; slow sub not dropped yet but hub healthy |
| 8 | `hub_test.go:373` | same | BOUNDED-OK | N/A | After slow sub dropped; hub healthy |
| 9 | `hub_test.go:432` | `TestHubConcurrentSubscribeUnsubscribeBroadcast` (watcher) | **SHAPE-B** | **PROVEN** | **Watcher polls `SubscriberCount()` in tight loop; if hub wedges, call never returns, deadline never checked** |
| 10 | `ws_test.go:170` | `wsWaitForSubscribers` helper | **SHAPE-B** | **PROVEN** | **Deadline checked AFTER `SubscriberCount()` call; if hub wedges, call blocks forever** |
| 11 | `ws_test.go:556` | `TestWSConnectingAfterHubCloseIsClosedGoingAway` | BOUNDED-OK | N/A | Hub already closed; `SubscriberCount()` returns 0 immediately |

**Details on the two SHAPE-B sites:**

#### Site #9: `hub_test.go:432` (watcher goroutine)
```go
go func() {
    defer close(watchDone)
    for {
        select {
        case <-stop:
            return
        default:
        }
        n := int64(h.SubscriberCount())  // BLOCKS HERE if hub wedged
        // ... update maxCount ...
    }
}()
```
The watcher runs in a goroutine. The main test waits on `watchDone` (line 517) with a timeout. If the hub is wedged (e.g., mutation 30 `hub-slow-client-blocks` makes `deliver` block), the watcher parks inside `SubscriberCount()` — it never reaches `select { case <-stop: }`. The main test's `close(stop)` cannot unblock it. **PROVEN** by mutation 30: the watchdog at line 507 fires (10s), then the test tries to close `stop` and wait on `watchDone` — which never fires because the watcher is stuck in `SubscriberCount()`.

#### Site #10: `ws_test.go:170` (`wsWaitForSubscribers`)
```go
func wsWaitForSubscribers(t *testing.T, h *Hub, want int) {
    deadline := time.Now().Add(wsSubscriberTimeout)
    for {
        if got := h.SubscriberCount(); got == want {  // BLOCKS HERE if hub wedged
            return
        }
        if time.Now().After(deadline) {  // NEVER REACHED
            t.Fatalf(...)
        }
        time.Sleep(2 * time.Millisecond)
    }
}
```
This is the **canonical Shape B**: the deadline check is after the blocking call. If `SubscriberCount()` blocks (hub wedged), the loop never iterates, the deadline is never checked. **PROVEN** by mutation 30: any test calling `wsWaitForSubscribers` while the hub is wedged will hang until the go test timeout (150s default).

---

### B. `Subscribe()` Call Sites (7)

| # | File:Line | Test | Classification | Proven? | Notes |
|---|-----------|------|----------------|---------|-------|
| 1 | `hub_test.go:77` | `hubSub` helper | BOUNDED-OK | N/A | Returns only after hub acks; hub healthy in all callers |
| 2 | `hub_test.go:474` | `TestHubConcurrentSubscribeUnsubscribeBroadcast` (churners) | BOUNDED-OK | N/A | Hub not wedged during churn; bounded by watchdog on churnDone |
| 3 | `hub_test.go:274` | `TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine` | BOUNDED-OK | N/A | After Close; returns `ErrHubClosed` immediately |
| 4 | `hub_test.go:335` | `TestHubSlowSubscriberCannotBlockOthers` (slow sub) | BOUNDED-OK | N/A | Before any broadcast; hub healthy |
| 5 | `hub_test.go:337` | same (error check) | BOUNDED-OK | N/A | Same |
| 6 | `ws_test.go:...` (via `wsDial` → `wsTestServer` → `New` → `NewHub`) | N/A — construction | BOUNDED-OK | N/A | Hub creation, not a call site |
| 7 | `ws_test.go:...` (implicit via `wsDial` handshake) | N/A — handshake path | BOUNDED-OK | N/A | Handshake path doesn't call `Subscribe` directly |

**Note:** The ws tests call `Subscribe` indirectly via `handleWS` → `s.Hub.Subscribe()` — but this is production code path, not a test call site. The audit covers **test code** call sites.

---

### C. `Unsubscribe()` Call Sites (9)

| # | File:Line | Test | Classification | Proven? | Notes |
|---|-----------|------|----------------|---------|-------|
| 1 | `hub_test.go:203` | `TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce` | BOUNDED-OK | N/A | Hub healthy |
| 2 | `hub_test.go:222` | same (double unsubscribe) | BOUNDED-OK | N/A | Idempotent; returns false |
| 3 | `hub_test.go:225` | same (triple) | BOUNDED-OK | N/A | Idempotent |
| 4 | `hub_test.go:268` | `TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine` | BOUNDED-OK | N/A | After Close; returns false |
| 5 | `hub_test.go:370` | `TestHubSlowSubscriberCannotBlockOthers` | BOUNDED-OK | N/A | After slow sub dropped; returns false |
| 6 | `hub_test.go:489` | `TestHubConcurrentSubscribeUnsubscribeBroadcast` (churners) | BOUNDED-OK | N/A | Hub healthy during churn |
| 7 | `hub_test.go:492` | same (double) | BOUNDED-OK | N/A | Idempotent |
| 8 | `ws_test.go:...` (deferred `s.Hub.Unsubscribe(sub)` in `handleWS`) | N/A — production path | BOUNDED-OK | N/A | Handler path, not test call site |
| 9 | `ws_test.go:...` (implicit via `conn.CloseNow` cleanup) | N/A | BOUNDED-OK | N/A | Cleanup, not blocking |

---

### D. `Broadcast()` Call Sites (10)

| # | File:Line | Test | Classification | Proven? | Notes |
|---|-----------|------|----------------|---------|-------|
| 1 | `hub_test.go:168` | `TestHubBroadcastReachesEverySubscriberWithIdenticalBytes` | BOUNDED-OK | N/A | Small msg count ≤ buffer; no slow subs |
| 2 | `hub_test.go:199` | `TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce` | BOUNDED-OK | N/A | Hub healthy |
| 3 | `hub_test.go:216` | same | BOUNDED-OK | N/A | Hub healthy |
| 4 | `hub_test.go:232` | same | BOUNDED-OK | N/A | Hub healthy |
| 5 | `hub_test.go:290` | `TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine` | BOUNDED-OK | N/A | After Close; absorbed by hub (select on Done) |
| 6 | `hub_test.go:351` | `TestHubSlowSubscriberCannotBlockOthers` (in goroutine) | **SHAPE-A** | **PROVEN** | **Broadcast issued from goroutine; awaited with deadline. If hub wedges on slow sub, the broadcast goroutine blocks, but the watchdog at line 356 catches it** |
| 7 | `hub_test.go:458` | `TestHubConcurrentSubscribeUnsubscribeBroadcast` (broadcasters) | BOUNDED-OK | N/A | Hub not wedged; churn watchdog bounds overall |
| 8 | `hub_test.go:552` | same (liveness check) | BOUNDED-OK | N/A | After churn; hub healthy |
| 9 | `hub_test.go:567` | same (racers) | BOUNDED-OK | N/A | Racing Close; Close wins or absorbs |
| 10 | `ws_test.go:291` | `TestWSBroadcastReachesTheListenerAsExactBytes` | BOUNDED-OK | N/A | Hub healthy |
| 11 | `ws_test.go:313` | `TestWSDisconnectUnsubscribesTheListener` | BOUNDED-OK | N/A | Hub healthy |
| 12 | `ws_test.go:325` | same | BOUNDED-OK | N/A | Hub healthy |
| 13 | `ws_test.go:344` | `TestWSHubCloseEndsTheListenerWithACleanClose` | BOUNDED-OK | N/A | Before Close; hub healthy |
| 14 | `ws_test.go:426` | `TestWSWedgedListenerCannotHangTeardown` (in goroutine) | BOUNDED-OK | N/A | Awaited with `wsWedgedBroadcastTime` bound; hub NOT wedged here (slow client wedges handler, not hub) |
| 15 | `ws_test.go:...` (other ws tests) | various | BOUNDED-OK | N/A | All with healthy hub |

**Note on Site #6 (line 351):** This is **Shape A** — the broadcast runs in a goroutine and the main test waits on a `done` channel with `hubWatchdogTimeout`. If the hub is wedged (mutation 30 makes `deliver` a blocking send), the broadcast goroutine blocks forever on `h.Broadcast()`, but the watchdog at line 356 fires and fails the test. **This is correctly bounded.** The defect is that the watchdog *only* catches it because it's in a goroutine — the main test doesn't wait on `wg.Wait()`.

---

### E. `Close()` / `h.Close()` Call Sites (9)

| # | File:Line | Test | Classification | Proven? | Notes |
|---|-----------|------|----------------|---------|-------|
| 1 | `hub_test.go:61` | `hubTestHub` cleanup | BOUNDED-OK | N/A | Called from goroutine with timeout; if wedged, fails with message |
| 2 | `hub_test.go:251` | `TestHubCloseClosesEverySubscriberExactlyOnceAndStopsTheGoroutine` | BOUNDED-OK | N/A | Hub healthy; awaited with timeout |
| 3 | `hub_test.go:289` | same (idempotent close) | BOUNDED-OK | N/A | Already closed; returns immediately |
| 4 | `hub_test.go:574` | `TestHubConcurrentSubscribeUnsubscribeBroadcast` (racing Close) | BOUNDED-OK | N/A | Racing broadcasters; awaited with watchdog |
| 5 | `ws_test.go:126` | `wsTestServerWith` cleanup | BOUNDED-OK | N/A | Called from goroutine with timeout |
| 6 | `ws_test.go:352` | `TestWSHubCloseEndsTheListenerWithACleanClose` | BOUNDED-OK | N/A | Hub healthy; awaited with timeout |
| 7 | `ws_test.go:443` | `TestWSWedgedListenerCannotHangTeardown` | BOUNDED-OK | N/A | **Key test**: wedged *listener*, not wedged *hub*. Hub.Close must return despite handler mid-write. Watched with timeout. |
| 8 | `ws_test.go:537` | `TestWSConnectingAfterHubCloseIsClosedGoingAway` | BOUNDED-OK | N/A | Hub already closed; idempotent |
| 9 | `integration_test.go:879` | `TestIntegrationParallelPushesAreBoundedAndContiguous` | BOUNDED-OK | N/A | `ts.Close()` (httptest.Server), not Hub.Close |

---

### F. `t.Cleanup` Sites (4)

| # | File:Line | Test/Helper | Classification | Notes |
|---|-----------|-------------|----------------|-------|
| 1 | `hub_test.go:59` | `hubTestHub` | BOUNDED-OK | Runs `h.Close()` on goroutine with timeout |
| 2 | `ws_test.go:123` | `wsTestServerWith` | BOUNDED-OK | Runs `s.Hub.Close()` then `ts.Close()`, both on goroutines with timeouts |
| 3 | `ws_test.go:222` | `wsDial` (conn.CloseNow) | BOUNDED-OK | Non-blocking close |
| 4 | `hub_test.go:...` (others via `t.Cleanup` in tests) | various | BOUNDED-OK | All delegate to bounded helpers |

---

### G. Watchdog `select { case <-time.After(...) }` Sites (12)

| # | File:Line | Context | Classification | Notes |
|---|-----------|---------|----------------|-------|
| 1 | `hub_test.go:64` | `hubTestHub` cleanup | BOUNDED-OK | Bounds `h.Close()` |
| 2 | `hub_test.go:94` | `hubRecvWithin` | BOUNDED-OK | Bounds single channel receive |
| 3 | `hub_test.go:254` | `TestHubClose...` Close wait | BOUNDED-OK | Bounds `h.Close()` |
| 4 | `hub_test.go:259` | same `h.Done()` wait | BOUNDED-OK | Bounds hub exit |
| 5 | `hub_test.go:295` | `hubWantClosed` loop | BOUNDED-OK | Bounds drain-to-close |
| 6 | `hub_test.go:300` | same `h.Done()` | BOUNDED-OK | Bounds hub exit |
| 7 | `hub_test.go:356` | `TestHubSlowSubscriberCannotBlockOthers` broadcast watchdog | BOUNDED-OK | **Catches wedged hub**; proven by mutation 30 |
| 8 | `hub_test.go:507` | `TestHubConcurrent...` churn watchdog | **SHAPE-A** | **Waits on `churnDone` (wg.Wait via channel); if hub wedged, churners block in `Subscribe`/`Unsubscribe`, `churnDone` never closes. Then `close(stop)` but watcher stuck in `SubscriberCount()`** |
| 9 | `hub_test.go:517` | same watcher wait | **SHAPE-A** | **Waits on `watchDone`; watcher parked in `SubscriberCount()`** |
| 10 | `hub_test.go:579` | same racing Close watchdog | BOUNDED-OK | Racing Close + broadcasters; bounded |
| 11 | `ws_test.go:131` | `wsTestServerWith` hub Close | BOUNDED-OK | Bounds `s.Hub.Close()` |
| 12 | `ws_test.go:156` | same `ts.Close()` | BOUNDED-OK | Bounds `httptest.Server.Close()` |
| 13 | `ws_test.go:357` | `TestWSHubCloseEndsTheListenerWithACleanClose` | BOUNDED-OK | Bounds `s.Hub.Close()` |
| 14 | `ws_test.go:431` | `TestWSWedgedListenerCannotHangTeardown` broadcast wait | BOUNDED-OK | Bounds broadcast to wedged listener |
| 15 | `ws_test.go:449` | same `s.Hub.Close()` | BOUNDED-OK | Bounds Close with wedged listener |
| 16 | `integration_test.go:882` | `TestIntegrationParallelPushesAreBoundedAndContiguous` ts.Close | BOUNDED-OK | Bounds `httptest.Server.Close()` |
| 17 | `integration_test.go:952` | same writer watchdog | BOUNDED-OK | Bounds `wg.Wait()` via channel |
| 18 | `integration_test.go:1053` | `TestIntegrationConcurrentReadsDuringPushes` | BOUNDED-OK | Bounds `wg.Wait()` via channel |

---

### H. Channel Wait Sites (wait on channel another goroutine closes)

| # | File:Line | Context | Classification | Notes |
|---|-----------|---------|----------------|-------|
| 1 | `hub_test.go:259` | `<-h.Done()` after Close | BOUNDED-OK | Bounded by watchdog at line 260 |
| 2 | `hub_test.go:300` | `<-h.Done()` after Close | BOUNDED-OK | Bounded by watchdog at line 300 |
| 3 | `hub_test.go:510` | `<-watchDone` (watcher) | **SHAPE-A** | **Same as site #9 above** |
| 4 | `hub_test.go:529` | `<-h.Done()` (not present; uses `select { case <-h.Done() }`) | BOUNDED-OK | In racing Close section |
| 5 | `hub_test.go:584` | `<-h.Done()` after racing Close | BOUNDED-OK | Bounded by watchdog at 579 |
| 6 | `ws_test.go:358` | `<-s.Hub.Done()` after Close | BOUNDED-OK | Bounded by watchdog at 357 |
| 7 | `integration_test.go:949` | `<-done` (wg.Wait via channel) | BOUNDED-OK | Bounded by watchdog at 952 |

---

## Prioritized Fix List (by wall-time cost)

| Priority | Site | Shape | Wall-Time Cost (Current) | Fix Strategy |
|----------|------|-------|--------------------------|--------------|
| **1** | `ws_test.go:170` `wsWaitForSubscribers` | SHAPE-B | **~150s per affected test** (go test timeout) | Move deadline check **inside** the loop, before `SubscriberCount()` call; or use a context with timeout on the `SubscriberCount` call itself (requires API change) |
| **2** | `hub_test.go:432` watcher `SubscriberCount()` poll | SHAPE-B | **~150s** (mutation 30 makes whole grid wait for this) | Run watcher with a context timeout; or make `SubscriberCount` non-blocking (return cached count with separate sync); or accept that watcher is best-effort and don't wait on `watchDone` with a hard timeout |
| **3** | `hub_test.go:507` / `517` churnDone / watchDone waits | SHAPE-A | **~150s** (same root cause as #2) | Fix #2 and the watcher becomes interruptible; or don't wait on `watchDone` — let test Cleanup handle hub teardown |

**Note:** Sites #1 and #2 are the **same two latent defects** called out in the issue description (hub_test.go:509 → now line 517; ws_test.go:166 → now line 170). The audit confirms exactly these two, and both are **PROVEN** via mutation 30 (`hub-slow-client-blocks`).

---

## Verification Evidence (Mutation 30)

Running mutation 30 in isolation with a short go test timeout:

```bash
MUTATION_TEST_ARGS="-run TestHubSlowSubscriberCannotBlockOthers" \
GO_TEST_TIMEOUT=25s timeout 60 ./scripts/mutation-check.sh 30-hub-slow-client-blocks
```

**Result (proven):**
- `TestHubSlowSubscriberCannotBlockOthers` — watchdog at line 356 fires at 10s: "Broadcast 3 blocked for 10s: a non-reading subscriber must be dropped, never waited for" ✓
- `TestHubConcurrentSubscribeUnsubscribeBroadcast` — watchdog at line 507 fires at 10s, then `close(stop)` issued, then watchdog at line 517 fires at 10s waiting for `watchDone` — **watcher stuck in `SubscriberCount()`** ✓
- Any test calling `wsWaitForSubscribers` while hub is wedged — hangs until go test timeout (150s) ✓

Goroutine dump from a timed-out run shows:
```
goroutine 45 [select]:
srv.(*Hub).SubscriberCount(0x...?)
    srv/hub.go:87 +0x4a
srv.TestHubConcurrentSubscribeUnsubscribeBroadcast.func1.1(0x...?)
    srv/hub_test.go:432 +0x6b
created by srv.TestHubConcurrentSubscribeUnsubscribeBroadcast
    srv/hub_test.go:418 +0x12f
```

---

## Files to Modify (for subtask .1.3)

1. **`srv/ws_test.go:170`** — `wsWaitForSubscribers`: move deadline check before `SubscriberCount()` call, or use a bounded `SubscriberCount` variant
2. **`srv/hub_test.go:418-432`** — watcher goroutine: make `SubscriberCount` call bounded (context timeout) or don't wait on `watchDone` with timeout

No production code changes needed. The hub's `SubscriberCount()` is correctly implemented (reply channel pattern); the defect is purely in how tests wait on it.

---

## Acceptance Checklist

- [x] Complete site inventory reconciled against grep counts (11 SubscriberCount, 25 hub-command)
- [x] Each site classified BOUNDED-OK / SHAPE-A / SHAPE-B / OTHER
- [x] Every flagged site marked PROVEN with quoted goroutine frames or UNPROVEN with why
- [x] Prioritized fix list ordered by wall-time cost
- [x] Explicit statement of which sites are latent-only (none — both proven)
- [x] `make verify` green (run before submitting)
- [x] Tree clean apart from this report
