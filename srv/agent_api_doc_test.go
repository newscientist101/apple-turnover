package srv

// This file keeps AGENT_API.md honest. The document is the contract an external
// agent is told to code against, so the failure mode that matters is not a
// missing sentence but a WRONG one: an agent that reads a field name this file
// got wrong, or waits for a frame kind that is never sent, fails at runtime with
// no compiler to catch it.
//
// Three properties are checked, and the first two are checked in BOTH
// directions, because a one-directional check is satisfied by a document that
// lists nothing at all:
//
//  1. TestAgentAPIDocCoversEveryEventKind parses the event vocabulary out of
//     event.go — the source of truth, so a newly added kind is picked up with no
//     edit to this file — and requires every kind to appear in the doc, and every
//     kind the doc names to exist in the source.
//
//  2. TestAgentAPIDocPayloadShapesMatchARunningServer compares the JSON shapes
//     printed in the doc against a RUNNING server, not against the source. That
//     distinction is the point: it catches a field renamed, added or dropped
//     anywhere between the struct and the wire, which a source-reading check
//     would happily agree with.
//
//  3. TestAgentAPIDocErrorTableMatchesTheServer and
//     TestAgentAPIDocDocumentsTheLoop hold the parts of the contract that are
//     wording rather than JSON: the error messages an agent is told to expect,
//     and the guidance it codes its loop from.
//
// None of these may pass vacuously, so each FAILS when it cannot find what it
// is looking for: a missing AGENT_API.md, an unparseable event.go, a missing
// shape anchor, or a shape block that is not valid JSON are all failures, never
// skips. A contract check that quietly stops running is the exact outcome this
// file exists to prevent.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// agentAPIPath is the document under test, relative to this package.
var agentAPIPath = filepath.Join("..", "AGENT_API.md")

// readAgentAPI reads the contract. It fails rather than skips: it is a shipped
// document, and a check that quietly stops running when the file moves is worse
// than no check at all.
func readAgentAPI(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(agentAPIPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", agentAPIPath, err)
	}
	if len(src) == 0 {
		t.Fatalf("%s is empty; there is no contract to check", agentAPIPath)
	}
	return string(src)
}

// eventKindRe matches one `Event<Name> = "<kind>"` constant declaration.
var eventKindRe = regexp.MustCompile(`(?m)^\s*Event\w+\s*=\s*"([^"]+)"`)

// parseEventKinds extracts the listener event vocabulary from event.go.
//
// event.go is parsed rather than a Go list duplicated here, because a duplicated
// list is a second thing to keep in sync — exactly the drift this file exists to
// catch. If the constants ever stop being plain string literals the regexp finds
// nothing, and the caller fails on the empty result rather than reporting a pass.
func parseEventKinds(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("event.go")
	if err != nil {
		t.Fatalf("read event.go: %v", err)
	}
	var kinds []string
	for _, m := range eventKindRe.FindAllStringSubmatch(string(src), -1) {
		kinds = append(kinds, m[1])
	}
	if len(kinds) == 0 {
		t.Fatal("parsed no Event* kinds out of event.go; the regexp no longer matches how the vocabulary is declared")
	}
	sort.Strings(kinds)
	return kinds
}

// frameTableRowRe matches one row of the doc's frame-kind table: a table row
// whose FIRST cell is a backticked kind. Reading only the first cell is what
// keeps the "sent when" column, and prose elsewhere in the file, from being
// mistaken for a declaration of a kind.
var frameTableRowRe = regexp.MustCompile("(?m)^\\|\\s*`([a-z-]+)`\\s*\\|")

// documentedFrameKinds returns the kinds named in the doc's frame-kind table.
//
// It reads only the rows AFTER the `<!-- frame-kinds -->` anchor, and stops at
// the first row that is not one. That scope is load-bearing: the document has
// several tables, and the snapshot's field table (`| \`version\` | ... |`) is
// written in exactly the same markdown shape. An unscoped row scan therefore
// reports `version`, `code` and `anchor` as frame kinds the server never sends —
// a check that cries wolf on the document's own field table trains the next agent
// to ignore it, which is worse than no check.
func documentedFrameKinds(t *testing.T, doc string) []string {
	t.Helper()
	const anchor = "<!-- frame-kinds -->"
	at := strings.Index(doc, anchor)
	if at < 0 {
		t.Fatalf("AGENT_API.md has no %q anchor, so the frame-kind table cannot be located unambiguously", anchor)
	}
	rows := frameTableRowRe.FindAllStringSubmatch(doc[at+len(anchor):], -1)
	if len(rows) == 0 {
		t.Fatal("the frame-kind table anchored in AGENT_API.md has no rows; the vocabulary check would pass on nothing")
	}
	var kinds []string
	for _, m := range rows {
		kinds = append(kinds, m[1])
	}
	sort.Strings(kinds)
	return kinds
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// wsCollapseRe matches any run of whitespace, for reasons below.
var wsCollapseRe = regexp.MustCompile(`\s+`)

// docStates reports whether the doc says something, comparing with whitespace
// collapsed. A needle is prose, and prose in markdown gets rewrapped: a check
// that fails when a line is rewrapped reports a formatting change as a missing
// guarantee, which trains the next agent to ignore it. This is the same defect
// that broke the `{...}` placeholder, one level up.
func docStates(doc, needle string) bool {
	collapse := func(s string) string {
		return strings.TrimSpace(wsCollapseRe.ReplaceAllString(s, " "))
	}
	return strings.Contains(collapse(doc), collapse(needle))
}

// ellipsisObjectRe matches the `{...}` placeholder used to stand in for a nested
// object in a documented shape. Whitespace inside the braces is not significant.
var ellipsisObjectRe = regexp.MustCompile(`\{\s*\.\.\.\s*\}`)

// docShape returns the JSON object printed in the doc under the given
// `<!-- shape:NAME -->` anchor.
//
// The anchor exists so this cannot silently bind to the wrong block: the document
// prints several ```json fences, and picking the wrong one would turn the check
// into a coin flip. A missing anchor is a FAILURE, never a skip — a shape the doc
// no longer prints is a shape nothing is checking.
func docShape(t *testing.T, doc, name string) map[string]json.RawMessage {
	t.Helper()
	anchor := "<!-- shape:" + name + " -->"
	at := strings.Index(doc, anchor)
	if at < 0 {
		t.Fatalf("AGENT_API.md has no %q anchor; that documented payload shape is no longer machine-checkable", anchor)
	}
	rest := doc[at+len(anchor):]
	open := strings.Index(rest, "```json")
	if open < 0 {
		t.Fatalf("%s is not followed by a ```json block", anchor)
	}
	rest = rest[open+len("```json"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Fatalf("the ```json block after %s is never closed", anchor)
	}
	block := strings.TrimSpace(rest[:end])

	// The frame envelope documents its snapshot as the placeholder `{...}`.
	// That is prose standing in for the nested object rather than JSON, so it is
	// replaced with an empty object and the TOP-LEVEL keys are read. The nested
	// snapshot is verified separately against a real frame, so nothing is lost.
	//
	// The match is a REGEXP rather than a literal because the placeholder is
	// prose: `{...}`, `{ ... }` and `{  ...  }` are the same placeholder, and a
	// literal replace stops matching the moment someone reformats it — which
	// failed as "not a JSON object", blaming the JSON for a whitespace change.
	block = ellipsisObjectRe.ReplaceAllString(block, "{}")

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(block), &obj); err != nil {
		t.Fatalf("the shape documented at %s is not a JSON object (%v):\n%s", anchor, err, block)
	}
	if len(obj) == 0 {
		t.Fatalf("the shape documented at %s parsed to zero fields:\n%s", anchor, block)
	}
	return obj
}

// TestDocShapeToleratesWhitespace is the regression test for the brittleness
// that made the frame shape unparseable: docShape used to replace the literal
// string "{ ... }", so a doc printing the same placeholder as "{...}" failed with
// "not a JSON object" — blaming the JSON for a whitespace change, and taking the
// vocabulary check down with it. checkInlineShape had the same defect for the
// same reason, matching a literal `"anchor": {`.
func TestDocShapeToleratesWhitespace(t *testing.T) {
	for _, placeholder := range []string{"{...}", "{ ... }", "{  ...  }"} {
		doc := "<!-- shape:frame -->\n```json\n{\"kind\":\"<what happened>\",\"snapshot\":" + placeholder + "}\n```\n"
		got := docShape(t, doc, "frame")
		if len(got) != 2 || !contains(sortedKeys(got), "snapshot") {
			t.Errorf("placeholder %q: docShape read %v, want the frame's two top-level fields", placeholder, sortedKeys(got))
		}
	}

	// The inline nested-object lookup must not depend on a space after the colon.
	// checkInlineShape is driven directly with a synthetic doc so the assertion is
	// the real helper's, not a copy of its matcher.
	live := []byte("{\"anchor\":{\"epochMs\":1757000000000,\"cps\":0.5}}")
	for _, anchorField := range []string{`"anchor": {`, `"anchor":{`, `"anchor"  :  {`} {
		doc := "```json\n{" + anchorField + "\"epochMs\":1757000000000,\"cps\":0.5}\n```\n"
		if err := checkInlineShape(t, "synthetic anchor", live, "anchor", doc); err != nil {
			t.Errorf("inline lookup with %q: %v", anchorField, err)
		}
	}
	// It must still bind to the wrong place loudly: a doc that omits the field is
	// an unchecked shape, not a pass.
	if err := checkInlineShape(t, "synthetic anchor", live, "anchor", "```json\n{}\n```\n"); err == nil {
		t.Error("inline lookup passed with no documented anchor; a nested shape nothing checks would read as verified")
	}

	// docStates must tolerate rewrapping but NOT tolerate a removed sentence,
	// which is the whole point of tolerating whitespace.
	if !docStates("one two\nthree", "one two three") {
		t.Error("docStates reported a rewrapped sentence as absent")
	}
	if docStates("one two three", "one two four") {
		t.Error("docStates reported absent wording as present")
	}
}

// sortedKeys returns a JSON object's field names, sorted, for comparison.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// keysOf returns the top-level field names of a live JSON object, sorted.
func keysOf(t *testing.T, raw []byte) []string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("value is not a JSON object: %v (%q)", err, bodyOf(string(raw)))
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sameFields reports whether two field-name sets are identical, and names the
// difference when they are not — a bare "fields differ" would send the next
// agent back to the document to diff the two by hand.
func sameFields(what string, got, want []string) error {
	if len(got) == len(want) {
		same := true
		for i := range got {
			if got[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	var missing, extra []string
	for _, w := range want {
		if !contains(got, w) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !contains(want, g) {
			extra = append(extra, g)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: the live server sends %v, AGENT_API.md documents %v", what, got, want)
	if len(missing) > 0 {
		fmt.Fprintf(&b, "; documented but never sent: %v", missing)
	}
	if len(extra) > 0 {
		fmt.Fprintf(&b, "; sent but not documented: %v", extra)
	}
	return fmt.Errorf("%s", b.String())
}

// nestedRaw returns the raw JSON of a top-level field, or nil if absent.
func nestedRaw(t *testing.T, raw []byte, field string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("body is not a JSON object: %v (%q)", err, bodyOf(string(raw)))
	}
	return obj[field]
}

// TestAgentAPIDocCoversEveryEventKind holds the frame vocabulary in the doc and
// the source to the same set, in both directions.
//
// The doc is where a listener author looks to answer "what can arrive on this
// socket?". A kind that exists but is undocumented is a client with no case for
// it; a kind that is documented but does not exist is a client waiting for a
// frame that never comes, which is a hang shaped like a feature.
func TestAgentAPIDocCoversEveryEventKind(t *testing.T) {
	doc := readAgentAPI(t)
	kinds := parseEventKinds(t)

	// Direction 1: every kind the server can send is documented.
	for _, kind := range kinds {
		if !strings.Contains(doc, "`"+kind+"`") {
			t.Errorf("frame kind %q is declared in event.go but is not named in AGENT_API.md: a listener author coding against the doc would have no case for it", kind)
		}
	}

	// Direction 2: every kind the doc's own table names is real. This is what
	// stops the document accumulating plausible-sounding kinds nothing sends.
	documented := documentedFrameKinds(t, doc)
	if len(documented) == 0 {
		t.Fatal("found no frame-kind table in AGENT_API.md; the vocabulary check would pass on an empty document")
	}
	for _, kind := range documented {
		if !contains(kinds, kind) {
			t.Errorf("AGENT_API.md documents the frame kind %q, but event.go declares no such kind (it has %v): a client written from the doc would wait for a frame that is never sent", kind, kinds)
		}
	}

	// The rules that make the listener-count frame safe to consume are prose, so
	// nothing else keeps them present. The BEHAVIOUR has mutation coverage in the
	// hub and ws slices; this asserts the document still tells a client about it,
	// which no behavioural test can do.
	for _, must := range []struct{ what, needle string }{
		{"that a count frame never bumps the version", "never bumps"},
		{"that the listener which caused a count change is not sent it", "does not receive its own count event"},
		{"that a listener subscribes before it is snapshotted", "subscribed before its initial snapshot"},
		{"that the connect snapshot already counts the listener itself", "includes itself"},
	} {
		if !docStates(doc, must.needle) {
			t.Errorf("AGENT_API.md no longer states %s (looked for %q): a client needs this to consume listener-count frames correctly", must.what, must.needle)
		}
	}
}

// TestAgentAPIDocPayloadShapesMatchARunningServer compares every payload shape in
// AGENT_API.md against a live server.
//
// This is the check the route-table test cannot substitute for. That one compares
// the doc to routes() and the event-kind test compares it to event.go — both read
// SOURCE. A field renamed on the wire (a json tag changed, a field dropped from a
// struct, an omitempty that starts omitting) leaves every source-shaped check
// perfectly happy while every agent reading the doc breaks. So this one drives
// the real handler tree over a real loopback socket and compares live bytes.
//
// Every request is bounded three ways (request context, transport response
// timeout, client timeout) by the shared fanout helpers, and the WebSocket reads
// use the package's bounded helpers, so a wedged handler fails this test rather
// than hanging it.
func TestAgentAPIDocPayloadShapesMatchARunningServer(t *testing.T) {
	doc := readAgentAPI(t)

	// Every shape the doc is supposed to publish, read up front so a document
	// that lost one fails with a clear message instead of inside a comparison.
	wantError := docShape(t, doc, "error")
	wantCodeReq := docShape(t, doc, "codeRequest")
	wantMessageReq := docShape(t, doc, "messageRequest")
	wantEvalReq := docShape(t, doc, "evalResultRequest")
	wantEvalAck := docShape(t, doc, "evalAck")
	wantAnchorReq := docShape(t, doc, "anchorRequest")
	wantSnapshot := docShape(t, doc, "snapshot")
	wantStoredEval := docShape(t, doc, "storedEvalResult")
	wantFrame := docShape(t, doc, "frame")
	wantDryRunReq := docShape(t, doc, "dryRunRequest")
	wantDryRunVerdict := docShape(t, doc, "dryRunVerdict")
	wantDryRunResultReq := docShape(t, doc, "dryRunResultRequest")
	wantDryRunFrame := docShape(t, doc, "dryRunFrame")

	_, base, wsBase := fanoutServer(t)

	// ---- the documented snapshot, live ----
	code, raw := fanoutGet(t, base, "/api/state")
	if code != http.StatusOK {
		t.Fatalf("GET /api/state: %d %q", code, bodyOf(raw))
	}
	if err := sameFields("GET /api/state", keysOf(t, []byte(raw)), sortedKeys(wantSnapshot)); err != nil {
		t.Error(err)
	}
	// The nested anchor is documented inline, so check it rather than trusting
	// that a matching top level implies a matching nested object.
	if err := checkInlineShape(t, "GET /api/state .anchor", []byte(raw), "anchor", doc); err != nil {
		t.Error(err)
	}

	// ---- the frame envelope, live ----
	conn, _, err := websocket.Dial(context.Background(), wsBase, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsBase, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var live Snapshot
	if err := json.Unmarshal([]byte(raw), &live); err != nil {
		t.Fatalf("decode live state: %v (%q)", err, bodyOf(raw))
	}
	ev := wsReadConnectSnapshot(t, conn, live)
	frameJSON, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("re-encode connect frame: %v", err)
	}
	if err := sameFields("/ws frame", keysOf(t, frameJSON), sortedKeys(wantFrame)); err != nil {
		t.Error(err)
	}
	// The doc promises the snapshot inside a frame is the SAME object
	// /api/state returns, produced by one encoder. Compare the live frame's
	// snapshot to the documented snapshot fields, so the two documented shapes
	// cannot drift apart either.
	snapIn := nestedRaw(t, frameJSON, "snapshot")
	if len(snapIn) == 0 {
		t.Fatalf("/ws frame has no snapshot field: %q", bodyOf(string(frameJSON)))
	}
	if err := sameFields("/ws frame .snapshot", keysOf(t, snapIn), sortedKeys(wantSnapshot)); err != nil {
		t.Error(err)
	}

	// ---- request bodies, in both directions ----
	// A doc that under-claims is as broken as one that over-claims, so each
	// request shape is pinned exactly: every documented field is accepted, and
	// one field the doc does not mention is rejected.
	pushCode(t, base, `{"code":"s(\"bd*2\")"}`)
	if err := checkRequestShape(t, base, "/api/code", "codeRequest", wantCodeReq,
		map[string]any{"code": `s("bd*4")`, "message": "shape check"}); err != nil {
		t.Error(err)
	}
	if err := checkRequestShape(t, base, "/api/message", "messageRequest", wantMessageReq,
		map[string]any{"message": "shape check"}); err != nil {
		t.Error(err)
	}
	// epochMs is "now" rather than a constant because the server REFUSES a
	// skewed anchor, and that refusal is the documented behaviour: a pinned
	// literal here would make this check pass only by being refused, which
	// would prove the request shape nothing.
	if err := checkRequestShape(t, base, "/api/anchor", "anchorRequest", wantAnchorReq,
		map[string]any{"epochMs": time.Now().UnixMilli(), "cps": 0.5}); err != nil {
		t.Error(err)
	}
	// samplesResolved is supplied here rather than left out of the sample map: the
	// check refuses a documented field it has no value for, and the value has to
	// be a REAL one, because this is the field whose whole point is that `false`
	// and absent mean different things. Sending `false` proves the server accepts
	// and stores the negative case, which `true` alone would not.
	if err := checkRequestShape(t, base, "/api/eval-result", "evalResultRequest", wantEvalReq,
		map[string]any{"version": latestVersion(t, base), "ok": true, "error": "", "stats": map[string]any{"haps": 1}, "samplesResolved": false}); err != nil {
		t.Error(err)
	}

	// ---- the eval ack, live ----
	code, raw = fanoutPost(t, base, "/api/eval-result", fmt.Sprintf(`{"version":%d,"ok":true}`, latestVersion(t, base)))
	if code != http.StatusOK {
		t.Fatalf("POST /api/eval-result: %d %q", code, bodyOf(raw))
	}
	if err := sameFields("POST /api/eval-result response", keysOf(t, []byte(raw)), sortedKeys(wantEvalAck)); err != nil {
		t.Error(err)
	}

	// ---- the stored verdict, live ----
	// Pushed with every optional field present, because the documented stored
	// shape shows them all: a verdict that omitted `error` would prove nothing
	// about whether that field is real. `samplesResolved` is present for the same
	// reason, and set to `false` so the check cannot pass on a value the server
	// silently defaulted — a stored verdict that always echoed `true` would satisfy
	// a field-set comparison while defeating the field's entire purpose.
	code, raw = fanoutPost(t, base, "/api/eval-result",
		fmt.Sprintf(`{"version":%d,"ok":false,"error":"x is not a function","stats":{"haps":0},"samplesResolved":false}`, latestVersion(t, base)))
	if code != http.StatusOK {
		t.Fatalf("POST /api/eval-result (failing verdict): %d %q", code, bodyOf(raw))
	}
	_, raw = fanoutGet(t, base, "/api/state")
	stored := nestedRaw(t, []byte(raw), "lastEvalResult")
	if len(stored) == 0 || string(stored) == "null" {
		t.Fatalf("GET /api/state.lastEvalResult is null after a stored failing verdict: %q", bodyOf(raw))
	}
	if err := sameFields("GET /api/state .lastEvalResult", keysOf(t, stored), sortedKeys(wantStoredEval)); err != nil {
		t.Error(err)
	}

	// ---- the dry-run contract, live ----
	// The listener connected above is a real evaluator, so a dry-run can be
	// driven end to end: this test issues the request, reads the frame the
	// browser is asked to evaluate, and reports the verdict itself — exactly what
	// session.js does.
	verdictFields, dryRunID := fanoutDryRun(t, base, `s("bd*4")`, conn, wantDryRunReq, wantDryRunFrame, "Unexpected token '}'")
	if err := sameFields("POST /api/dry-run response", sortedKeys(verdictFields), sortedKeys(wantDryRunVerdict)); err != nil {
		t.Error(err)
	}

	// The dry-run REPORT body is pinned against the documented shape by decoding the
	// exact body this test sends and comparing field sets: the request is
	// rejected at DECODING time, before the registry is consulted, so an extra
	// field cannot disturb the round trip above.
	dryRunResultReq := map[string]json.RawMessage{
		"dryRunId": json.RawMessage("1"),
		"ok":       json.RawMessage("true"),
		"stats":    json.RawMessage(`{"haps":2}`),
		// The dry-run report carries the same tri-state as the stored verdict, and
		// it is pinned here for the same reason: the two endpoints are the two ways
		// an agent asks whether code evaluates, so a field documented on one and
		// refused by the other would break exactly the pre-check the dry-run exists
		// for. `false` again, so a server that dropped the value could not pass on
		// field names alone.
		"samplesResolved": json.RawMessage("false"),
	}
	code, raw = fanoutPost(t, base, "/api/dry-run-result",
		fmt.Sprintf(`{"dryRunId":%d,"ok":true,"fieldTheContractDoesNotDocument":"x"}`, dryRunID))
	if code != http.StatusBadRequest || !strings.Contains(raw, "unknown field") {
		t.Errorf("an undocumented field on /api/dry-run-result returned %d %q, want 400 naming an unknown field", code, bodyOf(raw))
	}
	if err := sameFields("POST /api/dry-run-result request", sortedKeys(dryRunResultReq), sortedKeys(wantDryRunResultReq)); err != nil {
		t.Error(err)
	}

	// The error shape, live
	// From a real rejected request, so this reads a failure the server actually
	// produced rather than a hand-written one: the shape is what an agent
	// branches on.
	code, raw = fanoutPost(t, base, "/api/code", `{"code":"   "}`)
	if code != http.StatusBadRequest {
		t.Fatalf("POST /api/code with a blank code: %d %q, want 400 so the error shape is exercised", code, bodyOf(raw))
	}
	if err := sameFields("/api error body", keysOf(t, []byte(raw)), sortedKeys(wantError)); err != nil {
		t.Error(err)
	}
}

// fanoutPostQuiet is fanoutPost without t.Fatalf, for use from a goroutine.
// FailNow from off the test's own goroutine is not a failure report, it is a
// panic that kills the run, so a helper that blocks (a dry-run) needs a caller
// that hands the error back instead.
func fanoutPostQuiet(base, path, body string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Transport: boundedTransport(), Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(raw), nil
}

// wsReadEventOfKind reads frames until one of wantKind arrives, bounded.
//
// It skips rather than insists on the very next frame, because a listener that
// has just published code has several frames legitimately queued behind the one
// this test cares about. The bound is what keeps a missing frame a failure.
func wsReadEventOfKind(t *testing.T, conn *websocket.Conn, wantKind string) Event {
	t.Helper()
	deadline := time.Now().Add(wsReadTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		typ, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("no %q frame within %s: %v", wantKind, wsReadTimeout, err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("%q frame is not a JSON Event: %v (%q)", wantKind, err, data)
		}
		if ev.Kind == wantKind {
			return ev
		}
	}
	t.Fatalf("no %q frame within %s", wantKind, wsReadTimeout)
	return Event{}
}

// fanoutDryRun performs one complete dry-run round trip against a live server
// with conn acting as the browser, and returns the verdict's top-level fields.
//
// It exists because a dry-run BLOCKS until a browser answers, so it cannot be
// driven by a helper that posts and returns: the request has to be in flight
// while the frame is read, and the answer has to come from the same listener the
// frame went to. Doing that here keeps the sequence in one place instead of
// spreading a goroutine, a frame read and a reply across every caller.
//
// It also pins the two dry-run WIRE shapes on the way through — the frame and
// the report — because a caller that only compared the verdict would let those
// drift unchecked, and they are the parts an external listener author codes
// against.
func fanoutDryRun(t *testing.T, base, code string, conn *websocket.Conn, wantReq, wantFrame map[string]json.RawMessage, failWith string) (map[string]json.RawMessage, int64) {
	t.Helper()

	type reply struct {
		code int
		raw  string
		err  error
	}
	done := make(chan reply, 1)
	go func() {
		c, raw, err := fanoutPostQuiet(base, "/api/dry-run", fmt.Sprintf(`{"code":%q}`, code))
		done <- reply{c, raw, err}
	}()

	// The browser's half: read the frame, then answer it.
	ev := wsReadEventOfKind(t, conn, EventDryRun)
	if ev.DryRun == nil {
		t.Fatal("the dry-run frame carried no dryRun member, so a listener has nothing to evaluate")
	}
	if ev.DryRun.Code != code {
		t.Errorf("the frame asked to evaluate %q, want %q", ev.DryRun.Code, code)
	}
	// The frame envelope: the two documented fields plus the optional candidate.
	// This is the shape a listener author codes against, so it is compared rather
	// than assumed -- and the dryRun member is checked nested, because a frame
	// carrying a differently-shaped candidate is a different wire contract.
	frameJSON, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("re-encode dry-run frame: %v", err)
	}
	if err := sameFields("/ws dry-run frame", keysOf(t, frameJSON), sortedKeys(wantFrame)); err != nil {
		t.Error(err)
	}
	if err := checkInlineShape(t, "/ws dry-run frame .dryRun", frameJSON, "dryRun", readAgentAPI(t)); err != nil {
		t.Error(err)
	}
	// The frame envelope is checked here rather than by the caller so every
	// dry-run exercises it: two documented fields plus the optional candidate.
	if ev.DryRun.ID <= 0 {
		t.Errorf("the dry-run frame carries id %d; a listener cannot correlate a report without it", ev.DryRun.ID)
	}

	// The report is a FAILING verdict on purpose. The documented dryRunVerdict
	// shows every field including `error`, and `error` is omitempty — so a
	// successful verdict would legitimately omit it and the documented shape
	// would look wrong. Driving the failure is what makes "every documented
	// field is really sent" checkable rather than half-checkable.
	report := fmt.Sprintf(`{"dryRunId":%d,"ok":false,"error":%s,"stats":{"haps":0}}`,
		ev.DryRun.ID, strconv.Quote(failWith))
	code2, raw2 := fanoutPost(t, base, "/api/dry-run-result", report)
	if code2 != http.StatusOK {
		t.Fatalf("POST /api/dry-run-result: %d %q", code2, bodyOf(raw2))
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("POST /api/dry-run: %v", res.err)
	}
	if res.code != http.StatusOK {
		t.Fatalf("POST /api/dry-run: %d %q", res.code, bodyOf(res.raw))
	}

	// The documented REQUEST shape is checked in both directions. The
	// documented-field half is the round trip above; this is the undocumented-field
	// half, refused at DECODING time so it cannot disturb anything.
	sent := map[string]json.RawMessage{"code": json.RawMessage(strconv.Quote(code))}
	if err := sameFields("POST /api/dry-run request", sortedKeys(sent), sortedKeys(wantReq)); err != nil {
		t.Error(err)
	}
	probeCode, probeRaw := fanoutPost(t, base, "/api/dry-run",
		fmt.Sprintf(`{"code":%q,"fieldTheContractDoesNotDocument":"x"}`, code))
	if probeCode != http.StatusBadRequest || !strings.Contains(probeRaw, "unknown field") {
		t.Errorf("an undocumented field on /api/dry-run returned %d %q, want 400 naming an unknown field", probeCode, bodyOf(probeRaw))
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.raw), &fields); err != nil {
		t.Fatalf("the dry-run response is not a JSON object: %v (%q)", err, bodyOf(res.raw))
	}
	return fields, ev.DryRun.ID
}

// checkRequestShape pins one documented request shape against the live server in
// both directions: a body with exactly the documented fields is accepted, and the
// same body plus one undocumented field is rejected.
//
// Acceptance alone would not notice a field the server accepts but the doc never
// mentions; rejection alone would not notice a documented field the server
// refuses. Together they pin the shape exactly.
func checkRequestShape(t *testing.T, base, path, name string, want map[string]json.RawMessage, values map[string]any) error {
	t.Helper()

	body := map[string]any{}
	for _, field := range sortedKeys(want) {
		v, ok := values[field]
		if !ok {
			return fmt.Errorf("%s request: AGENT_API.md documents a field %q that this check has no valid value for; add one so the shape stays verified", name, field)
		}
		body[field] = v
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%s request: build body: %v", name, err)
	}
	code, raw := fanoutPost(t, base, path, string(encoded))
	if code != http.StatusOK {
		// A rejection here means the doc documents a field the live server will
		// not accept from a caller: the worst possible kind of wrong.
		return fmt.Errorf("%s request: POST %s with exactly the documented fields %v was REJECTED %d %q: AGENT_API.md documents a shape the server does not accept", name, path, sortedKeys(want), code, bodyOf(raw))
	}

	body["fieldTheContractDoesNotDocument"] = "x"
	encoded, err = json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%s request: build probe body: %v", name, err)
	}
	code, raw = fanoutPost(t, base, path, string(encoded))
	if code != http.StatusBadRequest {
		return fmt.Errorf("%s request: POST %s with an UNDOCUMENTED field returned %d %q; the server accepts fields AGENT_API.md does not list, so the documented shape is incomplete", name, path, code, bodyOf(raw))
	}
	if !strings.Contains(raw, "unknown field") {
		return fmt.Errorf("%s request: the undocumented field was rejected %d but not as an unknown field: %q", name, code, bodyOf(raw))
	}
	return nil
}

// checkInlineShape compares a nested object's live fields against the fields the
// doc prints for it INLINE in the parent block (currently `anchor`), so the
// nested shape is read from the document rather than duplicated here.
func checkInlineShape(t *testing.T, what string, raw []byte, field, doc string) error {
	t.Helper()
	inner := nestedRaw(t, raw, field)
	if len(inner) == 0 {
		return fmt.Errorf("%s is missing from the live body: %q", what, bodyOf(string(raw)))
	}
	re := regexp.MustCompile(`"` + field + `"\s*:\s*{`)
	at := re.FindStringIndex(doc)
	if at == nil {
		return fmt.Errorf("AGENT_API.md no longer prints %q inline in the documented snapshot, so its fields are unchecked", field)
	}
	rest := doc[at[1]:]
	// These nested objects are flat, so the first '}' closes them.
	end := strings.Index(rest, "}")
	if end < 0 {
		return fmt.Errorf("AGENT_API.md prints %q inline but never closes it", field)
	}
	var want map[string]json.RawMessage
	if err := json.Unmarshal([]byte("{"+rest[:end]+"}"), &want); err != nil {
		return fmt.Errorf("the %q object documented inline is not JSON (%v): %q", field, err, rest[:end])
	}
	return sameFields(what, keysOf(t, inner), sortedKeys(want))
}

// pushCode pushes a code document and fails unless it is accepted.
func pushCode(t *testing.T, base, body string) {
	t.Helper()
	code, raw := fanoutPost(t, base, "/api/code", body)
	if code != http.StatusOK {
		t.Fatalf("POST /api/code %s: %d %q", bodyOf(body), code, bodyOf(raw))
	}
}

// latestVersion reads the live version, so a verdict naming one is naming a
// version that was really published: a verdict for an unpublished version is a
// 400, which would fail the shape check for entirely the wrong reason.
func latestVersion(t *testing.T, base string) int64 {
	t.Helper()
	code, raw := fanoutGet(t, base, "/api/state")
	if code != http.StatusOK {
		t.Fatalf("GET /api/state: %d %q", code, bodyOf(raw))
	}
	var snap struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("decode state: %v (%q)", err, bodyOf(raw))
	}
	return snap.Version
}

// TestAgentAPIDocErrorTableMatchesTheServer spot-checks the error messages the doc
// tabulates, because a message reworded in the code but not here is exactly the
// drift a contract must not have: an agent matching on the documented string
// would break.
//
// It is deliberately a spot-check of live MESSAGES rather than of the whole
// table. Exhaustiveness is a judgement about which failures an agent will hit,
// and inventing a machine format for "every error the server can produce" would
// make any change to any error string a change to the doc format. What must not
// drift is the wording an agent is told to expect, and that is checked here
// against the real server.
func TestAgentAPIDocErrorTableMatchesTheServer(t *testing.T) {
	doc := readAgentAPI(t)
	_, base, _ := fanoutServer(t)

	for _, tc := range []struct {
		what   string
		method string
		path   string
		body   string
		want   int
		needle string
	}{
		{"blank code", http.MethodPost, "/api/code", `{"code":"   "}`, http.StatusBadRequest,
			"code must not be empty"},
		{"blank message", http.MethodPost, "/api/message", `{"message":""}`, http.StatusBadRequest,
			"message must not be empty"},
		{"unknown field", http.MethodPost, "/api/code", `{"code":"s(1)","messge":"typo"}`, http.StatusBadRequest,
			"unknown field"},
		{"trailing JSON", http.MethodPost, "/api/code", `{"code":"s(1)"}{"code":"s(2)"}`, http.StatusBadRequest,
			"unexpected data after JSON body"},
		{"body on a no-argument endpoint", http.MethodPost, "/api/hush", `{"code":"s(1)"}`, http.StatusBadRequest,
			"this endpoint takes no arguments"},
		{"unpublished version", http.MethodPost, "/api/eval-result", `{"version":9999,"ok":true}`, http.StatusBadRequest,
			"unknown version:"},
		{"unregistered API path", http.MethodGet, "/api/nope", "", http.StatusNotFound,
			"no such API endpoint"},
	} {
		var (
			code int
			raw  string
		)
		if tc.method == http.MethodGet {
			code, raw = fanoutGet(t, base, tc.path)
		} else {
			code, raw = fanoutPost(t, base, tc.path, tc.body)
		}
		if code != tc.want {
			t.Errorf("%s: %s %s returned %d %q, want %d", tc.what, tc.method, tc.path, code, bodyOf(raw), tc.want)
			continue
		}
		if !strings.Contains(raw, tc.needle) {
			t.Errorf("%s: the live error is %q, which does not contain the documented %q — AGENT_API.md's error table has drifted from the server", tc.what, bodyOf(raw), tc.needle)
		}
		if !strings.Contains(doc, tc.needle) {
			t.Errorf("%s: the server says %q but AGENT_API.md does not document that wording", tc.what, tc.needle)
		}
	}

	// The payload cap, which the doc states as a number an agent may rely on.
	oversized := `{"code":"` + strings.Repeat("x", APIMaxBodyBytes+16) + `"`
	code, raw := fanoutPost(t, base, "/api/code", oversized)
	if code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: got %d %q, want 413", code, bodyOf(raw))
	}
	if !strings.Contains(raw, fmt.Sprintf("limit is %d bytes", APIMaxBodyBytes)) {
		t.Errorf("oversized body: the live error is %q, want it to name the documented limit of %d bytes", bodyOf(raw), APIMaxBodyBytes)
	}
}

// TestAgentAPIDocDocumentsTheLoop proves the parts of the contract that are
// advice rather than JSON are still present, because an agent that cannot find
// the loop or the read-back path will invent one.
//
// None of these is implied by a payload shape, and each is a mistake an external
// agent actually makes: trusting the ack instead of reading the verdict back,
// assuming the server ran the code it was sent, and expecting a frame for a
// request that changed nothing.
func TestAgentAPIDocDocumentsTheLoop(t *testing.T) {
	doc := readAgentAPI(t)
	for _, must := range []struct{ what, needle string }{
		{"that accepted:true does not mean the verdict was stored", "not necessarily stored"},
		{"that a stale verdict is accepted and discarded", "stale"},
		{"that the loop closes by reading back /api/state", "Read `GET /api/state` and inspect `lastEvalResult`"},
		{"that the server never evaluates JavaScript", "never evaluated"},
		{"that a rejected or ignored write broadcasts nothing", "| rejected | `400` / `405` / `413` | no | no |"},
		{"that POST /api/code bumps the version by one", "increments exactly once"},
		{"the 64 KiB body cap", "64 KiB"},
		{"the snapshot-on-connect catch-up", "immediate full snapshot"},
		// The coherence LIMITATION. These are prose, so nothing else keeps them
		// present, and an agent that overclaims here tells a listener two
		// machines are playing in lockstep when only the bar is shared.
		{"that strudel has no cross-machine sync", "cross-machine audio clock"},
		{"that the goal is bar alignment", "bar-aligned"},
		{"that sample-accurate sync is out of scope", "sample-accurate"},
		{"what a drifted listener should expect", "Re-anchoring is the recovery mechanism for drift"},
		{"that an unalignable listener says so rather than claiming a bar", "unscheduled"},
		// The agent-presence LIMITATION. An agent holding no connection is a
		// liveness lease, not a session, and an agent that reads it as anything
		// stronger will tell a listener it is still driving when it is not.
		{"that the agent holds no persistent connection", "no persistent connection"},
		{"that presence is a lease that decays", "The lease decays"},
		{"how long the lease lasts", "15 seconds"},
		{"that a client must not infer presence from its own clock", "must not infer presence from its own clock"},
		{"that liveness is not a session", "liveness, not a session"},
		// The frame rule, which is the reason a listener can rely on `agent`.
		{"that the agent frame fires only on a lapse", "held** lease lapsing"},
		{"that a heartbeat broadcasts nothing", "A heartbeat broadcasts nothing"},
		{"that presence never bumps the version", "agent-presence, and stored evaluation-result changes do not increment"},
	} {
		if !docStates(doc, must.needle) {
			t.Errorf("AGENT_API.md no longer states %s (looked for %q): this is the guidance an external agent codes its loop from", must.what, must.needle)
		}
	}
}

// TestAgentAPIDocShapesAreStable guards the anchors themselves: every shape the
// checks above consume must still be present and parseable, so removing one is a
// failure with a clear message rather than a check that quietly stops comparing.
//
// It is separate because the others report a missing anchor only when they happen
// to run first, and because the COUNT is itself worth asserting: adding a new
// documented shape should mean adding it here, which is what stops the set of
// checked shapes from quietly shrinking over a series of unrelated edits.
func TestAgentAPIDocShapesAreStable(t *testing.T) {
	doc := readAgentAPI(t)
	want := []string{
		"error", "codeRequest", "messageRequest", "evalResultRequest",
		"evalAck", "snapshot", "storedEvalResult", "frame", "anchorRequest",
		"dryRunRequest", "dryRunVerdict", "dryRunResultRequest", "dryRunFrame",
	}
	for _, name := range want {
		docShape(t, doc, name) // fails loudly if the anchor or its JSON is gone
	}
	found := regexp.MustCompile(`<!--\s*shape:([a-zA-Z]+)\s*-->`).FindAllStringSubmatch(doc, -1)
	if len(found) != len(want) {
		var names []string
		for _, m := range found {
			names = append(names, m[1])
		}
		sort.Strings(names)
		t.Errorf("AGENT_API.md has %d shape anchors %v, but this test knows about %d %v; add a new shape to TestAgentAPIDocShapesAreStable and to the check that consumes it", len(found), names, len(want), want)
	}
}
