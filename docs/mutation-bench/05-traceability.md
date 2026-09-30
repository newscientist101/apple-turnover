# Traceability: the 41 curated mutations against gomutants' generated set

**Issue:** strudel-agent-kki.5 (epic strudel-agent-kki)
**Date:** 2026-09-30
**Repo:** `/home/exedev/strudel-agent`. The anchors were located, and every line number in
this document read, at `19ea479`; `19ea479..8a908d7` (current `main`) touches only
`AGENTS.md`, `Makefile`, `README.md` and adds `scripts/docs-split-check.sh` — **zero** changes
under `srv/`, `cmd/` or `scripts/mutation-check.sh` — so the anchors and rows below hold
unchanged at `8a908d7`. The incumbent grid itself last changed at `5a47cf5`. Nothing in the
repo was modified by this audit except this document and the index row in
`docs/mutation-bench/README.md`.
**Tool under test:** `gomutants` **v0.6.1** (`gomutants v0.6.1 (commit: none, built: unknown)`),
binary `/home/exedev/go/bin/gomutants`, 28 mutator types
(`/home/exedev/kki5-data/list-mutators.txt`). **Go:** go1.27.1 linux/amd64.
**Incumbent:** `scripts/mutation-check.sh` at `19ea479` — 41 hand-written mutations.
**Inputs:** kki.3's measured sweep (`report-B.json`, and the 101 per-mutant
`conductor.go` runs) and kki.4's hang semantics (`04-timeout-semantics.md`). No new
mutation pass of any kind was run for this document.

---

## 0. Conclusion

**18 of the 41 curated mutations (44 %) have a gomutants mutant at the same file and line
expressing the same defect; all 18 are KILLED. The other 23 (56 %) are NOT-GENERATED — and
for 15 of those, the only mutants on the line express a *different* defect.**

| Class | Rows | Meaning here |
|---|---|---|
| `GENERATED-AND-KILLED` | **18** | an equivalent mutant exists at the anchor and a test killed it |
| `GENERATED-AND-LIVED` | **0** | — |
| `NOT-GENERATED` | **23** | no equivalent mutant exists |
| &nbsp;&nbsp;· of which *no mutant at all on any anchor line* | 8 | gomutants emitted nothing on those lines |
| &nbsp;&nbsp;· of which *only a related mutant on the line* | 15 | the mutant there breaks a different claim |

So the adoption premise — "gomutants auto-generates what we hand-crafted" — holds for the
**deletion and guard classes** of the curated grid (statement removal, guard-body
neutralisation, operator flips, boundary flips) and does not hold for any **substitution**
class the grid uses: HTTP status codes, websocket close codes and opcodes, route patterns,
callees, boolean literals, loop bookkeeping, struct-field values, message bytes,
constructors with different arguments, and a deliberate wedge. Those 23 rows carry no
equivalent in gomutants' 368-mutant discovery.

Two further facts that matter for `.6` and are not recommendations (see §7):

* **Four anchor lines carry LIVED mutants** (14, 25, 27, 39 — lines `srv/server.go:84`,
  `srv/ws.go:179` twice, `srv/ws.go:189`): gomutants reports a surviving mutant on a line
  the curated grid already covers with a caught mutation. All four LIVED mutants are on the
  error-log branch (`!=` → `==`, or the `slog.Debug` body emptied), which no test asserts.
* **Breadth runs the other way too:** 300 of the 357 judged mutants (84 %) sit on lines the
  41 anchors never touch, 66 of them LIVED (§6).

---

## 1. Method, and the rule this audit applies

The question is *expressibility*, not *coverage*: does gomutants' generated set contain a
mutant that states the same defect at the same place?

**Rule applied to every row** (stated so a reviewer can disagree with a specific row rather
than with the whole table):

1. Locate the curated row's `old` text in its file, using the script's own `\n`/`\t`
   unescape rules, and require **exactly one occurrence**. All 41 anchors matched exactly
   once (a multi-match would have been reported, not hidden).
2. Take every gomutants mutant whose `(file, line)` is **on a line the anchor spans**
   (multi-line anchors such as 31 and 33 span 2–6 lines; a first-line-only lookup would
   have missed row 33's equivalent, which sits one line below the anchor start).
3. Classify as **`GENERATED`** only when the mutant edits *the construct the curated row
   edits*, or neutralises precisely the guard/statement whose sole purpose is the curated
   row's claim — i.e. a reviewer would call the two the same defect. If the nearest mutant
   shares the line but breaks a *related* claim (for instance it omits a response where the
   curated row misreports its status), the row is **`NOT-GENERATED`**, and the nearest
   mutant is recorded so the distinction is visible.
4. Verdicts come from kki.3's data: `report-B.json` for `api.go`/`hub.go`/`server.go`/
   `ws.go`, and `conductor-permutant.tsv` for `conductor.go`. kki.4's finding is used to
   classify the hang rows (23, 30): a `TIMED OUT` verdict is excluded from gomutants'
   efficacy denominator, so it could not gate either defect even if it fired.

The `basis` column in §3 is the strength of each trace:

| basis | meaning |
|---|---|
| `exact` | gomutants emits the identical edit (same tokens), or the operator/statement-level equivalent |
| `equivalent` | different edit, same guard/expression/statement, same resulting defect |
| `related-coverage` | a mutant exists on the line but expresses a **different** defect |
| `none` | no gomutants mutant exists on any line the anchor spans |

---

## 2. Provenance, and reconciliation to kki.3/kki.4

kki.3 wrote its artifacts to `/tmp/kki3`, which `03-gomutants.md` §10 warns is not durable.
They were still present when this audit ran; because they are the only evidence for these
rows, they were **copied to a persistent path** before any analysis:

```bash
mkdir -p /home/exedev/kki5-data
cp -p /tmp/kki3/{report-B.json,cache-B.json,list-mutators.txt,dry-srv.txt,dry-all.txt,report-srv.json,conductor-permutant.tsv,version.txt} /home/exedev/kki5-data/
cp -rp /tmp/kki3/mut /home/exedev/kki5-data/      # 101 per-mutant conductor.go reports
```

No artifact was written into the repo. If `/home/exedev/kki5-data` is ever lost, every
number here is regenerable by `03-gomutants.md` §11 (`gomutants -w 1 --exclude-files
"conductor\.go$" … ./srv/...` plus the per-mutant `conductor.go` harness); this document
adds no new commands and no new measurement.

### 2.1 Derived counts (all computed from the artifacts, not copied from the prose)

| Fact | Value | Source |
|---|---|---|
| curated mutations | 41 | `scripts/mutation-check.sh --list`; 41 rows in the `MUTATIONS` table |
| mutator types enabled | 28 | `list-mutators.txt` |
| mutants discovered by `--dry-run ./srv/...` | **360** = 343 to-test + 17 not covered | `dry-srv.txt:11` |
| mutants discovered by `--dry-run ./...` | 368 = 343 to-test + 25 not covered | `dry-all.txt:11` |
| the 343 to-test records in both dry runs | identical modulo the `srv/` path prefix | `diff` of the two `[PENDING]` sets, zero differences after prefix normalisation |
| judged mutants in the kki.3 data | **357** = 256 (`report-B.json`) + 101 (`conductor-permutant.tsv`) | both artifacts |
| verdicts over those 357 | KILLED 242, LIVED 72, NOT VIABLE 28, NOT COVERED 14, INFRA ERROR 1 | both artifacts |
| not covered, total | **17** = 14 in `report-B.json` (7 hub, 6 server, 1 ws) + 3 `conductor.go` never handed to the per-mutant harness | `[NOT COVERED]` records in `dry-srv.txt` |
| efficacy / coverage | 242/(242+72) = **77.07 %** · (242+72)/(242+72+17) = **94.86 %** | same arithmetic as `03-gomutants.md` §6 |

---

## 3. The 41 rows

`anchor` is `file:first-line-of-the-anchor` at `19ea479`. Mutant ids are
`package:function:TYPE#n` from `report-B.json`/`conductor-permutant.tsv`. `KILLED`/
`LIVED`/`NOT VIABLE` are gomutants' verdicts.

| # | curated mutation | anchor | curated defect (old → new) | class | basis | evidence — equivalent mutant, or nearest mutant + why it does not express the row |
|---|---|---|---|---|---|---|
| 01 | `01-empty-code-accepted` | api.go:133 | `strings.TrimSpace(req.Code) == ""` → `false` | GENERATED-AND-KILLED | equivalent | `…handleAPICode:CONDITIONALS_NEGATION#2` (`==` → `!=`, KILLED) — same condition with the operator flipped, so an empty code passes the guard exactly as `false` makes it. (Same line: `…BRANCH_IF#2`, guard body → `{ _ = 0 }`, KILLED.) |
| 02 | `02-unknown-json-field-allowed` | api.go:286 | `dec.DisallowUnknownFields()` → `_ = dec` | GENERATED-AND-KILLED | exact | `srv/api.go:decodeBody:STATEMENT_REMOVE#1` (`dec.DisallowUnknownFields()` → `_ = 0`, KILLED) |
| 03 | `03-trailing-json-allowed` | api.go:291 | `return errTrailingJSON` → `return nil` | GENERATED-AND-KILLED | exact | `…decodeBody:RETURN_ERROR_NIL#3` (`errTrailingJSON` → `nil`, KILLED) |
| 04 | `04-payload-cap-removed` | api.go:88 | `http.MaxBytesReader(w, r.Body, APIMaxBodyBytes)` → `…int64(1)<<40` | GENERATED-AND-KILLED | equivalent | `…limitAPIRequestBody:STATEMENT_REMOVE#1` (`r.Body = http.MaxBytesReader(…)` → `_ = http.MaxBytesReader(…)`, KILLED) — the cap is not enforced at all rather than raised to 1 TiB; the same claim ("a body over the cap is rejected") is broken. |
| 05 | `05-cap-reported-as-400` | api.go:301 | `http.StatusRequestEntityTooLarge` → `http.StatusBadRequest` | NOT-GENERATED | related-coverage | nearest: `…writeDecodeError:STATEMENT_REMOVE#1` (KILLED). **Named-constant substitution (HTTP status):** no mutator rewrites one identifier constant to another, and the line contains no literal for `INTEGER_*` to move. The surviving mutant drops the whole response — nothing is written, rather than a 400 being written — so the curated claim is not expressed. |
| 06 | `06-allow-header-dropped` | api.go:98 | `w.Header().Set("Allow", want)` → `_ = want` | GENERATED-AND-KILLED | exact | `…methodNotAllowed:STATEMENT_REMOVE#1` (`w.Header().Set("Allow", want)` → `_ = 0`, KILLED) |
| 07 | `07-api-404-is-500` | api.go:106 | `http.StatusNotFound` → `http.StatusInternalServerError` | NOT-GENERATED | related-coverage | nearest: `…handleAPINotFound:STATEMENT_REMOVE#1` (KILLED). **Named-constant substitution (HTTP status)**, as row 05: the mutant omits the response instead of misreporting its code, so "a missing endpoint answers 500 instead of 404" is not expressible. |
| 08 | `08-api-catchall-too-broad` | api.go:76 | route pattern `"/api"` → `"/"` | NOT-GENERATED | related-coverage | nearest: `…(*Server).routes:STATEMENT_REMOVE#17` (KILLED). **String literal / route pattern:** gomutants has no mutator that edits a string literal (the 28 types touch numbers, booleans, operators, statements and calls). Removing the route entirely means a non-API path is still *not* served by the JSON 404, so the curated claim is untouched. |
| 09 | `09-static-mount-removed` | api.go:45 | `mux.Handle("/static/", http.StripPrefix(…))` → `_ = s.StaticDir` | GENERATED-AND-KILLED | exact | `…(*Server).routes:STATEMENT_REMOVE#2` (`mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.StaticDir))))` → `_ = 0`, KILLED) |
| 10 | `10-eval-ack-lies` | api.go:196 | `apiEvalAck{Accepted: true, …}` → `Accepted: false` | NOT-GENERATED | related-coverage | nearest: `…handleAPIEvalResult:STATEMENT_REMOVE#3` (KILLED). **Boolean literal in a struct field:** `RETURN_TRUE`/`RETURN_FALSE` rewrite boolean *return* expressions and `EXPRESSION_REMOVE` rewrites `&&` operands; nothing flips a struct-literal field. The mutant omits the ack rather than lying in it. |
| 11 | `11-message-bumps-version` | api.go:163 | `s.Conductor.SetMessage(req.Message)` → `s.Conductor.Publish(req.Message, req.Message)` | NOT-GENERATED | related-coverage | nearest: `…handleAPIMessage:STATEMENT_REMOVE#3` (KILLED). **Semantic call swap:** no mutator renames or swaps a callee. Dropping the call suppresses the message write; it does not make a message write advance the version. |
| 12 | `12-transport-accepts-any-body` | api.go:226 | `if err := rejectUnexpectedBody(r); err != nil {` → `if err := error(nil); false {` | GENERATED-AND-KILLED | equivalent | `…handleAPITransport:BRANCH_IF#1` (guard body `{ writeDecodeError(w, err); return }` → `{ _ = 0 }`, KILLED) — the guard fires and does nothing, so the unexpected body is accepted, the same effect as `false`. (Same line: `…CONDITIONALS_NEGATION#1`, KILLED.) |
| 13 | `13-hush-plays-instead` | api.go:212 | `s.handleAPITransport(w, r, false)` → `…(w, r, true)` | NOT-GENERATED | related-coverage | nearest: `…handleAPIHush:STATEMENT_REMOVE#1` (KILLED). **Boolean literal argument:** no mutator rewrites a call argument's literal. The mutant drops the call, which does not make hush resume playback. |
| 14 | `14-shell-render-silently-truncates` | server.go:84 | `s.renderTemplate(w, "welcome.html", data)` → `…, struct{ Hostname string }{data.Hostname})` | NOT-GENERATED | related-coverage | nearest: `…HandleRoot:BRANCH_IF#1` (**LIVED**), plus `CONDITIONALS_NEGATION#1` (**LIVED**). **Argument substitution** (a truncated data struct): no mutator rewrites an argument's type or value. Both mutants on this line LIVED — gomutants reports a gap on a line the curated grid covers, but not in the render-data behaviour. |
| 15 | `15-eval-accepts-future-version` | conductor.go:176 | `res.Version > c.version` → `res.Version > c.version+1` | GENERATED-AND-KILLED | equivalent | `…RecordEvalResult:EXPRESSION_REMOVE#2` (`res.Version > c.version` → `false`, KILLED) — the future-version bound is removed entirely instead of relaxed by one, so a version newer than the latest is accepted, the same claim. (`CONDITIONALS_BOUNDARY#2`/`#NEGATION#2` also sit on this line, KILLED.) |
| 16 | `16-eval-result-not-stored` | conductor.go:189 | `c.lastEval = &stored` → `_ = stored` | GENERATED-AND-KILLED | exact | `…RecordEvalResult:STATEMENT_REMOVE#2` (`c.lastEval = &stored` → `_ = &stored`, KILLED) |
| 17 | `17-stale-report-regresses` | conductor.go:179 | `if <stale guard> {` → `if false {` | GENERATED-AND-KILLED | equivalent | `…RecordEvalResult:BRANCH_IF#2` (`{ return nil }` → `{ _ = 0 }`, KILLED) — the "ignore the older report" action is removed, so the stale report is stored, the same effect as `if false`. (The same line also carries `EXPRESSION_REMOVE#3`/`#4` and `INVERT_LOGICAL#2`, KILLED, which break the other direction.) |
| 18 | `18-same-version-report-ignored` | conductor.go:179 | `res.Version < c.lastEval.Version` → `res.Version <= c.lastEval.Version` | GENERATED-AND-KILLED | exact | `…RecordEvalResult:CONDITIONALS_BOUNDARY#3` (`<` → `<=` on the same comparison, KILLED) — literally the curated edit |
| 19 | `19-version-not-monotonic` | conductor.go:109 | `c.version++` → `c.version += 0` | GENERATED-AND-KILLED | equivalent | `…Publish:STATEMENT_REMOVE#2` (`c.version++` → `_ = c.version`, KILLED) — a no-op in place of `+= 0`, so a publish never advances the version. (`INCREMENT_DECREMENT#1`, `++` → `--`, also KILLED.) |
| 20 | `20-version-skipped` | conductor.go:109 | `c.version++` → `c.version += 2` | NOT-GENERATED | related-coverage | nearest: `…Publish:STATEMENT_REMOVE#2` (KILLED). **Arithmetic-literal change:** the source line is `c.version++`, so there is no integer literal for `INTEGER_*` to move, and `INVERT_ASSIGNMENTS`/`REMOVE_SELF_ASSIGNMENTS` need a compound assignment that is not there. The available edits are `++` → no-op or `++` → `--`; *skipping a value* is not among them. |
| 21 | `21-history-window-wrong-start` | conductor.go:205 | `start := c.histNext - c.histLen` → `start := 0` | GENERATED-AND-KILLED | equivalent | `…snapshotLocked:ARITHMETIC_BASE#1` (`-` → `+` on the same expression, KILLED; `INVERT_NEGATIVES#1` is the same edit, also KILLED). Same expression, different wrong offset — a rotated window rather than the oldest entries — and the same claim ("the window is the most recent `histLen` versions") breaks. Sharp edge: `+` is *equivalent* while the ring is exactly full (`2·histLen ≡ 0 mod len(history)`); it differs only mid-fill, which is the case the tests cover, and that is why it is killed rather than surviving. |
| 22 | `22-snapshot-omits-playing` | conductor.go:219 | `Playing: c.playing,` → `Playing: false,` | NOT-GENERATED | none | no gomutants mutant exists on conductor.go:219. **Struct-field value substitution** — `RETURN_TRUE`/`RETURN_FALSE`/`RETURN_ZERO` target return statements, not field values (this line is inside a `Snapshot{…}` literal). |
| 23 | `23-wedged-state-handler` | api.go:111 | insert `select {}` into the handler body | NOT-GENERATED | none | no gomutants mutant exists on api.go:111. **Statement insertion:** all 28 mutators replace or delete existing constructs; none inserts a wedge. kki.4's synthetic loop mutant came back `TIMED OUT` and is excluded from efficacy, so even an equivalent could not gate this row. |
| 24 | `24-ws-405-fallback-removed` | api.go:73 | `mux.HandleFunc("/ws", methodNotAllowed(http.MethodGet))` → `_ = methodNotAllowed(http.MethodGet)` | GENERATED-AND-KILLED | exact | `…(*Server).routes:STATEMENT_REMOVE#16` (KILLED) |
| 25 | `25-ws-wrong-close-status` | ws.go:179 | `websocket.StatusNormalClosure` → `websocket.StatusGoingAway` | NOT-GENERATED | related-coverage | nearest: `wsCloseListener:BRANCH_IF#1` (**LIVED**), `…CONDITIONALS_NEGATION#1` (**LIVED**). **Named-constant substitution (websocket close code, 1000 → 1001):** no mutator rewrites an identifier constant, and the line has no numeric literal. Both mutants on the line touch the error-log branch instead, and both LIVED. |
| 26 | `26-ws-origin-check-disabled` | ws.go:73 | `websocket.Accept(w, r, nil)` → `websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})` | NOT-GENERATED | none | no gomutants mutant exists on ws.go:73. **Argument substitution** (nil → an options struct): nothing rewrites a call argument into a different value or type. |
| 27 | `27-ws-closes-without-handshake` | ws.go:179 | `conn.Close(websocket.StatusNormalClosure, "")` → `conn.CloseNow()` | NOT-GENERATED | related-coverage | nearest: `…CONDITIONALS_NEGATION#1` (**LIVED**). **Call substitution** (`Close(status)` → `CloseNow()`): no mutator renames a callee. Both mutants on the line LIVED. |
| 28 | `28-hub-double-close-allowed` | hub.go:312 | `if _, present := subs[sub]; !present { return false }` → `if false { return false }` | GENERATED-AND-KILLED | equivalent | `removeSubscriber:BRANCH_IF#1` (`{ return false }` → `{ _ = 0 }`, KILLED) — the membership guard fires and does nothing, so execution falls through to `close(sub.ch)` for an absent subscriber: the double close the curated `if false` produces. (Same line: `REMOVE_LOGICAL_NOT#1` and `RETURN_TRUE#1`, both KILLED.) |
| 29 | `29-hub-unsubscribe-does-not-close` | hub.go:315 | drop `close(sub.ch)` | GENERATED-AND-KILLED | exact | `removeSubscriber:STATEMENT_REMOVE#1` (`close(sub.ch)` → `_ = 0`, KILLED) |
| 30 | `30-hub-slow-client-blocks` | hub.go:330–335 | non-blocking `select { case sub.ch <- msg: … default: … }` → `sub.ch <- msg; return true` | NOT-GENERATED | related-coverage | nearest: `(*Hub).deliver:RETURN_FALSE#1` (`true` → `false`, KILLED) and `RETURN_TRUE#1` (`false` → `true`, KILLED). **Non-blocking → blocking send** (the `select`/`default` removed) is a control-flow/synchronisation edit no mutator makes; the mutants that exist change the reported *verdict* (always drop, or never drop), so no wedged hub is reachable from them. kki.4 measured the equivalent synthetic loop as `TIMED OUT`, excluded from efficacy — so this row could not be gated even indirectly. |
| 31 | `31-hub-broadcast-skips-a-subscriber` | hub.go:283–285 | add `skipFirst` loop state that skips exactly one subscriber | NOT-GENERATED | related-coverage | nearest: `(*Hub).run:RANGE_BREAK#1` (inserts `break;` at the top of the `for sub := range subs` body, KILLED) and `REMOVE_LOGICAL_NOT#1` (`!h.deliver(sub, msg)` → `h.deliver(sub, msg)`, KILLED). **Loop-state insertion:** no mutator adds bookkeeping to a loop. Deliberately borderline: `RANGE_BREAK#1` also breaks "every subscriber receives the broadcast", but by ending the loop (skipping *all* of them) rather than skipping one, which is a different defect. |
| 32 | `32-hub-count-escapes-the-hub-goroutine` | hub.go:280–281 | `reply <- len(subs)` → `go func() { reply <- len(subs) }()` | NOT-GENERATED | none | no gomutants mutant exists on hub.go:280 or :281. **Concurrency edit** (the reply is no longer ordered on the owning hub goroutine): no mutator wraps a statement in a goroutine. |
| 33 | `33-hub-dropped-client-not-removed` | hub.go:288–289 | drop `removeSubscriber(subs, sub)` | GENERATED-AND-KILLED | exact | `(*Hub).run:STATEMENT_REMOVE#2` (`removeSubscriber(subs, sub)` → `_ = 0`, KILLED) — found only because the anchor's *second* line was searched; a first-line-only lookup sees `hub.go:288` (a comment) and reports "none". |
| 34 | `34-hub-removal-not-recorded` | hub.go:315–317 | drop `delete(subs, sub)` | GENERATED-AND-KILLED | exact | `removeSubscriber:STATEMENT_REMOVE#2` (`delete(subs, sub)` → `_ = 0`, KILLED) |
| 35 | `35-ws-listener-never-subscribes` | ws.go:91 | `sub, err := s.Hub.Subscribe()` → `sub, err := (*Subscriber)(nil), error(ErrHubClosed)` | NOT-GENERATED | none | no gomutants mutant exists on ws.go:91. **Happy-path replacement by an error return:** nothing replaces a successful call with a fabricated error (the nearest shapes, `RETURN_ERROR_NIL`/`RETURN_ZERO`, need a return statement, not a two-value assignment). |
| 36 | `36-ws-subscriber-leaked-on-exit` | ws.go:105 | drop `defer s.Hub.Unsubscribe(sub)` | NOT-GENERATED | none | no gomutants mutant exists on ws.go:105. **Dropped defer:** `STATEMENT_REMOVE` in this tool targets ordinary expression/assignment statements, not `defer` (and no other mutator does deletion). This is the one curated row whose shape *looks* like `STATEMENT_REMOVE` and still gets nothing. |
| 37 | `37-ws-broadcast-trimmed` | ws.go:169 | `msg` → `msg[:max(0, len(msg)-1)]` | NOT-GENERATED | related-coverage | nearest: `writeToListener:RETURN_ERROR_NIL#1` (`conn.Write(ctx, websocket.MessageText, msg)` → `nil`, **NOT VIABLE** — did not build). **Slice trim / message-byte edit:** no mutator edits the message payload or an argument expression. The only mutant on the line is unusable, which is why this row has no live trace at all. |
| 38 | `38-ws-write-deadline-removed` | ws.go:167 | `context.WithTimeout(context.Background(), s.wsWriteTimeout)` → `context.WithCancel(context.Background())` | NOT-GENERATED | none | no gomutants mutant exists on ws.go:167. **Call substitution** (`WithTimeout` → `WithCancel`): no mutator renames a callee, and dropping the statement would not remove the deadline the same way. |
| 39 | `39-ws-going-away-not-reported` | ws.go:189 | `websocket.StatusGoingAway` → `websocket.StatusNormalClosure` | NOT-GENERATED | related-coverage | nearest: `wsCloseGoingAway:BRANCH_IF#1` (**LIVED**), `…CONDITIONALS_NEGATION#1` (**LIVED**). **Named-constant substitution (websocket close code, 1001 → 1000)**, as row 25, with the same two LIVED mutants on the error-log branch. |
| 40 | `40-ws-client-close-not-noticed` | ws.go:115 | `clientGone := conn.CloseRead(context.Background())` → `clientGone := context.Background()` | NOT-GENERATED | none | no gomutants mutant exists on ws.go:115. **Call substitution / dropped call** (`CloseRead(ctx)` → a bare ctx): nothing rewrites the call away. |
| 41 | `41-ws-binary-frames` | ws.go:169 | `websocket.MessageText` → `websocket.MessageBinary` | NOT-GENERATED | related-coverage | nearest: `writeToListener:RETURN_ERROR_NIL#1` (**NOT VIABLE**). **Named-constant substitution (frame opcode):** no mutator rewrites an identifier constant, and the line has no numeric literal. As row 37, the line's only mutant did not build. |

These reproduce `03-gomutants.md` §6/§8 exactly (K=242, L=72, NC=17, efficacy 77.07 %,
coverage 94.86 %); the derivation above is recorded because it also shows *where* the 17
not-covered mutants are, which the earlier document reports only as a total.

**One correction to a documentation claim (not to any number used here).**
`03-gomutants.md` §10 describes `dry-srv.txt` and `dry-all.txt` as "identical 360 mutants".
They are not: `./srv/...` discovers 360 with 17 not covered, `./...` discovers **368** with
25 not covered. The 343 *testable* records are identical modulo the path prefix; the 8
extra not-covered mutants are all in `cmd/srv/main.go` (8 of 8), which `./srv/...` does not
scan at all. The number that matters for this audit — 343 testable mutants — is the same in
both.

---

## 4. Counts

| | api.go | server.go | conductor.go | hub.go | ws.go | total |
|---|---|---|---|---|---|---|
| curated mutations | 15 | 1 | 8 | 7 | 10 | **41** |
| `GENERATED-AND-KILLED` | 8 | 0 | 6 | 4 | 0 | **18** |
| `GENERATED-AND-LIVED` | 0 | 0 | 0 | 0 | 0 | **0** |
| `NOT-GENERATED` | 7 | 1 | 2 | 3 | 10 | **23** |

Basis of the 41 traces:

| basis | rows | which |
|---|---|---|
| `exact` | 10 | 02, 03, 06, 09, 16, 18, 24, 29, 33, 34 |
| `equivalent` | 8 | 01, 04, 12, 15, 17, 19, 21, 28 |
| `related-coverage` | 15 | 05, 07, 08, 10, 11, 13, 14, 20, 25, 27, 30, 31, 37, 39, 41 |
| `none` | 8 | 22, 23, 26, 32, 35, 36, 38, 40 |

Every `exact` and `equivalent` trace is a mutant that kki.3's data records as KILLED
(asserted programmatically before this document was written: the 18 evidence ids were
looked up in the judged set and each had to be present and `KILLED`).

Two things this table makes visible:

* **`ws.go` and `server.go` are the largest gaps.** All 10 `ws.go` rows and the single
  `server.go` row are NOT-GENERATED — and every `ws.go` row's curated defect is a
  substitution (close code, call, opcode, defer, message bytes).
* The 18 traceable rows are concentrated where the curated grid and gomutants overlap in
  *kind*: deleting a statement or neutralising a guard.

---

## 5. NOT-GENERATED, grouped by why

**23 rows.** Grouped by the class of edit gomutants cannot express, with the curated
`old → new` that shows why no AST/token transform reaches it. Quotes are abbreviated at `…`
only where the full line would add nothing; full anchors are in §3.

**A. Named-constant / enum substitution — 5 rows** (05, 07, 25, 39, 41)
`http.StatusRequestEntityTooLarge` → `http.StatusBadRequest`; `http.StatusNotFound` →
`http.StatusInternalServerError`; `websocket.StatusNormalClosure` → `websocket.StatusGoingAway`;
`websocket.StatusGoingAway` → `websocket.StatusNormalClosure`; `websocket.MessageText` →
`websocket.MessageBinary`. The 28 mutators include `INTEGER_INCREMENT`/`DECREMENT` (literals
±1), but none rewrites one *identifier* constant into another, and two of these five lines
contain no literal at all.

**B. String literal / route pattern — 1 row** (08)
`mux.HandleFunc("/api", handleAPINotFound)` → `mux.HandleFunc("/", handleAPINotFound)`.
No mutator edits a string literal.

**C. Boolean literal in a struct field or call argument — 2 rows** (10, 13)
`apiEvalAck{Accepted: true, …}` → `Accepted: false`; `s.handleAPITransport(w, r, false)` →
`…(w, r, true)`. `RETURN_TRUE`/`RETURN_FALSE` rewrite boolean *return* expressions and
`EXPRESSION_REMOVE` rewrites operands of `&&`/`||`; neither reaches a field value or an
argument.

**D. Call substitution — 4 rows** (11, 27, 38, 40)
`SetMessage(req.Message)` → `Publish(req.Message, req.Message)`; `conn.Close(status, "")` →
`conn.CloseNow()`; `context.WithTimeout(…)` → `context.WithCancel(…)`;
`conn.CloseRead(context.Background())` → `context.Background()`. No mutator renames, swaps
or unwraps a callee.

**E. Dropped defer — 1 row** (36)
`defer s.Hub.Unsubscribe(sub)` → `_ = sub // deliberately leaked`. `STATEMENT_REMOVE` is
the closest mutator by name, and it emits nothing here: the line's only statement is a
`defer`.

**F. Loop-state insertion — 1 row** (31)
`for sub := range subs {` → add `skipFirst` bookkeeping that skips exactly one iteration.
No mutator adds state to a loop (the loop mutators empty the condition, insert a `break`, or
flip `break`/`continue`).

**G. Concurrency re-ordering — 1 row** (32)
`reply <- len(subs)` → `go func() { reply <- len(subs) }()`. Nothing wraps a statement in a
goroutine, which is the entire defect.

**H. Non-blocking → blocking send — 1 row** (30)
`select { case sub.ch <- msg: … default: … }` → `sub.ch <- msg; return true`. The `default:`
branch is removed rather than edited; kki.4 established that the resulting hang is scored
`TIMED OUT` and excluded from efficacy even if a mutant could state it.

**I. Statement insertion (a wedge) — 1 row** (23)
`func (s *Server) handleAPIState(…) {` → insert `select {} // deliberately wedged`. Every
mutator replaces or deletes an existing construct; none inserts a statement — and kki.4
showed the hang class is not gateable by this tool anyway.

**J. Arithmetic-literal change — 1 row** (20)
`c.version++` → `c.version += 2`. The source has no literal and no compound assignment, so
only `++` → no-op/`--` is reachable; skipping a value is not among the generated edits.

**K. Struct-field value substitution — 1 row** (22)
`Playing: c.playing,` → `Playing: false,`. The value is a `Snapshot` literal field, not a
return expression, so the `RETURN_*` mutators do not apply.

**L. Argument substitution (non-boolean, non-constant) — 2 rows** (14, 26)
`s.renderTemplate(w, "welcome.html", data)` → `…, struct{ Hostname string }{data.Hostname})`;
`websocket.Accept(w, r, nil)` → `…, &websocket.AcceptOptions{InsecureSkipVerify: true})`.

**M. Message-byte edit / slice trim — 1 row** (37)
`return conn.Write(ctx, websocket.MessageText, msg)` → `msg[:max(0, len(msg)-1)]`.

**N. Happy-path replacement by an error return — 1 row** (35)
`sub, err := s.Hub.Subscribe()` → `sub, err := (*Subscriber)(nil), error(ErrHubClosed)`.

### 5.1 The four rows where a *related* mutant LIVED

Rows 14, 25, 27 and 39 have mutants on their anchor lines that gomutants did **not** kill,
while the curated grid catches a mutation on those same lines. In every case the LIVED
mutant is on the error-log branch:

| line | LIVED mutants there | curated row(s) on that line |
|---|---|---|
| `srv/server.go:84` | `…HandleRoot:CONDITIONALS_NEGATION#1` (`!=`→`==`), `…BRANCH_IF#1` (empty `slog.Warn` body) | 14 |
| `srv/ws.go:179` | `wsCloseListener:CONDITIONALS_NEGATION#1` (`!=`→`==`), `…BRANCH_IF#1` (empty `slog.Debug` body) | 25, 27 |
| `srv/ws.go:189` | `wsCloseGoingAway:CONDITIONALS_NEGATION#1` (`!=`→`==`), `…BRANCH_IF#1` (empty `slog.Debug` body) | 39 |

That is a reported gap in gomutants' *verdicts* on those lines (no test asserts the log
branch), and it is distinct from the expressibility question: even if those rows were
traceable, the trace would be `GENERATED-AND-LIVED`, not equivalent-and-killed.

---

## 6. Breadth gomutants adds (factual, not a weighed trade-off)

Of the 357 judged mutants, **57 (16 %) sit on a line one of the 41 anchors spans** and
**300 (84 %) do not.**

| | count |
|---|---|
| judged mutants off the anchor lines | **300** |
| … by file | api.go 94, hub.go 49, server.go 60, ws.go 18, conductor.go 79 |
| … by verdict | KILLED 193, LIVED 66, NOT VIABLE 26, NOT COVERED 14, INFRA ERROR 1 |
| … plus the 8 `cmd/srv/main.go` mutants discovered only by `./...` (all not covered) | 8 |

The 66 LIVED mutants off the anchor lines are the interesting half of that breadth, because
they are survivors no curated mutation is asking about. By mutator type:

| type | LIVED off-anchor |
|---|---|
| `INTEGER_INCREMENT` | 13 |
| `INTEGER_DECREMENT` | 12 |
| `BRANCH_IF` | 12 |
| `CONDITIONALS_NEGATION` | 7 |
| `EXPRESSION_REMOVE` | 5 |
| `CONDITIONALS_BOUNDARY` | 5 |
| `STATEMENT_REMOVE` | 5 |
| `ERRORF_WRAP` | 2 |
| `RANGE_BREAK`, `INVERT_LOGICAL`, `RETURN_FALSE`, `RETURN_TRUE`, `RETURN_ZERO` | 1 each |

Consistency check that the two halves partition the survivors: 66 off-anchor LIVED + 6 on
anchor-line LIVED (the three lines in §5.1, two each) = 72 = the LIVED total in §2.1.

Two honest readings of "breadth", so neither is over-claimed:

* It counts *lines gomutants mutates that the curated grid never anchors on*. That is
  broader than "behaviours the curated grid does not test": a curated test can and does
  cover those lines (the 193 KILLED off-anchor mutants are killed by the same suite), and a
  generated mutant can be `NOT VIABLE` or `NOT COVERED` rather than meaningful.
* It is also narrower than "coverage": the off-anchor mutants are the tool's arithmetic and
  control-flow edits, so much of the 300 is mechanical (50 of the 300 are literal ±1
  `INTEGER_*` edits; 84 are `STATEMENT_REMOVE`s). The five `ERRORF_WRAP` mutants (`%w` →
  `%v` — `srv/server.go:104`/`:107`, both not covered, two LIVED in `srv/api.go`, one
  KILLED in `srv/conductor.go`) are the part with no analogue in the curated grid at all.

---

## 7. Limitations, and what this does not answer

* **This is not the adoption decision.** It reports traceability. Whether a 44 % traceable
  grid is worth replacing is `.6`'s question; no `.gomutants.yml`, Makefile change, grid
  edit or script addition was made or is proposed here.
* **Equivalence is a judgement at 41 points.** §1 fixes the rule, and every row's argument
  is in §3/§5 so a reviewer can disagree with a row instead of the whole table. The rows
  where the equivalent mutant differs in *magnitude* or *direction* from the curated edit
  are the debatable ones: **04** (cap removed, not raised), **15** (bound removed, not
  relaxed by one), **17** (the ignore-action emptied rather than the condition falsified),
  **21** (`+` rather than `0`; identical while the ring is full). Classified here as
  traceable, giving **18/23**; read as strictly as possible those four become
  NOT-GENERATED, giving **14/27**. The count does not move outside that range under a
  consistent application of §1. Row 31 is flagged in place rather than counted either way.
* **The verdicts are kki.3's measurement, not a new one.** Cold, `-w 1`, cgroup-capped,
  **no `-race`**, on this VM. kki.3 §2/§7 showed contention changes verdicts (119 false
  `TIMED OUT` at the default `-w 6`), so `KILLED`/`LIVED` here mean "as measured in that
  configuration". In particular the six LIVED mutants in §5.1 might not be the tool's
  verdict under a `-race` gate.
* **`--detect-equivalent` was never enabled** (`03-gomutants.md` §8), so nothing here
  speaks to gomutants' own `EQUIVALENT` verdict, only to our reading of its generated edits.
* **Anchors are text-located at `19ea479`** (which the header shows is the same tree for
  `srv/` and `scripts/mutation-check.sh` as current `8a908d7`). If either moves, the rows
  must be recomputed; the grid's 5th-field `-run` routing was deliberately not considered,
  since it cannot change whether a mutant exists.
* **Traceability is not replaceability.** An equivalent mutant may break unrelated
  behaviour, and a curated mutation's single assertion may be the only thing pinning a
  claim; a traceable row means the defect is *expressible somewhere in gomutants' set*, not
  that swapping the row for that mutant would preserve the grid's meaning.
* **Nothing here says gomutants has no value.** 300 of its 357 judged mutants are off the
  anchor lines, 66 of them LIVED; §6 reports that breadth factually, and weighing it against
  the 23 inexpressible rows is `.6`'s job.

---

## 8. Verification state

Commands this audit ran (all read-only except documentation):

```bash
./scripts/mutation-check.sh --list                 # 41 names
python3 /tmp/kki5-work/analyze.py                  # reads the repo + /home/exedev/kki5-data; writes only /tmp
make verify                                        # gofmt, go vet, go build, go test -race -count=1, ./scripts/docs-split-check.sh
git status --porcelain                             # only the two files below
git diff --stat                                    # empty outside docs/mutation-bench/
```

`make verify` ran against the finished document at `8a908d7` and reported
`==> verify: all checks passed`, including the `docs-split-check.sh` gate
(`RESULT: GREEN`) that landed in that commit. Working tree at the end of the audit:
`M docs/mutation-bench/README.md` and `?? docs/mutation-bench/05-traceability.md` — nothing
else, tracked or untracked.

**No new full mutation pass was run** (neither the curated grid nor gomutants), and no
`--run-mutant-id` lookup was needed: every row is answered by `report-B.json`,
`conductor-permutant.tsv`/`mut/*.json`, `dry-srv.txt` and `04-timeout-semantics.md`, with
`04` supplying the hang-row semantics. The analysis helper stays in `/tmp/kki5-work` because
the epic's ground rules allow only `scripts/mutation-bench.sh` and `docs/mutation-bench/*.md`
as new repo files.

The kki.3 artifacts this audit depends on were copied out of `/tmp` to
`/home/exedev/kki5-data` (§2) — that is outside the repo, so the tree stays clean while the
evidence stops being volatile. `scripts/mutation-check.sh`, `Makefile`, `srv/*.go`, the root
`README.md` and `AGENTS.md` are unmodified; no `.gomutants.yml`, no mutation report and no
cache file is in the working tree.

