#!/usr/bin/env bash
#
# mutation-check.sh — prove the test suite is not vacuous.
#
# A green test run means nothing on its own: a test that asserts `err == nil`
# on everything, or one whose assertion was quietly loosened, passes just as
# loudly as a real one. This script is the repo-owned answer. It introduces a
# series of small, deliberate defects ("mutations") into the implementation,
# runs the test suite after each, and REQUIRES the suite to fail. A mutation
# that survives is reported and makes the script exit non-zero.
#
# Every mutation is:
#   * expressed as a literal old->new string pair in MUTATIONS below,
#   * applied with python3 (no GNU-sed/BSD-sed portability trap),
#   * checked to have actually changed the file (otherwise the mutation is
#     reported as BROKEN — the implementation moved on and this check needs
#     updating, which is a different failure from a missed mutation),
#   * reverted and verified byte-identical (recorded sha256) whatever happens,
#     including on Ctrl-C: the revert runs twice, from the loop and from an
#     EXIT trap, and the trap restores every file from a pristine backup.
#
# Bounded: each mutation's test run has an explicit timeout, the whole script
# has an outer timeout when run via `make mutation-check`, and `go test` is run
# with -timeout so a hang inside one test fails that mutation rather than
# hanging the review. The script needs no network and no external service.
#
# Usage:
#   ./scripts/mutation-check.sh                    # all mutations, whole suite
#   ./scripts/mutation-check.sh --list             # names only
#   ./scripts/mutation-check.sh <name>...          # selected mutations
#   MUTATION_TEST_ARGS="-run TestIntegration" \
#     ./scripts/mutation-check.sh                  # only the integration harness
#
# The last form is the one that matters when a NEW test file is added: it
# proves the new tests on their own catch the defects, rather than relying on
# the older unit tests to do the work.
#
# Exit status: 0 if every mutation was caught, 1 otherwise.

set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 2
REPO_ROOT="$(pwd)"

# Per-mutation test timeout, and the go test binary's own internal timeout
# (which is what turns a hung test into a failure with a stack dump).
MUTATION_TIMEOUT="${MUTATION_TIMEOUT:-180}"
GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-150s}"

# Extra arguments for `go test`. Empty by default (the whole suite); set to
# e.g. "-run TestIntegration" to hold only the integration harness to account.
MUTATION_TEST_ARGS="${MUTATION_TEST_ARGS:-}"
read -r -a TEST_ARGS <<<"$MUTATION_TEST_ARGS"

BACKUP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mutation-check.XXXXXX")"
trap 'restore_all; rm -rf "$BACKUP_DIR"' EXIT

# ---------------------------------------------------------------- mutations --
#
# Format: <name>|<file>|<old>|<new>|<optional -run regex>
#
# The old text must appear exactly once. Keep each mutation SMALL and tied to
# one behavioural claim, so a reviewer can see the intent.
#
# The optional 5th field narrows the test run for that one mutation (e.g. a
# deliberately WEDGED handler makes every other test block until the go test
# timeout, so hold it to the one test that is supposed to notice). It REPLACES
# MUTATION_TEST_ARGS for that mutation rather than narrowing it further, so a
# mutation with a 5th field ignores an outer `-run` restriction; an alternation
# such as TestA|TestB routes one mutation to several tests. Most mutations leave
# it empty and run the whole suite.
#
# Row 14 is anchored in the TEMPLATE, not in Go, and that is deliberate. Its
# claim is behavioural: "GET / never serves a body that stops mid-document". The
# original anchor substituted a truncated pageData struct, which only truncated
# the render because welcome.html named a field the struct lacked. After the
# exe.dev removal (issue strudel-agent-rka.1) the template has no field
# references at all, so that substitution now compiles AND renders the full
# page — the row would report SURVIVED, a silently vacuous grid. Naming a field
# pageData does not have reproduces the same defect (html/template aborts at
# that node, HandleRoot swallows the error via slog.Warn, and the visitor gets a
# 200 with a truncated body) and keeps the row load-bearing on pageData being a
# struct: against nil data the same mutation renders empty and SURVIVES, so the
# grid fails loudly if anyone "simplifies" pageData away.
#
# The anchor itself was re-pointed in issue strudel-agent-3vo.6: it had named an
# `<h1>Strudel Agent</h1>` that the three-region shell (commit 2450f53) deleted,
# so the row had been reporting BROKEN — the grid could no longer see the defect
# it exists to catch, and only running it end to end said so. The anchor is now
# the agent-message TEXT node (the `<h1>`'s closest surviving analogue: body
# content, before </main>, appearing exactly once), which the same substitution
# turns into an unresolvable field reference. An unresolvable ACTION is what
# aborts the render, so the replacement must keep the `{{...}}` braces — a
# mutation that merely deleted the text would render fine and SURVIVE.
#
# Rows 61-64 cover the keepalive reaper (issue strudel-agent-3vo.3.6), and they
# constrain an EXISTING row in a way worth recording. Row 38 proves the per-write
# deadline using TestWSWedgedListenerCannotHangTeardown: it removes the deadline
# and requires the test to fail. Now that the handler also reaps clients that
# stop answering, that reaper is a SECOND way the same wedged test could start
# passing — the keepalive would give up on the client for the reaper's reasons
# while the write deadline was gone. That is the failure mode this repo calls a
# bound making a hang pass: coverage that looks intact because some other
# mechanism now supplies the outcome the assertion was there to prove.
#
# It is prevented structurally rather than by assertion: the wedged test runs at
# the DEFAULT ping interval (30s), so the reaper cannot plausibly fire inside a
# test measured in seconds, leaving the write deadline as the only possible
# cause. Rows 38 and 61-64 are therefore complementary and both must stay caught.
# If a future change makes the wedged test install a short ping interval, row 38
# must be re-examined before that change is accepted.
MUTATIONS=$(cat <<'EOF'
01-empty-code-accepted|srv/api.go|strings.TrimSpace(req.Code) == ""|false
02-unknown-json-field-allowed|srv/api.go|dec.DisallowUnknownFields()|_ = dec
03-trailing-json-allowed|srv/api.go|return errTrailingJSON|return nil
04-payload-cap-removed|srv/api.go|http.MaxBytesReader(w, r.Body, APIMaxBodyBytes)|http.MaxBytesReader(w, r.Body, int64(1)<<40)
05-cap-reported-as-400|srv/api.go|writeError(w, http.StatusRequestEntityTooLarge,|writeError(w, http.StatusBadRequest,
06-allow-header-dropped|srv/api.go|w.Header().Set("Allow", want)|_ = want
07-api-404-is-500|srv/api.go|writeError(w, http.StatusNotFound, fmt.Sprintf("no such API endpoint|writeError(w, http.StatusInternalServerError, fmt.Sprintf("no such API endpoint
08-api-catchall-too-broad|srv/api.go|mux.HandleFunc("/api", handleAPINotFound)|mux.HandleFunc("/", handleAPINotFound)
09-static-mount-removed|srv/api.go|mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.StaticDir))))|_ = s.StaticDir
10-eval-ack-lies|srv/api.go|apiEvalAck{Accepted: true, Version: req.Version}|apiEvalAck{Accepted: false, Version: req.Version}
11-message-bumps-version|srv/api.go|s.Conductor.SetMessage(req.Message)|s.Conductor.Publish(req.Message, req.Message)
12-transport-accepts-any-body|srv/api.go|if err := rejectUnexpectedBody(r); err != nil {|if err := error(nil); false {
13-hush-plays-instead|srv/api.go|s.handleAPITransport(w, r, false)|s.handleAPITransport(w, r, true)
14-shell-render-silently-truncates|srv/templates/welcome.html|System initialized. Awaiting agent input...|{{.NoSuchField}}
15-eval-accepts-future-version|srv/conductor.go|res.Version > c.version {|res.Version > c.version+1 {
16-eval-result-not-stored|srv/conductor.go|c.lastEval = &stored|_ = stored
17-stale-report-regresses|srv/conductor.go|if c.lastEval != nil && res.Version < c.lastEval.Version {|if false {
18-same-version-report-ignored|srv/conductor.go|if c.lastEval != nil && res.Version < c.lastEval.Version {|if c.lastEval != nil && res.Version <= c.lastEval.Version {
19-version-not-monotonic|srv/conductor.go|c.version++|c.version += 0
20-version-skipped|srv/conductor.go|c.version++|c.version += 2
21-history-window-wrong-start|srv/conductor.go|start := c.histNext - c.histLen|start := 0
22-snapshot-omits-playing|srv/conductor.go|Playing:          c.playing,|Playing:          false,
23-wedged-state-handler|srv/api.go|func (s *Server) handleAPIState(w http.ResponseWriter, r *http.Request) {|func (s *Server) handleAPIState(w http.ResponseWriter, r *http.Request) {\n\tselect {} // deliberately wedged: never responds|TestIntegrationParallelPushesAreBoundedAndContiguous
24-ws-405-fallback-removed|srv/api.go|mux.HandleFunc("/ws", methodNotAllowed(http.MethodGet))|_ = methodNotAllowed(http.MethodGet)
25-ws-wrong-close-status|srv/ws.go|websocket.StatusNormalClosure|websocket.StatusGoingAway
26-ws-origin-check-disabled|srv/ws.go|websocket.Accept(w, r, nil)|websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
27-ws-closes-without-handshake|srv/ws.go|conn.Close(websocket.StatusNormalClosure, "")|conn.CloseNow()
28-hub-double-close-allowed|srv/hub.go|if _, present := subs[sub]; !present {\n\t\treturn false\n\t}|if false {\n\t\treturn false\n\t}
29-hub-unsubscribe-does-not-close|srv/hub.go|close(sub.ch)\n\tdelete(subs, sub)\n\treturn true|delete(subs, sub)\n\treturn true|TestHubUnsubscribeStopsDeliveryAndClosesExactlyOnce
30-hub-slow-client-blocks|srv/hub.go|select {\n\tcase sub.ch <- msg:\n\t\treturn true\n\tdefault:\n\t\treturn false\n\t}|sub.ch <- msg\n\treturn true|TestHubSlowSubscriberCannotBlockOthers|TestHubConcurrentSubscribeUnsubscribeBroadcast
31-hub-broadcast-skips-a-subscriber|srv/hub.go|\t\t\tfor sub := range subs {\n\t\t\t\tif sub == skip {|\t\t\tskipped := false\n\t\t\tfor sub := range subs {\n\t\t\t\tif !skipped {\n\t\t\t\t\tskipped = true\n\t\t\t\t\tcontinue\n\t\t\t\t}\n\t\t\t\tif sub == skip {|TestHubBroadcastReachesEverySubscriberWithIdenticalBytes
32-hub-count-escapes-the-hub-goroutine|srv/hub.go|case reply := <-h.count:\n\t\t\treply <- len(subs)|case reply := <-h.count:\n\t\t\tgo func() { reply <- len(subs) }()
33-hub-dropped-client-not-removed|srv/hub.go|\t\t\t\tif !h.deliver(sub, msg) {\n\t\t\t\t\tremoveSubscriber(subs, sub)|\t\t\t\tif !h.deliver(sub, msg) {\n\t\t\t\t\t_ = sub // deliberately: the drop is never removed from the set
34-hub-removal-not-recorded|srv/hub.go|close(sub.ch)\n\tdelete(subs, sub)\n\treturn true|close(sub.ch)\n\treturn true
35-ws-listener-never-subscribes|srv/ws.go|sub, err := s.Hub.Subscribe()|sub, err := (*Subscriber)(nil), error(ErrHubClosed)|TestWSUpgradeSucceedsAndStaysOpen
36-ws-subscriber-leaked-on-exit|srv/ws.go|\tdefer s.Hub.Unsubscribe(sub)|\t_ = sub // deliberately leaked: the subscriber is never removed|TestWSDisconnectUnsubscribesTheListener
37-ws-broadcast-trimmed|srv/ws.go|return conn.Write(ctx, websocket.MessageText, msg)|return conn.Write(ctx, websocket.MessageText, msg[:max(0, len(msg)-1)])|TestWSBroadcastReachesTheListenerAsExactBytes
38-ws-write-deadline-removed|srv/ws.go|ctx, cancel := context.WithTimeout(context.Background(), s.wsWriteTimeout)|ctx, cancel := context.WithCancel(context.Background())
39-ws-going-away-not-reported|srv/ws.go|websocket.StatusGoingAway, "server shutting down"|websocket.StatusNormalClosure, "server shutting down"
40-ws-client-close-not-noticed|srv/ws.go|clientGone := conn.CloseRead(context.Background())|clientGone := context.Background()|TestWSDisconnectUnsubscribesTheListener
41-ws-binary-frames|srv/ws.go|return conn.Write(ctx, websocket.MessageText, msg)|return conn.Write(ctx, websocket.MessageBinary, msg)
42-no-snapshot-on-connect|srv/ws.go|if err := s.sendSnapshot(conn); err != nil {\n\t\tslog.Debug("websocket snapshot", "path", r.URL.Path, "error", err)\n\t}|_ = 0 // deliberately no catch-up snapshot on connect|TestWSUpgradeSucceedsAndStaysOpen
43-connect-frame-not-a-snapshot|srv/ws.go|s.encodeEvent(EventSnapshot, s.Conductor.Snapshot())|s.encodeEvent(EventCode, s.Conductor.Snapshot())|TestWSUpgradeSucceedsAndStaysOpen
44-code-write-not-broadcast|srv/api.go|s.broadcast(EventCode, snap)|_ = snap // deliberately not broadcast|TestIntegrationEveryAcceptedWriteFansOutOnceToEveryListener
45-message-write-not-broadcast|srv/api.go|s.broadcast(EventMessage, snap)|_ = snap // deliberately not broadcast|TestIntegrationEveryAcceptedWriteFansOutOnceToEveryListener
46-transport-write-not-broadcast|srv/api.go|s.broadcast(EventTransport, snap)|_ = snap // deliberately not broadcast|TestIntegrationEveryAcceptedWriteFansOutOnceToEveryListener
47-eval-result-not-broadcast|srv/api.go|s.broadcast(EventEvalResult, s.Conductor.Snapshot())|_ = 0 // deliberately not broadcast|TestIntegrationEveryAcceptedWriteFansOutOnceToEveryListener
48-stale-eval-result-broadcasts|srv/conductor.go|return false, nil|return true, nil // deliberately claims a stale report was stored|TestIntegrationRejectedAndStaleWritesBroadcastNothing
49-code-broadcast-twice|srv/api.go|s.broadcast(EventCode, snap)|s.broadcast(EventCode, snap)\n\ts.broadcast(EventCode, snap)|TestIntegrationEveryAcceptedWriteFansOutOnceToEveryListener
50-rejected-code-still-broadcasts|srv/api.go|\t\twriteError(w, http.StatusBadRequest, "code must not be empty: send the strudel pattern to play")\n\t\treturn\n\t}|\t\ts.broadcast(EventCode, s.Conductor.Snapshot())\n\t\twriteError(w, http.StatusBadRequest, "code must not be empty: send the strudel pattern to play")\n\t\treturn\n\t}|TestIntegrationRejectedAndStaleWritesBroadcastNothing
51-connect-snapshot-not-the-live-state|srv/ws.go|s.encodeEvent(EventSnapshot, s.Conductor.Snapshot())|s.encodeEvent(EventSnapshot, Snapshot{})|TestIntegrationConnectReceivesTheLiveSnapshot
52-count-hook-never-installed|srv/server.go|NewHubWithCountHook(HubDefaultSendBuffer, s.listenerCountFrame)|NewHub(HubDefaultSendBuffer)|TestWSListenerCountIsPublishedOnConnectAndDisconnect
53-count-not-published-on-subscribe|srv/hub.go|\t\t\tsubs[sub] = struct{}{}\n\t\t\tpublishCount(sub)\n\t\t\treq.reply <- sub|\t\t\tsubs[sub] = struct{}{}\n\t\t\treq.reply <- sub|TestHubCountHookReportsEveryTransition
54-count-not-published-on-unsubscribe|srv/hub.go|\t\t\tif removed {\n\t\t\t\tpublishCount(req.sub)\n\t\t\t}\n\t\t\treq.reply <- removed|\t\t\tif false {\n\t\t\t\tpublishCount(req.sub)\n\t\t\t}\n\t\t\treq.reply <- removed|TestWSListenerCountIsPublishedOnConnectAndDisconnect
55-count-not-published-on-drop|srv/hub.go|\t\t\tif h.onCount == nil {\n\t\t\t\treturn\n\t\t\t}|\t\t\tif true {\n\t\t\t\treturn // deliberately: a dropped subscriber never publishes the new count\n\t\t\t}|TestHubCountHookReportsTheDropOfASlowSubscriber|TestHubCountHookFrameReachesTheSurvivorsOfADrop
56-count-published-after-the-subscribe-reply|srv/hub.go|\t\t\tpublishCount(sub)\n\t\t\treq.reply <- sub|\t\t\treq.reply <- sub\n\t\t\tpublishCount(sub)|TestHubCountHookIsPublishedBeforeTheCommandIsAnswered|TestWSConnectSnapshotCountsTheConnectingListener
57-count-not-published-on-close|srv/hub.go|\t\t\tpublishCount(nil)\n\t\t\tclose(reply)|\t\t\tclose(reply)|TestHubCountHookPublishesZeroOnClose
58-count-published-when-nothing-changed|srv/hub.go|\t\t\tif len(subs) == before {\n\t\t\t\treturn\n\t\t\t}\n|\t\t\tif len(subs) == before {\n\t\t\t\t_ = h.onCount(len(subs)) // deliberately: publishes a count nobody changed\n\t\t\t\treturn\n\t\t\t}\n|TestHubCountHookReportsEveryTransition
59-negative-listener-count-not-clamped|srv/conductor.go|\tif n < 0 {\n\t\tn = 0\n\t}\n\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n\tc.listeners = n|\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n\tc.listeners = n|TestConductorSetListenerCountClampsAndLeavesTheVersionAlone
60-listener-count-bumps-the-version|srv/conductor.go|\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n\tc.listeners = n|\tc.mu.Lock()\n\tdefer c.mu.Unlock()\n\tc.listeners = n\n\tc.version++|TestConductorListenerCountDoesNotBumpVersion
61-no-keepalive-at-all|srv/ws.go|\t\tcase <-ping.C:|\t\tcase <-time.After(time.Duration(1<<62)):|TestWSDeadListenerIsReaped
62-keepalive-failure-ignored|srv/ws.go|\t\t\tif err := s.wsPingListener(conn); err != nil {|\t\t\tif err := s.wsPingListener(conn); false {|TestWSDeadListenerIsReaped
63-pong-deadline-removed|srv/ws.go|ctx, cancel := context.WithTimeout(context.Background(), s.wsPongTimeout)|ctx, cancel := context.WithCancel(context.Background())|TestWSDeadListenerIsReaped
64-keepalive-reaps-the-living|srv/ws.go|\t\t\tif err := s.wsPingListener(conn); err != nil {|\t\t\tif err := s.wsPingListener(conn); err == nil {|TestWSListenerSurvivesPingCycles
65-listener-count-frame-never-built|srv/server.go|\treturn s.encodeEvent(EventListenerCount, s.Conductor.Snapshot())|\treturn nil // deliberately: the count is published but nobody is told|TestWSListenerCountIsBroadcastAsAnEvent
66-listener-count-wrong-kind|srv/server.go|s.encodeEvent(EventListenerCount, s.Conductor.Snapshot())|s.encodeEvent(EventTransport, s.Conductor.Snapshot())|TestWSListenerCountIsBroadcastAsAnEvent
67-count-frame-sent-to-its-cause|srv/hub.go|\t\t\t\tif sub == skip {\n\t\t\t\t\tcontinue\n\t\t\t\t}|\t\t\t\t_ = skip // deliberately: the listener that caused the change is told too|TestWSListenerCountFrameIsNotSentToTheListenerThatCausedIt|TestHubCountHookFrameReachesEveryoneExceptTheCause
68-count-frame-built-and-thrown-away|srv/hub.go|\t\tif frame := h.onCount(len(subs)); frame != nil {\n\t\t\tfanOut(frame, cause)\n\t\t}|\t\t_ = h.onCount(len(subs)) // deliberately: the frame is built and never delivered|TestWSListenerCountIsBroadcastAsAnEvent|TestHubCountHookFrameReachesEveryoneExceptTheCause
69-drop-not-broadcast-to-survivors|srv/hub.go|\t\t\tmsg = h.onCount(len(subs))\n\t\t\tif msg == nil {\n\t\t\t\treturn\n\t\t\t}|\t\t\t_ = h.onCount(len(subs)) // deliberately: a drop publishes the count but sends no frame|TestHubCountHookFrameReachesTheSurvivorsOfADrop
70-cli-rejection-exits-zero|cmd/agentcli/main.go|\treturn exitError|\treturn exitOK // deliberately: the failure is printed but reported as success|TestRejectionIsReportedVerbatimAndFails|TestUsageErrorsExitTwoWithoutSending|TestUnreachableServerIsNotARejection|TestAnchorRefusalIsReportedVerbatimAndFails
71-cli-swalows-the-servers-reason|cmd/agentcli/client.go|\treturn probe.Error|\treturn "request failed" // deliberately: the server's exact reason is replaced by a generic one|TestRejectionIsReportedVerbatimAndFails|TestOversizedDocumentIsRejectedByTheServer|TestAnchorRefusalIsReportedVerbatimAndFails
72-cli-reports-a-stale-verdict-as-stored|cmd/agentcli/commands.go|\tstale := before.LastEvalResult != nil && *ver < before.LastEvalResult.Version|\tstale := false // deliberately: "accepted" is reported as "stored"|TestEvalResultDistinguishesStoredFromIgnored
73-cli-hush-noop-reported-as-a-change|cmd/agentcli/commands.go|\tif before.Playing == playing {|\tif before.Playing != playing && false { // deliberately: an idempotent no-op is reported as a change|TestTransportReportsANoopAsANoop
74-livecode-editor-adapter-not-loaded|srv/templates/welcome.html|    <script src="/static/editor.js" defer></script>|    <!-- deliberately: the editor adapter is never loaded -->|TestLiveCodeEditorView
75-livecode-view-writable|srv/templates/welcome.html|spellcheck="false" readonly aria-readonly="true"|spellcheck="false" aria-readonly="false"|TestLiveCodeEditorView
76-livecode-error-region-not-highlighted|srv/static/editor.js|cm.addLineClass(line, 'background', 'editor-error-line');|void line; // deliberately: the failing region is not highlighted|TestLiveCodeEditorView
77-livecode-flash-cue-dropped|srv/static/editor.js|root.classList.add('editor-flash');|void root; // deliberately: no landed-version flash|TestLiveCodeEditorView
78-livecode-code-never-shown|srv/static/session.js|      ed.setCode(snapshot.code);|      // deliberately: the arriving version is not shown|TestLiveCodeEditorView
79-anchor-route-not-mounted|srv/api.go|	mux.HandleFunc("POST /api/anchor", s.handleAPIAnchor)|	// deliberately: no agent can re-anchor the shared timeline|TestAnchorRoute
80-anchor-zero-rate-guard-removed|srv/conductor.go|	if !(cps > 0) {|	if false { // deliberately: a zero or NaN rate is accepted as a shared timeline|TestAnchorRouteRejectsNonsenseAnchor|TestConductorSetAnchorRejectsNonsense
81-anchor-rate-ceiling-removed|srv/conductor.go|	if cps > AnchorMaxCPS {|	if false { // deliberately: a runaway rate is accepted as a shared timeline|TestAnchorRouteRejectsNonsenseAnchor|TestConductorSetAnchorRejectsNonsense
81b-anchor-epoch-skew-guard-removed|srv/conductor.go|	if skew > AnchorMaxSkewMS {|	if false { // deliberately: an epoch from a badly-skewed clock is accepted|TestAnchorRouteRejectsNonsenseAnchor|TestConductorSetAnchorRejectsNonsense
82-refused-anchor-still-stored|srv/conductor.go|	if err := validateAnchor(epochMS, cps); err != nil {\n\t\treturn err\n\t}|	if err := validateAnchor(epochMS, cps); err != nil {\n\t\t_ = err // deliberately: the refusal is noticed and then ignored, so a rejected anchor is stored anyway\n\t}|TestConductorSetAnchorRejectsNonsense|TestAnchorRouteRejectsNonsenseAnchor
83-anchor-frame-never-built|srv/api.go|	s.broadcast(EventAnchor, snap)|	_ = snap // deliberately: the anchor is stored but nobody is told|TestAnchorRouteAcceptedBroadcastsOneAnchorFrame|TestAnchorRouteRejectedBroadcastsNothing
84-anchor-frame-wrong-kind|srv/api.go|	s.broadcast(EventAnchor, snap)\n\twriteJSON(w, http.StatusOK, snap)\n}|	s.broadcast(EventTransport, snap)\n\twriteJSON(w, http.StatusOK, snap)\n}|TestAnchorRouteAcceptedBroadcastsOneAnchorFrame
85-refused-anchor-still-broadcast|srv/api.go|	if err := s.Conductor.SetAnchor(req.EpochMS, req.CPS); err != nil {\n\t\twriteError(w, http.StatusBadRequest, err.Error())\n\t\treturn\n\t}|	if err := s.Conductor.SetAnchor(req.EpochMS, req.CPS); err != nil {\n\t\tdefer s.broadcast(EventAnchor, s.Conductor.Snapshot())\n\t\twriteError(w, http.StatusBadRequest, err.Error())\n\t\treturn\n\t} // deliberately: a rejected re-anchor is announced to every listener|TestAnchorRouteRejectedBroadcastsNothing
86-sync-js-never-loaded|srv/templates/welcome.html|    <script src="/static/sync.js" defer></script>|    <!-- deliberately: the cycle-alignment module is never loaded -->|TestSyncModuleIsServedAndCarriesTheContract
87-commit-not-boundary-deferred|srv/static/session.js|    var handle = sync().scheduleAtBoundary(function (at) {|    var handle = commitNow(commit, version, stats, pattern), sync().scheduleAtBoundary(function (at) { // deliberately: the commit is ALSO applied on arrival, which is the drift being fixed|TestSessionDefersTheCommitToTheBoundary
88-sync-lead-removed|srv/static/sync.js|    return anchor.epochMs + nextIndex * cycleMs + lead;|    return anchor.epochMs + nextIndex * cycleMs; // deliberately: commits at the raw boundary, so a late frame rounds to a different bar|TestTheLeadIsWhatAlignsTheLateClient|TestTwoClientsOnTheSameAnchorCommitOnTheSameBar
89-pending-commit-not-cancelled|srv/static/session.js|      pendingCommit.cancel();|      // deliberately: a superseded pattern still lands after the version that replaced it|TestSyncModuleIsServedAndCarriesTheContract|TestANewerVersionSupersedesAPendingCommit
90-cli-anchor-noop-reported-as-a-change|cmd/agentcli/commands.go|\tif req.EpochMS == before.Anchor.EpochMS && req.CPS == before.Anchor.CPS {|\tif false { // deliberately: re-sending the anchor the server already holds is reported as a fresh re-anchor|TestAnchorReportsANoopAsANoop
91-cli-anchor-half-not-carried-over|cmd/agentcli/commands.go|\treq := anchorRequest{EpochMS: before.Anchor.EpochMS, CPS: before.Anchor.CPS}|\treq := anchorRequest{} // deliberately: the half the caller did not name is sent as zero, so a tempo change loses the epoch|TestAnchorCarriesOverTheHalfItWasNotGiven
92-cli-anchor-no-pre-read|cmd/agentcli/commands.go|\tbefore, err := c.get(ctx, "/api/state")\n\tif err != nil {\n\t\treturn err\n\t}\n\n\t// The half the caller did not name is carried over from the state just|\tvar before snapshot\n\t// deliberately: the comparison is gone, so every accepted re-anchor reads as a change|TestAnchorReportsANoopAsANoop|TestAnchorCarriesOverTheHalfItWasNotGiven
93-sync-status-region-dropped|srv/templates/welcome.html|<div class="sync-status" id="sync-status" aria-live="polite"></div>|<!-- deliberately: sync-status region dropped -->|TestSyncModuleIsServedAndCarriesTheContract
94-sync-drift-value-not-written|srv/static/session.js|var driftText = typeof driftVal === "number"\n          ? (driftVal >= 0 ? "+" : "") + Math.round(driftVal) + "ms"\n          : String(driftVal);|var driftText = "0ms"; // deliberately: drift value is never written|TestSyncStatusUIRendersDriftAndStateInGoja
95-sync-css-cue-removed|srv/static/style.css|.sync-drift {|.sync-drift-removed {|TestSyncModuleIsServedAndCarriesTheContract
96-sync-status-writes-on-every-tick|srv/static/session.js|if (el.innerHTML === next) {\n      return;\n    }\n    el.innerHTML = next;|el.innerHTML = next; // deliberately: an identical write still mutates the aria-live region, so the countdown is announced on every tick|TestSyncStatusUIOnlyWritesOnChangeAndStopsOnPageHide
97-sync-status-timer-never-stopped|srv/static/session.js|  window.addEventListener("pagehide", stopStatusTimer);|  // deliberately: the status interval outlives the page|TestSyncStatusUIOnlyWritesOnChangeAndStopsOnPageHide
98-cli-subcommand-help-is-a-usage-error|cmd/agentcli/commands.go|\t\tif errors.Is(err, flag.ErrHelp) {\n\t\t\treturn errHelpRequested\n\t\t}|\t\t_ = errHelpRequested // deliberately: ErrHelp is no longer special-cased, so -h is a usage error again and no flags are printed|TestSubcommandHelpPrintsItsOwnFlags|TestFlaglessSubcommandHelpExitsZero
99-cli-subcommand-help-exits-two|cmd/agentcli/main.go|\tif errors.Is(err, errHelpRequested) {\n\t\treturn exitOK\n\t}|\tif errors.Is(err, errHelpRequested) {\n\t\terr = errHelpRequested\n\t} // deliberately: the sentinel is NOT mapped to exit 0, so help exits 1|TestSubcommandHelpPrintsItsOwnFlags|TestFlaglessSubcommandHelpExitsZero
100-cli-every-parse-error-is-help|cmd/agentcli/commands.go|\t\tif errors.Is(err, flag.ErrHelp) {\n\t\t\treturn errHelpRequested\n\t\t}\n\t\treturn &usageError{msg: err.Error()}|\t\treturn errHelpRequested // deliberately: every parse error is called help, so a bad flag exits 0|TestSubcommandParseErrorsAreNotHelpRequests|TestSubcommandHelpDoesNotWeakenTheExitCodeContract
101-cli-flagless-help-hides-real-mistakes|cmd/agentcli/commands.go|\tif wantsFlaglessHelp(args) {\n\t\tprintFlaglessUsage(stderr, "state", "print the current snapshot")|\tif len(args) >= 0 {\n\t\tprintFlaglessUsage(stderr, "state", "print the current snapshot") // deliberately: every invocation is answered with help, so `state extra` is never reported|TestFlaglessSubcommandHelpExitsZero|TestUsageErrorsExitTwoWithoutSending
EOF
)

# --------------------------------------------------------------- helpers ----

restore_all() {
	for path in "${BACKED_UP[@]:-}"; do
		[ -n "$path" ] || continue
		if [ -f "$BACKUP_DIR/$path" ]; then
			cp -p "$BACKUP_DIR/$path" "$path"
		fi
	done
}

BACKED_UP=()
declare -A ORIGINAL_HASH=()

backup() {
	local path="$1"
	if [ ! -f "$BACKUP_DIR/$path" ]; then
		mkdir -p "$BACKUP_DIR/$(dirname "$path")"
		cp -p "$path" "$BACKUP_DIR/$path"
		BACKED_UP+=("$path")
		ORIGINAL_HASH["$path"]="$(sha256sum "$path" | awk '{print $1}')"
	fi
}

# apply_mutation writes old->new in path. It prints nothing on success and an
# error message (plus non-zero) if the anchor text is missing or ambiguous.
apply_mutation() {
	local path="$1" old="$2" new="$3"
	python3 - "$path" "$old" "$new" <<'PY'
import sys
path, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
# The mutation table is pipe-delimited, one mutation per line, so a literal
# newline or tab has to be written as the two characters \n / \t and
# unescaped here. (No mutation text contains a real backslash, so a simple
# textual unescape is unambiguous.)
def unescape(text):
    return text.replace("\\n", "\n").replace("\\t", "\t")
old = unescape(old)
new = unescape(new)
with open(path, encoding="utf-8") as fh:
    src = fh.read()
count = src.count(old)
if count != 1:
    sys.stderr.write(f"anchor appears {count} times in {path}, want exactly 1\n")
    sys.exit(3)
with open(path, "w", encoding="utf-8") as fh:
    fh.write(src.replace(old, new, 1))
PY
}

# run_tests runs the suite for one mutation, bounded. It echoes nothing on
# success; on failure it prints the first failing test lines, which is the
# evidence a reviewer wants ("mutation X was caught by test Y").
run_tests() {
	local log="$BACKUP_DIR/last-test.log"
	local args=("${TEST_ARGS[@]}")
	if [ -n "${1:-}" ]; then
		args=(-run "$1")
	fi
	if timeout "$MUTATION_TIMEOUT" go test ./... "${args[@]}" -race -count=1 -timeout "$GO_TEST_TIMEOUT" >"$log" 2>&1; then
		return 1 # tests passed => mutation survived
	fi

	# A mutation must be caught by a FAILING TEST, which is either "--- FAIL:"
	# or a race/panic report. A mutation that merely fails to COMPILE proves
	# nothing about the assertions, so it is reported separately as weak
	# evidence rather than as a catch. Without this, a typo in the mutation
	# table would look like a passing check.
	if ! grep -qE '(^--- FAIL:|^    --- FAIL:|^panic:|^DATA RACE)' "$log"; then
		echo "no test failed — the mutation did not build or did not run"
		head -3 "$log" | cut -c1-200 | sed 's/^/      /'
		return 2
	fi

	# Print the first few FAILING test names and assertion lines: the evidence a
	# reviewer wants ("mutation X was caught by test Y"). Lines are truncated
	# because a failed assertion can echo a whole 64 KiB response body.
	grep -E '^(--- FAIL|    --- FAIL|FAIL|panic:|DATA RACE|.*\.go:[0-9]+:)' "$log" \
		| head -3 | cut -c1-200 | sed 's/^/      /'
	return 0
}

# --------------------------------------------------------------- grid -------

names=()
files=()
olds=()
news=()
runs=()

while IFS='|' read -r name file old new run; do
	[ -n "$name" ] || continue
	names+=("$name"); files+=("$file"); olds+=("$old"); news+=("$new"); runs+=("$run")
done <<<"$MUTATIONS"

selection=()
if [ "${1:-}" = "--list" ]; then
	for n in "${names[@]}"; do echo "$n"; done
	exit 0
fi
if [ "$#" -gt 0 ]; then
	selection=("$@")
fi
selected() {
	local want="$1"
	[ "${#selection[@]}" -eq 0 ] && return 0
	local s
	for s in "${selection[@]}"; do [ "$s" = "$want" ] && return 0; done
	return 1
}

# A clean baseline first: if the suite is already red, every mutation would
# "fail" and the check would be meaningless. Report that plainly.
echo "mutation-check: baseline (unmutated) run${MUTATION_TEST_ARGS:+ [go test ./... $MUTATION_TEST_ARGS]}"
baseline_log="$BACKUP_DIR/baseline.log"
if ! timeout "$MUTATION_TIMEOUT" go test ./... "${TEST_ARGS[@]}" -race -count=1 -timeout "$GO_TEST_TIMEOUT" >"$baseline_log" 2>&1; then
	echo "mutation-check: BASELINE IS ALREADY FAILING — fix that first, this check cannot mean anything." >&2
	sed 's/^/      /' "$baseline_log" | tail -20 >&2
	exit 1
fi
echo "mutation-check: baseline green"
echo

caught=0
survived=0
broken=0
weak=0
caught_names=()
survived_names=()
broken_names=()
weak_names=()

for i in "${!names[@]}"; do
	name="${names[$i]}"; file="${files[$i]}"; old="${olds[$i]}"; new="${news[$i]}"
	selected "$name" || continue

	printf '%-38s ' "$name"
	if [ ! -f "$file" ]; then
		echo "BROKEN (missing file $file)"
		broken=$((broken + 1)); broken_names+=("$name")
		continue
	fi

	backup "$file"
	if ! apply_mutation "$file" "$old" "$new"; then
		echo "BROKEN (anchor text not found — the implementation moved; update this script)"
		broken=$((broken + 1)); broken_names+=("$name")
		restore_all
		continue
	fi

	run_tests "${runs[$i]}"
	case $? in
	0)
		echo "caught"
		caught=$((caught + 1)); caught_names+=("$name")
		;;
	2)
		echo "WEAK  <-- did not build/failed on something other than a test assertion"
		weak=$((weak + 1)); weak_names+=("$name")
		;;
	*)
		echo "SURVIVED  <-- the tests did not notice this defect"
		survived=$((survived + 1)); survived_names+=("$name")
		;;
	esac

	restore_all
	# Verify the revert is byte-identical; a failed revert would corrupt the
	# tree and poison every later mutation.
	got="$(sha256sum "$file" | awk '{print $1}')"
	if [ "$got" != "${ORIGINAL_HASH[$file]}" ]; then
		echo "      FATAL: revert of $file is not byte-identical (want ${ORIGINAL_HASH[$file]}, got $got)" >&2
		exit 1
	fi
done

echo
echo "mutation-check: $caught caught, $survived survived, $weak weak, $broken broken anchor(s)"
if [ "${#survived_names[@]}" -gt 0 ]; then
	echo "  NOT CAUGHT (the suite is vacuous for these):"
	for n in "${survived_names[@]}"; do echo "    - $n"; done
fi
if [ "${#broken_names[@]}" -gt 0 ]; then
	echo "  BROKEN ANCHORS (script needs updating, implementation changed):"
	for n in "${broken_names[@]}"; do echo "    - $n"; done
fi
if [ "${#weak_names[@]}" -gt 0 ]; then
	echo "  WEAK (did not fail an assertion, so they prove nothing):"
	for n in "${weak_names[@]}"; do echo "    - $n"; done
fi

if [ "$survived" -gt 0 ] || [ "$broken" -gt 0 ] || [ "$weak" -gt 0 ]; then
	echo "mutation-check: FAILED"
	exit 1
fi
echo "mutation-check: PASSED — every mutation was caught; the suite is non-vacuous."
exit 0
