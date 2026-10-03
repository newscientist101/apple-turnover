package main

// These tests drive the CLI the way the harness does — in process, against the
// REAL handler tree over loopback httptest — and assert on both the output and
// the exit code, because the exit code is half the contract here: a caller that
// cannot distinguish "the server said no" from "you called me wrong" cannot
// script against it.
//
// Three rules from AGENTS.md govern everything below:
//
//  1. No network, no real ports, no :8000. Every server is httptest on loopback.
//  2. Everything is bounded. httptest.Server.Close waits for outstanding
//     requests, so closing a server whose handler is wedged would hang the test;
//     every close therefore runs on a goroutine with its own deadline. Every
//     client call carries a context deadline AND per-transport timeouts.
//  3. Assert on BODIES. "exit 0" alone would pass against a CLI that printed
//     nothing at all.
//
// srv.New().Handler() is the real routes(), not a fake: a fake would agree with
// a CLI that had drifted from the contract, which is the one thing these tests
// exist to catch.

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"strudelagent/srv"
)

// testTimeout is the -timeout every test passes to run(). It is short so that a
// bounded-by-construction CLI cannot spend a minute proving it works.
const testTimeout = 5 * time.Second

// newTestServer boots the real handler tree on loopback and returns its base URL
// plus a cleanup that is BOUNDED.
//
// t.Cleanup runs after the test body, and httptest.Server.Close blocks until
// every in-flight request finishes. If a handler ever wedges, that turns a
// failing test into a hanging suite — so the close runs on its own goroutine and
// the test fails if it does not return. A parked goroutine after a timeout is
// acceptable here precisely because the test is already failing.
func newTestServer(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(srv.New().Handler())

	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			ts.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("httptest.Server.Close did not return within 5s; a handler is wedged")
		}
	})

	addr := ts.Listener.Addr().String()
	if strings.HasSuffix(addr, ":8000") {
		t.Fatalf("test server bound %s, the production port; tests must not collide with it", addr)
	}
	return "http://" + addr
}

// invocation is one CLI run's observable behaviour.
type invocation struct {
	code   int
	stdout string
	stderr string
}

// runCLI invokes run() with an explicit base URL and no inherited environment,
// so a developer's own STRUDEL_AGENT_URL cannot change a test's outcome.
func runCLI(t *testing.T, base, stdin string, args ...string) invocation {
	t.Helper()
	// A full -timeout on every call: the CLI's own boundedness is part of what
	// is under test, and a test that hangs proves nothing.
	full := append([]string{"-base", base, "-timeout", testTimeout.String()}, args...)
	var out, errBuf bytes.Buffer
	code := run(full, strings.NewReader(stdin), &out, &errBuf)
	return invocation{code: code, stdout: out.String(), stderr: errBuf.String()}
}

// TestStatePrintsTheWholeSnapshot covers the read path: version, document,
// playing flag, last agent message and listener count are all the caller needs
// before deciding what to push next, and every one is asserted on the BODY rather
// than on the exit code.
func TestStatePrintsTheWholeSnapshot(t *testing.T) {
	base := newTestServer(t)

	// Put the server into a non-default state first, so a CLI printing
	// hard-coded defaults cannot pass.
	if got := runCLI(t, base, `s("bd*2")`, "push", "-m", "four on the floor"); got.code != exitOK {
		t.Fatalf("setup push: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runCLI(t, base, "", "hush"); got.code != exitOK {
		t.Fatalf("setup hush: exit %d, stderr %q", got.code, got.stderr)
	}

	got := runCLI(t, base, "", "state")
	if got.code != exitOK {
		t.Fatalf("state: exit %d, want 0 (stderr %q)", got.code, got.stderr)
	}
	for _, want := range []string{
		"version:        1", // the document really was published
		`code:`,
		`s("bd*2")`, // the document itself
		"playing:        false",
		"last message:   four on the floor",
		"listeners:      0",
		"last eval:      none yet",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("state output missing %q\ngot:\n%s", want, got.stdout)
		}
	}
	// The anchor is echoed because clients derive their scheduler position from
	// it; a snapshot printed without it is not the documented snapshot.
	if !strings.Contains(got.stdout, "epochMs=") || !strings.Contains(got.stdout, "cps=0.5") {
		t.Errorf("state output missing the anchor\ngot:\n%s", got.stdout)
	}
}

// TestStateJSONModeEmitsTheSnapshotVerbatim proves -json is the machine-readable
// path: the output must parse as the snapshot object, so a script can pipe it
// into jq instead of scraping prose.
func TestStateJSONModeEmitsTheSnapshotVerbatim(t *testing.T) {
	base := newTestServer(t)
	runCLI(t, base, `s("cp*4")`, "push")

	got := runCLI(t, base, "", "-json", "state")
	if got.code != exitOK {
		t.Fatalf("state -json: exit %d, stderr %q", got.code, got.stderr)
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(got.stdout), &snap); err != nil {
		t.Fatalf("-json output is not decodable: %v\ngot: %q", err, got.stdout)
	}
	if snap.Version != 1 || snap.Code != `s("cp*4")` || !snap.Playing {
		t.Errorf("-json snapshot = %+v, want version 1, the pushed code, playing", snap)
	}
}

// TestPushReadsStdinAndFile covers both documented sources of the document, and
// asserts the NEW VERSION is reported — the value a caller must read back to
// close the loop, since it is what a later eval-result has to name.
func TestPushReadsStdinAndFile(t *testing.T) {
	base := newTestServer(t)

	fromStdin := runCLI(t, base, `s("bd*2, ~ cp")`, "push")
	if fromStdin.code != exitOK {
		t.Fatalf("push from stdin: exit %d, stderr %q", fromStdin.code, fromStdin.stderr)
	}
	if !strings.Contains(fromStdin.stdout, "pushed version 1") {
		t.Errorf("push from stdin = %q, want it to report version 1", fromStdin.stdout)
	}

	path := filepath.Join(t.TempDir(), "pattern.js")
	if err := os.WriteFile(path, []byte(`s("hh*8").slowcat("<x2 x3>")`), 0o600); err != nil {
		t.Fatalf("write temp pattern: %v", err)
	}
	fromFile := runCLI(t, base, "", "push", "-f", path)
	if fromFile.code != exitOK {
		t.Fatalf("push from file: exit %d, stderr %q", fromFile.code, fromFile.stderr)
	}
	if !strings.Contains(fromFile.stdout, "pushed version 2") {
		t.Errorf("push from file = %q, want it to report version 2", fromFile.stdout)
	}

	// Read the state back through the CLI, so the assertion is on what the
	// SERVER stored rather than on what the CLI claimed it sent.
	state := runCLI(t, base, "", "state")
	for _, want := range []string{"version:        2", `s("hh*8").slowcat("<x2 x3>")`} {
		if !strings.Contains(state.stdout, want) {
			t.Errorf("state after two pushes missing %q\ngot:\n%s", want, state.stdout)
		}
	}
}

// TestPushStdinDashIsStdin pins the "-" spelling of stdin, since it is the form
// a shell pipeline reaches for.
func TestPushStdinDashIsStdin(t *testing.T) {
	base := newTestServer(t)
	got := runCLI(t, base, `s("bd")`, "push", "-f", "-")
	if got.code != exitOK {
		t.Fatalf("push -f -: exit %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "pushed version 1") {
		t.Errorf("push -f - = %q, want it to report version 1", got.stdout)
	}
}

// TestMessageSetsNarrationWithoutBumpingTheVersion pins the one-to-one mapping
// onto POST /api/message: the words change, the version does not.
func TestMessageSetsNarrationWithoutBumpingTheVersion(t *testing.T) {
	base := newTestServer(t)
	runCLI(t, base, `s("bd")`, "push")

	got := runCLI(t, base, "", "message", "four", "on", "the", "floor")
	if got.code != exitOK {
		t.Fatalf("message: exit %d, stderr %q", got.code, got.stderr)
	}
	// The unquoted words are joined, so the narration lands intact.
	if !strings.Contains(got.stdout, "version 1") {
		t.Errorf("message = %q, want it to confirm the unchanged version 1", got.stdout)
	}

	state := runCLI(t, base, "", "state")
	if !strings.Contains(state.stdout, "last message:   four on the floor") {
		t.Errorf("state after message missing the narration\ngot:\n%s", state.stdout)
	}
	if !strings.Contains(state.stdout, "version:        1") {
		t.Errorf("message bumped the version; narration must not\ngot:\n%s", state.stdout)
	}
}

// TestRejectionIsReportedVerbatimAndFails is the core honesty property: the
// server's own rejection text reaches the user unchanged, and the exit code is
// non-zero. A CLI that swallowed the reason, or exited 0 on a 400, would let an
// agent believe a refused push had landed.
func TestRejectionIsReportedVerbatimAndFails(t *testing.T) {
	base := newTestServer(t)

	tests := []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{
			name:  "blank code",
			stdin: "   \n  ", // whitespace only: the server trims and rejects it
			args:  []string{"push"},
			want:  "code must not be empty", // the server's exact wording
		},
		{
			name: "empty message",
			args: []string{"message", "   "},
			want: "message must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLI(t, base, tc.stdin, tc.args...)
			if got.code == exitOK {
				t.Errorf("exit 0 on a rejected request; want non-zero\nstdout: %q", got.stdout)
			}
			if !strings.Contains(got.stderr, tc.want) {
				t.Errorf("stderr does not carry the server's verbatim message %q\nstderr: %q", tc.want, got.stderr)
			}
			// A rejection must not look like a success on stdout either.
			if strings.Contains(got.stdout, "pushed version") {
				t.Errorf("stdout claims a push succeeded despite a rejection: %q", got.stdout)
			}
		})
	}
}

// TestTransportReportsANoopAsANoop is the idempotence property. The server
// accepts a hush on an already-hushed performance and changes nothing; printing
// an unqualified success there would let a caller believe it had just stopped
// something that was already stopped.
//
// The state is read first, so the CLI can tell the two cases apart — and the
// report must be honest that it is a comparison, not a guarantee.
func TestTransportReportsANoopAsANoop(t *testing.T) {
	base := newTestServer(t)

	// A fresh server is already playing, so hush is a real change.
	first := runCLI(t, base, "", "hush")
	if first.code != exitOK {
		t.Fatalf("first hush: exit %d, stderr %q", first.code, first.stderr)
	}
	if !strings.Contains(first.stdout, "hushed") || strings.Contains(first.stdout, "no change") {
		t.Errorf("first hush = %q, want a plain change report", first.stdout)
	}

	// The second hush is accepted and ignored.
	second := runCLI(t, base, "", "hush")
	if second.code != exitOK {
		t.Errorf("second hush: exit %d, want 0 — the request WAS accepted", second.code)
	}
	if !strings.Contains(second.stdout, "no change") {
		t.Errorf("second hush = %q, want it reported as a no-op", second.stdout)
	}
	if !strings.Contains(second.stdout, "already hushed") {
		t.Errorf("second hush = %q, want it to say the performance was already hushed", second.stdout)
	}

	// And resuming from the hushed state is a genuine change again.
	resumed := runCLI(t, base, "", "play")
	if resumed.code != exitOK || !strings.Contains(resumed.stdout, "playing") ||
		strings.Contains(resumed.stdout, "no change") {
		t.Errorf("play after hush = %q (exit %d), want a plain change report", resumed.stdout, resumed.code)
	}
}

// TestTransportSendsNoBody pins the contract that /api/hush and /api/play take
// no arguments. A CLI that posted {"playing": false} would be rejected with a
// 400 for smuggling a field through an argument-free endpoint, so this is
// asserted against the real server rather than by inspecting the request.
func TestTransportSendsNoBody(t *testing.T) {
	base := newTestServer(t)
	// Both directions must succeed, which they only do with an empty body.
	if got := runCLI(t, base, "", "hush"); got.code != exitOK {
		t.Fatalf("hush: exit %d, stderr %q", got.code, got.stderr)
	}
	if got := runCLI(t, base, "", "play"); got.code != exitOK {
		t.Fatalf("play: exit %d, stderr %q", got.code, got.stderr)
	}
	// A stray argument is a usage error, caught before anything is sent.
	if got := runCLI(t, base, "", "hush", "now"); got.code != exitUsage {
		t.Errorf("hush with an argument: exit %d, want %d", got.code, exitUsage)
	}
}

// TestEvalResultDistinguishesStoredFromIgnored is the reason this command reads
// the state first.
//
// The endpoint answers {"accepted": true} for a report it understood, INCLUDING
// one it then discarded as stale — accepted means well-formed, not stored, and
// nothing on the wire distinguishes them. A CLI that printed "ok" off the back
// of accepted:true would tell an agent its verdict is the server's verdict when
// it is not. So both cases are driven against the real server and told apart.
func TestEvalResultDistinguishesStoredFromIgnored(t *testing.T) {
	base := newTestServer(t)

	// Publish two versions so a report about v1 can be stale.
	runCLI(t, base, `s("bd")`, "push")
	runCLI(t, base, `s("hh")`, "push")

	// A verdict for the CURRENT version is stored.
	stored := runCLI(t, base, "", "eval-result", "-version", "2", "-ok=false",
		"-error", "x is not a function", "-stats", `{"haps":0}`)
	if stored.code != exitOK {
		t.Fatalf("eval-result v2: exit %d, stderr %q", stored.code, stored.stderr)
	}
	if !strings.Contains(stored.stdout, "stored for version 2") {
		t.Errorf("eval-result v2 = %q, want it reported as stored", stored.stdout)
	}
	if strings.Contains(stored.stdout, "IGNORED") {
		t.Errorf("eval-result v2 = %q, must not claim it was ignored", stored.stdout)
	}

	// Now the SAME version again is not stale (equal is accepted), but an OLDER
	// one is. This is the accepted-but-ignored case.
	ignored := runCLI(t, base, "", "eval-result", "-version", "1")
	if ignored.code != exitOK {
		t.Errorf("stale eval-result: exit %d, want 0 — the server accepted it", ignored.code)
	}
	if !strings.Contains(ignored.stdout, "IGNORED") {
		t.Errorf("stale eval-result = %q, want it reported as accepted but IGNORED", ignored.stdout)
	}
	if !strings.Contains(ignored.stdout, "version 2") {
		t.Errorf("stale eval-result = %q, want it to name the verdict already stored", ignored.stdout)
	}

	// Prove the state really did not change: the stored verdict is still v2.
	state := runCLI(t, base, "", "state")
	if !strings.Contains(state.stdout, "last eval:      version=2 ok=false") {
		t.Errorf("the ignored report changed the stored verdict\ngot:\n%s", state.stdout)
	}
	if !strings.Contains(state.stdout, `error="x is not a function"`) {
		t.Errorf("state lost the stored verdict's error\ngot:\n%s", state.stdout)
	}
}

// TestUsageErrorsExitTwoWithoutSending covers the caller-mistake half of the
// exit-code contract. These must be distinguishable from a server refusal, and
// must be caught locally rather than by sending a doomed request.
func TestUsageErrorsExitTwoWithoutSending(t *testing.T) {
	base := newTestServer(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, "usage:"},
		{"unknown command", []string{"dance"}, `unknown command "dance"`},
		{"message with no text", []string{"message"}, "needs the text"},
		{"state with an argument", []string{"state", "extra"}, "takes no arguments"},
		{"push with a positional", []string{"push", "pattern.js"}, "no positional"},
		{"eval-result with no version", []string{"eval-result"}, "-version"},
		{"eval-result with invalid stats", []string{"eval-result", "-version", "1", "-stats", "{nope"}, "valid JSON"},
		{"unknown flag", []string{"-nope", "state"}, "flag provided but not defined"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLI(t, base, "", tc.args...)
			if got.code != exitUsage {
				t.Errorf("exit %d, want %d (stdout %q, stderr %q)", got.code, exitUsage, got.stdout, got.stderr)
			}
			// A usable message, not a bare code: the user has to know what to fix.
			if !strings.Contains(got.stderr, tc.want) {
				t.Errorf("stderr %q does not explain the problem (want %q)", got.stderr, tc.want)
			}
		})
	}
}

// TestBadBaseURLFailsClearly covers the "fails clearly with a non-zero exit and a
// usable message" criterion for a URL the CLI cannot use at all.
func TestBadBaseURLFailsClearly(t *testing.T) {
	tests := []struct{ name, base, want string }{
		{"empty", "", "no base URL"},
		{"whitespace", "   ", "no base URL"},
		{"wrong scheme", "ftp://localhost:8000", "must be http or https"},
		{"no host", "http://", "no host"},
		{"unparseable", "http://[::1", "invalid base URL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			code := run([]string{"-base", tc.base, "state"}, strings.NewReader(""), &out, &errBuf)
			if code != exitUsage {
				t.Errorf("exit %d, want %d (stderr %q)", code, exitUsage, errBuf.String())
			}
			if !strings.Contains(errBuf.String(), tc.want) {
				t.Errorf("stderr %q does not explain the bad URL (want %q)", errBuf.String(), tc.want)
			}
		})
	}
}

// TestUnreachableServerIsNotARejection separates "the server said no" from "the
// server never answered". Reporting a dial failure as a rejection would be a lie
// about what happened, and an agent that retries on a rejection but not on a
// transport failure needs the two to be distinguishable.
//
// The port is bound and then released, so nothing is listening; the CLI's own
// timeout bounds the case where the connection is not refused promptly.
func TestUnreachableServerIsNotARejection(t *testing.T) {
	dead := reserveClosedPort(t)

	got := runCLI(t, "http://"+dead, "", "state")
	if got.code != exitError {
		t.Errorf("exit %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "cannot reach") {
		t.Errorf("stderr %q, want it to say the server was unreachable rather than refused", got.stderr)
	}
	// "server rejected" is the rejection wording and must not appear here.
	if strings.Contains(got.stderr, "server rejected") {
		t.Errorf("stderr %q reports a transport failure as a server rejection", got.stderr)
	}
}

// reserveClosedPort returns a loopback address that was bound and immediately
// released, so connecting to it fails. It asserts the port is not 8000.
func reserveClosedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	if strings.HasSuffix(addr, ":8000") {
		t.Fatalf("reserved the production port %s; tests must not use it", addr)
	}
	return addr
}

// TestOversizedDocumentIsRejectedByTheServer drives the payload cap end to end.
// The CLI's own read bound is deliberately looser than the server's 64 KiB, so
// the rejection must come from the server and carry the server's message.
func TestOversizedDocumentIsRejectedByTheServer(t *testing.T) {
	base := newTestServer(t)
	huge := strings.Repeat("a", 70<<10) // over APIMaxBodyBytes (64 KiB)

	got := runCLI(t, base, huge, "push")
	if got.code != exitError {
		t.Errorf("exit %d, want %d for an oversized body", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "too large") {
		t.Errorf("stderr %q, want the server's payload-cap message", got.stderr)
	}
}

// TestMalformedSuccessBodyIsReported proves a 2xx that is not the documented JSON
// is reported rather than decoded into a plausible-looking zero value. Silently
// printing "version 0" because a response did not parse is how a CLI starts
// lying about the performance.
func TestMalformedSuccessBodyIsReported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("this is not json"))
	}))
	t.Cleanup(func() { closeBounded(t, ts) })

	got := runCLI(t, "http://"+ts.Listener.Addr().String(), "", "state")
	if got.code != exitError {
		t.Errorf("exit %d, want %d for an undecodable body", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "undecodable") {
		t.Errorf("stderr %q, want it to name the undecodable body", got.stderr)
	}
}

// closeBounded closes an httptest server without ever blocking the test forever.
func closeBounded(t *testing.T, ts *httptest.Server) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ts.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Errorf("httptest.Server.Close did not return within 5s")
	}
}

// TestTimeoutIsReportedAsATimeout proves a slow server fails rather than hanging.
// The handler sleeps well past the CLI's deadline; the CLI must return on its own
// budget and say so, and the watchdog bounds the whole CALL so a regression shows
// up as a failure rather than as a stuck suite.
func TestTimeoutIsReportedAsATimeout(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done(): // the client gave up, as it should
		case <-time.After(30 * time.Second):
		}
	}))
	t.Cleanup(func() {
		close(release) // let the handler return so Close cannot block
		closeBounded(t, ts)
	})

	var out, errBuf bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{
			"-base", "http://" + ts.Listener.Addr().String(),
			"-timeout", "300ms", "state",
		}, strings.NewReader(""), &out, &errBuf)
	}()

	// The watchdog bounds the CALL, not just the assertion afterwards: an
	// unbounded wait here would be the very defect this test exists to catch.
	select {
	case code := <-done:
		if code != exitError {
			t.Errorf("exit %d, want %d on a timeout", code, exitError)
		}
		if !strings.Contains(errBuf.String(), "within the timeout") {
			t.Errorf("stderr %q, want it to report a timeout", errBuf.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return within 10s against a slow server; the CLI is not bounded")
	}
}

// currentAnchor reads the stored anchor straight from the real server, so an
// assertion about what the CLI did is checked against server state rather than
// against the CLI's own prose — which is the thing most likely to be wrong.
func currentAnchor(t *testing.T, base string) (epochMS int64, cps float64) {
	t.Helper()
	resp, err := http.Get(base + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state: %v", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/state: HTTP %d", resp.StatusCode)
	}
	var snap snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decoding /api/state: %v", err)
	}
	return snap.Anchor.EpochMS, snap.Anchor.CPS
}

// TestAnchorMovesTheSharedTimeline is the happy path: a changed rate is stored,
// the version is NOT bumped (a re-anchor moves where the timeline starts, not
// what is playing), and the reported numbers are the server's own.
func TestAnchorMovesTheSharedTimeline(t *testing.T) {
	base := newTestServer(t)

	// Publish something first, so an unchanged version is evidence rather than
	// the trivial case of a performance that never had a version.
	if got := runCLI(t, base, `s("bd")`, "push"); got.code != exitOK {
		t.Fatalf("push: exit %d, stderr %q", got.code, got.stderr)
	}

	got := runCLI(t, base, "", "anchor", "-cps", "0.75")
	if got.code != exitOK {
		t.Fatalf("anchor -cps 0.75: exit %d, stderr %q", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, "no change") {
		t.Errorf("stdout %q reports a no-op for a real tempo change", got.stdout)
	}
	if !strings.Contains(got.stdout, "cps=0.75") {
		t.Errorf("stdout %q, want it to report the new rate from the server", got.stdout)
	}

	epochMS, cps := currentAnchor(t, base)
	if cps != 0.75 {
		t.Errorf("server anchor cps = %g, want 0.75", cps)
	}
	if epochMS == 0 {
		t.Error("server anchor epochMs was lost; a re-anchor replaced the timeline instead of moving it")
	}
	if !strings.Contains(got.stdout, "version 1 unchanged") {
		t.Errorf("stdout %q, want it to say the version did not move", got.stdout)
	}
}

// TestAnchorCarriesOverTheHalfItWasNotGiven pins the convenience that makes this
// command safe to use: the endpoint REPLACES the whole timeline, so a body
// carrying only one half would be a 400 — and an agent hand-writing that body
// would have to read /api/state first to get the other half right. The CLI does
// that read for them, so naming only -cps must not lose the epoch.
func TestAnchorCarriesOverTheHalfItWasNotGiven(t *testing.T) {
	base := newTestServer(t)

	epochBefore, _ := currentAnchor(t, base)
	got := runCLI(t, base, "", "anchor", "-cps", "1.5")
	if got.code != exitOK {
		t.Fatalf("anchor -cps only: exit %d, stderr %q", got.code, got.stderr)
	}
	epochAfter, cps := currentAnchor(t, base)
	if cps != 1.5 {
		t.Errorf("cps = %g, want the requested 1.5", cps)
	}
	if epochAfter != epochBefore {
		t.Errorf("epochMs moved from %d to %d when only -cps was given; the unnamed half must be carried over",
			epochBefore, epochAfter)
	}

	// And symmetrically for the epoch: the rate must survive a bare -epoch-ms.
	// The epoch is nudged by a millisecond so the no-op comparison cannot
	// collapse this into "no change".
	_, cpsBefore := currentAnchor(t, base)
	got = runCLI(t, base, "", "anchor", "-epoch-ms", strconv.FormatInt(epochAfter+1, 10))
	if got.code != exitOK {
		t.Fatalf("anchor -epoch-ms only: exit %d, stderr %q", got.code, got.stderr)
	}
	if _, cpsAfter := currentAnchor(t, base); cpsAfter != cpsBefore {
		t.Errorf("cps changed from %g to %g when only -epoch-ms was given", cpsBefore, cpsAfter)
	}
}

// TestAnchorReportsANoopAsANoop is the "accepted is not applied" property, and
// it is real here rather than hypothetical: a re-anchor with the SAME values is
// accepted, stored and broadcast, and nothing on the wire distinguishes it from
// one that moved every listener onto a new bar grid.
//
// So the command reads the state first and compares, and the wording says what
// was OBSERVED. The exit code is still 0 — the request WAS accepted — and the
// distinct wording is what stops a caller reading "no change" as a re-anchor.
func TestAnchorReportsANoopAsANoop(t *testing.T) {
	base := newTestServer(t)

	// Move the anchor somewhere distinctive, so the repeat is unambiguous.
	if got := runCLI(t, base, "", "anchor", "-cps", "2"); got.code != exitOK {
		t.Fatalf("first anchor: exit %d, stderr %q", got.code, got.stderr)
	}
	epoch, _ := currentAnchor(t, base)

	// Re-sending exactly what is stored must be reported as a no-op.
	repeat := runCLI(t, base, "", "anchor", "-epoch-ms", strconv.FormatInt(epoch, 10), "-cps", "2")
	if repeat.code != exitOK {
		t.Errorf("repeat anchor: exit %d, want 0 — the request WAS accepted", repeat.code)
	}
	if !strings.Contains(repeat.stdout, "no change") {
		t.Errorf("repeat anchor = %q, want it reported as a no-op", repeat.stdout)
	}
	if !strings.Contains(repeat.stdout, "already") {
		t.Errorf("repeat anchor = %q, want it to say the timeline was already there", repeat.stdout)
	}

	// A genuine move must NOT be reported as a no-op, or the wording is worse
	// than useless: it is the false negative an agent would act on.
	moved := runCLI(t, base, "", "anchor", "-cps", "3")
	if moved.code != exitOK {
		t.Fatalf("moved anchor: exit %d, stderr %q", moved.code, moved.stderr)
	}
	if strings.Contains(moved.stdout, "no change") {
		t.Errorf("a real tempo change is reported as a no-op: %q", moved.stdout)
	}
}

// TestAnchorRefusalIsReportedVerbatimAndFails is the first CLI property on this
// endpoint: a rejected re-anchor leaves the stored anchor UNTOUCHED and is never
// broadcast, so an agent that believed it had landed would believe every
// listener had moved onto a timeline the server refused to store.
//
// Both halves matter independently: a paraphrase loses the reason (the server
// distinguishes a bad rate from a bad skew), and exit 0 would let a script
// record a refused re-anchor as applied.
func TestAnchorRefusalIsReportedVerbatimAndFails(t *testing.T) {
	base := newTestServer(t)
	epochBefore, cpsBefore := currentAnchor(t, base)

	tests := []struct {
		name string
		args []string
		want string // the server's exact wording, not a paraphrase
	}{
		{"zero rate", []string{"anchor", "-cps", "0"}, "cps must be greater than 0"},
		{"negative rate", []string{"anchor", "-cps", "-1"}, "cps must be greater than 0"},
		{"runaway rate", []string{"anchor", "-cps", "100000"}, "cps must be at most 1000"},
		{
			// Far outside AnchorMaxSkewMS, so this cannot pass by accident.
			name: "epoch from a badly skewed clock",
			args: []string{"anchor", "-epoch-ms", "1000000000000"},
			want: "must be within",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLI(t, base, "", tc.args...)
			if got.code == exitOK {
				t.Errorf("exit 0 on a refused re-anchor; want non-zero\nstdout: %q", got.stdout)
			}
			if got.code != exitError {
				t.Errorf("exit %d, want %d (a refusal, not a bad command line)", got.code, exitError)
			}
			if !strings.Contains(got.stderr, tc.want) {
				t.Errorf("stderr %q does not carry the server's verbatim reason %q", got.stderr, tc.want)
			}
			// A refusal must not read as a success on stdout either.
			if strings.Contains(got.stdout, "anchor: epochMs=") {
				t.Errorf("stdout claims an anchor landed despite a refusal: %q", got.stdout)
			}
			// And the refusal must be total: the stored anchor is untouched.
			if epochAfter, cpsAfter := currentAnchor(t, base); epochAfter != epochBefore || cpsAfter != cpsBefore {
				t.Errorf("a refused re-anchor changed the stored anchor: epochMs %d->%d cps %g->%g",
					epochBefore, epochAfter, cpsBefore, cpsAfter)
			}
		})
	}
}

// TestAnchorUsageErrorsExitTwo covers the second half of the exit-code contract:
// a command line that names nothing cannot be sent at all. Exit 2 rather than 1
// is what lets a caller retry after fixing its arguments without mistaking a
// malformed invocation for a server refusal.
func TestAnchorUsageErrorsExitTwo(t *testing.T) {
	base := newTestServer(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"neither flag", []string{"anchor"}, "-epoch-ms"},
		{"positional argument", []string{"anchor", "now"}, "no positional"},
		{"unknown flag", []string{"anchor", "-tempo", "2"}, "not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLI(t, base, "", tc.args...)
			if got.code != exitUsage {
				t.Errorf("exit %d, want %d (stdout %q, stderr %q)", got.code, exitUsage, got.stdout, got.stderr)
			}
			if !strings.Contains(got.stderr, tc.want) {
				t.Errorf("stderr %q does not explain the problem (want %q)", got.stderr, tc.want)
			}
		})
	}
}

// TestHelpExitsZero covers the one non-error path that is not a command, and
// checks every subcommand is discoverable from it.
func TestHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		got := runCLI(t, "http://localhost:1", "", arg)
		if got.code != exitOK {
			t.Errorf("%s: exit %d, want 0", arg, got.code)
		}
		if !strings.Contains(got.stderr, "usage: agentcli") {
			t.Errorf("%s: stderr %q, want the usage text", arg, got.stderr)
		}
		for _, want := range []string{"state", "push", "message", "hush", "play", "eval-result", "anchor"} {
			if !strings.Contains(got.stderr, want) {
				t.Errorf("%s: usage does not mention %q", arg, want)
			}
		}
	}
}

// TestSubcommandHelpPrintsItsOwnFlags is the counterpart of TestHelpExitsZero for
// the per-subcommand flagsets (strudel-agent-uvj.12).
//
// These flagsets are the ONLY documentation of -f, -m, -version, -ok, -error,
// -stats, -epoch-ms and -cps: each one sets its output to io.Discard so that a
// parse error is reported through the CLI's own exit-code contract rather than
// Go's, which also suppresses Go's automatic usage printing. So if -h is not
// handled here, the flags are undiscoverable except by reading the Go source.
//
// Help is also a SUCCESSFUL request, exactly as it is for the top-level flags,
// where main.go already maps flag.ErrHelp to exitOK. A bare "flag: help
// requested" error with exit 2 told the user nothing and mislabelled a valid
// request as a mistake.
//
// A dead port is used on purpose: help is a local answer to a local question, so
// it must succeed without reaching a server at all.
func TestSubcommandHelpPrintsItsOwnFlags(t *testing.T) {
	dead := "http://" + reserveClosedPort(t)

	tests := []struct {
		command string
		// wantFlags are the flag names that must appear. These are the strings a
		// user would otherwise have to grep the source for.
		wantFlags []string
		// wantText are the usage strings that explain what the flags mean.
		wantText []string
	}{
		{
			command:   "push",
			wantFlags: []string{"-f", "-m"},
			wantText:  []string{"read the document from this file", "narration shown to listeners"},
		},
		{
			command:   "eval-result",
			wantFlags: []string{"-version", "-ok", "-error", "-stats"},
			wantText:  []string{"the published version this verdict is about", "opaque stats JSON"},
		},
		{
			command:   "anchor",
			wantFlags: []string{"-epoch-ms", "-cps"},
			wantText:  []string{"the shared timeline's epoch", "cycles per second"},
		},
	}

	for _, tc := range tests {
		for _, helpFlag := range []string{"-h", "--help"} {
			t.Run(tc.command+" "+helpFlag, func(t *testing.T) {
				got := runCLI(t, dead, "", tc.command, helpFlag)
				if got.code != exitOK {
					t.Fatalf("exit %d, want 0 — help is a successful request, not a usage mistake\nstdout: %q\nstderr: %q",
						got.code, got.stdout, got.stderr)
				}
				// The usage line, so the text says which command it belongs to.
				if !strings.Contains(got.stderr, "usage: agentcli "+tc.command) {
					t.Errorf("stderr %q, want the usage line for %q", got.stderr, tc.command)
				}
				for _, flag := range tc.wantFlags {
					if !strings.Contains(got.stderr, flag) {
						t.Errorf("stderr does not document %q — the flag is undiscoverable\nstderr:\n%s", flag, got.stderr)
					}
				}
				for _, text := range tc.wantText {
					if !strings.Contains(got.stderr, text) {
						t.Errorf("stderr does not explain %q\nstderr:\n%s", text, got.stderr)
					}
				}
				// The old behaviour printed Go's raw error string. It must be gone,
				// or a caller grepping for it would still be misled about the cause.
				if strings.Contains(got.stderr, "flag: help requested") {
					t.Errorf("stderr still reports help as a Go parse error:\n%s", got.stderr)
				}
				// Nothing was sent, so nothing may be claimed on stdout.
				if strings.TrimSpace(got.stdout) != "" {
					t.Errorf("stdout %q, want nothing: help touches no endpoint", got.stdout)
				}
			})
		}
	}
}

// TestFlaglessSubcommandHelpExitsZero covers the subcommands that have no flags
// of their own (strudel-agent-uvj.12). They reject extra arguments, so `state -h`
// used to be reported as "takes no arguments, got \"-h\"" with exit 2 — a valid
// request for help answered as a mistake, the same defect as on the flag-taking
// commands.
//
// The check is deliberately narrow: an args slice that is EXACTLY the help flag.
// `state extra` remains the usage error the exit-code contract promises, so this
// cannot be satisfied by simply ignoring a subcommand's arguments.
func TestFlaglessSubcommandHelpExitsZero(t *testing.T) {
	dead := "http://" + reserveClosedPort(t)

	for _, command := range []string{"state", "message", "hush", "play"} {
		for _, helpFlag := range []string{"-h", "--help"} {
			t.Run(command+" "+helpFlag, func(t *testing.T) {
				got := runCLI(t, dead, "", command, helpFlag)
				if got.code != exitOK {
					t.Errorf("exit %d, want 0 for a help request (stderr %q)", got.code, got.stderr)
				}
				if !strings.Contains(got.stderr, "usage: agentcli "+command) {
					t.Errorf("stderr %q, want the usage line for %q", got.stderr, command)
				}
				if strings.TrimSpace(got.stdout) != "" {
					t.Errorf("stdout %q, want nothing: help touches no endpoint", got.stdout)
				}
			})
		}
	}
}

// TestSubcommandParseErrorsAreNotHelpRequests is the half of the contract that
// must NOT move: help is a successful request, a bad flag is a usage mistake,
// and the two must stay distinguishable by exit code (0 versus 2).
//
// Without this the obvious "fix" — treating every parse error as help — would
// pass the help tests above while silently reclassifying real mistakes as
// success, so a script could no longer tell a bad invocation from a good one.
func TestSubcommandParseErrorsAreNotHelpRequests(t *testing.T) {
	base := newTestServer(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag on anchor", []string{"anchor", "-tempo", "2"}, "not defined"},
		{"unknown flag on eval-result", []string{"eval-result", "-verdict", "1"}, "not defined"},
		{"unknown flag on push", []string{"push", "-file", "x.js"}, "not defined"},
		{"non-numeric version", []string{"eval-result", "-version", "notanumber"}, "invalid value"},
		{"non-numeric rate", []string{"anchor", "-cps", "quick"}, "invalid value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCLI(t, base, "", tc.args...)
			if got.code != exitUsage {
				t.Errorf("exit %d, want %d — a bad flag is a usage mistake, not a help request\nstderr: %q",
					got.code, exitUsage, got.stderr)
			}
			if !strings.Contains(got.stderr, tc.want) {
				t.Errorf("stderr %q does not explain the problem (want %q)", got.stderr, tc.want)
			}
			// A parse error must still be REPORTED as one. It is acceptable — and
			// is Go's own convention — that the usage text accompanies it, so the
			// proof that this was not treated as help is the diagnostic line, not
			// the absence of usage. Without that line, the "fix" of calling every
			// parse error help would satisfy the exit-code check above by luck.
			if !strings.Contains(got.stderr, "agentcli: ") {
				t.Errorf("a genuine parse error was not reported as a mistake:\n%s", got.stderr)
			}
		})
	}
}

// TestSubcommandHelpDoesNotWeakenTheExitCodeContract re-proves the two mappings
// that surround the change: a server refusal is still 1 and a usage mistake is
// still 2, when a subcommand flagset is in play. It guards the fix against
// "solving" the help bug by widening what counts as a success.
func TestSubcommandHelpDoesNotWeakenTheExitCodeContract(t *testing.T) {
	base := newTestServer(t)

	// A refusal from a subcommand with flags: exit 1, not 2, not 0.
	refused := runCLI(t, base, "", "anchor", "-cps", "0")
	if refused.code != exitError {
		t.Errorf("a refused re-anchor exited %d, want %d (stderr %q)", refused.code, exitError, refused.stderr)
	}
	if !strings.Contains(refused.stderr, "cps must be greater than 0") {
		t.Errorf("stderr %q, want the server's verbatim reason", refused.stderr)
	}

	// And a bad flag on the same subcommand is still 2, not 1.
	mistake := runCLI(t, base, "", "anchor", "-tempo", "2")
	if mistake.code != exitUsage {
		t.Errorf("a bad flag exited %d, want %d", mistake.code, exitUsage)
	}
}

// TestBaseURLComesFromTheEnvironment proves the env var is honoured, since that
// is how a harness points every invocation at one server without repeating a flag.
func TestBaseURLComesFromTheEnvironment(t *testing.T) {
	t.Setenv(baseURLEnv, newTestServer(t))

	var out, errBuf bytes.Buffer
	code := run([]string{"-timeout", testTimeout.String(), "state"}, strings.NewReader(""), &out, &errBuf)
	if code != exitOK {
		t.Fatalf("state via %s: exit %d, stderr %q", baseURLEnv, code, errBuf.String())
	}
	if !strings.Contains(out.String(), "version:") {
		t.Errorf("stdout %q, want the snapshot", out.String())
	}
}

// TestBareHostPortIsAccepted covers the "localhost:8000" spelling people actually
// type, without making them remember the scheme.
func TestBareHostPortIsAccepted(t *testing.T) {
	bare := strings.TrimPrefix(newTestServer(t), "http://")

	var out, errBuf bytes.Buffer
	code := run([]string{"-base", bare, "-timeout", testTimeout.String(), "state"},
		strings.NewReader(""), &out, &errBuf)
	if code != exitOK {
		t.Fatalf("state via bare host:port: exit %d, stderr %q", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "version:") {
		t.Errorf("stdout %q, want the snapshot", out.String())
	}
}
