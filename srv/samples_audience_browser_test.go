package srv

// The audience report in the browser (strudel-agent-f79), executed as served.
//
// THE ASSERTION IS ON THE WIRE, NOT ON THE RENDERER, and that is the whole point
// of this file.
//
// The precedent is strudel-agent-uvj.15: drift was computed in the browser, painted
// into #sync-status, and then dropped — the only outbound call in any browser asset
// was the eval-result POST — so README's "residual drift is visible" was true only
// to a human watching the tab. A test that called describeSamples() and asserted
// on its return value would pass against a browser that resolved everything
// correctly and never told the server anything, which is the identical defect.
//
// So every test below drives the real decode seam and then reads the recorded
// fetch bodies. If the report does not reach the wire, these fail.

import (
	"testing"
)

// audienceBodies returns every body POSTed to /api/samples.
func audienceBodies(t *testing.T, rt *syncRuntime) []map[string]any {
	t.Helper()
	return evalBodies(t, rt, "/api/samples")
}

// identifyListener drives the REAL connect path: it feeds the served handleFrame a
// `listener` frame, exactly as ws.onmessage would.
//
// It is a helper rather than an inline frame so every test below starts from the
// same place a browser does — identity first, then everything else. A test that
// set the id directly would pass against a client whose decode seam ignored the
// frame entirely.
func identifyListener(t *testing.T, rt *syncRuntime, id string) {
	t.Helper()
	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
  kind: "listener",
  snapshot: { version: 0, code: "", anchor: null },
  listener: { id: ` + jsString(id) + ` },
}), window.__sandbox, window.__live);`)
}

// TestTheAudienceReportActuallyLeavesTheBrowser is the test this feature lives or
// dies by.
//
// A client that computed the right answer and never sent it would satisfy every
// other test in the repository, and the agent would learn nothing — which is the
// precise state strudel-agent-f79 was filed to fix. So this asserts the POST
// happened, was keyed to the id the server issued, and carried the registry state.
func TestTheAudienceReportActuallyLeavesTheBrowser(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = ["bd", "cp", "hh"];`)
	identifyListener(t, rt, "L7")

	bodies := audienceBodies(t, rt)
	if len(bodies) != 1 {
		t.Fatalf("%d reports to /api/samples after identifying, want exactly 1: %v; "+
			"a browser that computes the answer and never sends it is the uvj.15 defect again",
			len(bodies), bodies)
	}
	body := bodies[0]

	if body["listenerId"] != "L7" {
		t.Errorf("listenerId = %v, want L7: a report keyed to anything else is refused by the server",
			body["listenerId"])
	}
	if body["loaded"] != true {
		t.Errorf("loaded = %v, want true: the registry holds three sounds", body["loaded"])
	}
	if body["count"] != float64(3) {
		t.Errorf("count = %v, want 3: the count is what tells a partial pack from an empty one", body["count"])
	}
}

// TestNoReportIsSentBeforeTheServerNamesUs proves the client does not invent an
// identity.
//
// The server refuses a report naming no listener, so a client that POSTed before
// the `listener` frame arrived would fill the server's error path with its own
// handshake. More importantly it would be guessing at the one thing that makes a
// report attributable.
func TestNoReportIsSentBeforeTheServerNamesUs(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = ["bd"];`)

	if got := rt.eval(`String(window.strudelSession.getListenerId())`).String(); got != "null" {
		t.Fatalf("listener id = %s before any listener frame, want null", got)
	}
	if bodies := audienceBodies(t, rt); len(bodies) != 0 {
		t.Errorf("a report was sent before the server assigned an id: %v", bodies)
	}
}

// TestAnUnchangedRegistryIsNotReportedAgain proves the client-side gate.
//
// The server ignores an unchanged report, but a client that re-POSTed on every
// tick would still be issuing a request every 250ms forever — traffic, and a
// failure surface, for a state nobody is waiting on.
func TestAnUnchangedRegistryIsNotReportedAgain(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = ["bd"];`)
	identifyListener(t, rt, "L1")

	// Several further checks with nothing changed in between.
	rt.run(`window.strudelSession.reportAudienceSamples();`)
	rt.run(`window.strudelSession.reportAudienceSamples();`)
	rt.settle()

	if bodies := audienceBodies(t, rt); len(bodies) != 1 {
		t.Errorf("%d reports for one unchanging registry, want 1: %v", len(bodies), bodies)
	}
}

// TestALoadedPackIsReported proves the transition a human causes is noticed.
//
// Nobody tells this module that a pack arrived: welcome.html loads it
// asynchronously behind a button, with no callback here. The client polls the
// registry instead, which is why a change must actually be reported rather than
// suppressed as "no change since last time".
func TestALoadedPackIsReported(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = [];`)
	identifyListener(t, rt, "L1")

	first := audienceBodies(t, rt)
	if len(first) != 1 {
		t.Fatalf("precondition: want one report for an empty registry, got %v", first)
	}
	if first[0]["loaded"] != false {
		t.Errorf("loaded = %v for an empty registry, want false", first[0]["loaded"])
	}

	// The human presses Samples.
	rt.run(`window.__sounds = ["bd", "cp", "hh", "oh"];`)
	rt.run(`window.strudelSession.reportAudienceSamples();`)
	rt.settle()

	bodies := audienceBodies(t, rt)
	if len(bodies) != 2 {
		t.Fatalf("%d reports after a pack was loaded, want 2: the audience answer must follow the audience", len(bodies))
	}
	if bodies[1]["loaded"] != true || bodies[1]["count"] != float64(4) {
		t.Errorf("second report = %v, want loaded:true count:4", bodies[1])
	}
	if bodies[1]["listenerId"] != "L1" {
		t.Errorf("second report is keyed to %v, want the same listener L1", bodies[1]["listenerId"])
	}
}

// TestAnAbsentRegistryIsReportedAsUnknown proves the tri-state on the wire.
//
// A browser whose strudel bundle has not arrived has learned nothing about
// samples. Reporting false would tell an agent its packs are missing on the
// strength of a check that never ran — the uvj.18 defect in new clothes — so the
// key must be ABSENT rather than null or false.
func TestAnAbsentRegistryIsReportedAsUnknown(t *testing.T) {
	rt := newSamplesRuntime(t)
	// Remove the registry entirely, as a page whose CDN bundle failed would have it.
	rt.run(`window.strudel = {};`)
	identifyListener(t, rt, "L1")

	bodies := audienceBodies(t, rt)
	if len(bodies) != 1 {
		t.Fatalf("%d reports with no registry, want 1: %v", len(bodies), bodies)
	}
	raw, present := bodies[0]["loaded"]
	if present {
		t.Errorf("loaded = %v, want the key ABSENT: a browser that could not look has learned nothing, "+
			"and false would be a finding nobody made", raw)
	}
}

// TestAReconnectReportsUnderItsNewID proves a reconnected tab reappears.
//
// Ids are never reused, so a reconnect mints a new one. A client that treated its
// previous report as still valid would leave the new connection absent from the
// audience summary until its registry happened to change — and a summary that
// silently omits a live tab is worse than one that is merely stale, because it
// looks complete.
func TestAReconnectReportsUnderItsNewID(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = ["bd"];`)
	identifyListener(t, rt, "L1")
	identifyListener(t, rt, "L2")

	bodies := audienceBodies(t, rt)
	if len(bodies) != 2 {
		t.Fatalf("%d reports across a reconnect, want 2: %v", len(bodies), bodies)
	}
	if bodies[0]["listenerId"] != "L1" || bodies[1]["listenerId"] != "L2" {
		t.Errorf("reports are keyed %v then %v, want L1 then L2",
			bodies[0]["listenerId"], bodies[1]["listenerId"])
	}
}

// TestASamplesFrameDoesNotBecomeAVersion proves the audience frame is inert.
//
// `samples` describes what the audience can hear. Treating it as a change to the
// document would make a browser re-run validate-then-commit — and therefore
// re-commit the pattern and re-send an eval verdict — every time any listener's
// registry moved. That would turn an agent's single push into repeated commits
// with no new code, which is exactly the kind of quiet wrongness this repository
// treats as a defect.
func TestASamplesFrameDoesNotBecomeAVersion(t *testing.T) {
	rt := newSamplesRuntime(t)
	identifyListener(t, rt, "L1")

	before := rt.eval(`String(window.strudelSession.getLastVersion())`).String()
	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
  kind: "samples",
  snapshot: { version: 4, code: ` + jsString(`s("bd*2")`) + `, anchor: null },
}), window.__sandbox, window.__live);`)

	if after := rt.eval(`String(window.strudelSession.getLastVersion())`).String(); after != before {
		t.Errorf("lastVersion moved from %s to %s on a `samples` frame; "+
			"an audience report is not a new document", before, after)
	}
	if calls := rt.eval(`JSON.stringify(window.__sandboxCalls)`).String(); calls != "[]" {
		t.Errorf("a `samples` frame caused a sandbox evaluation: %s; "+
			"sample state changes must not re-commit the pattern", calls)
	}
}

// TestTheAudienceReporterDoesNotShadowTheVerdictReporter is a regression guard on
// a collision this change actually introduced.
//
// session.js already had a function named reportSamples -- the one that attaches a
// sample finding to an EVAL VERDICT. The audience reporter added here wanted a
// similar name, and because both are function declarations in one IIFE the second
// silently replaced the first: every verdict went out with no samplesResolved, and
// every dry-run verdict with it.
//
// Nothing about that failure looks like a naming mistake. The verdict tests failed
// with "samplesResolved present=false", which reads as a resolver problem, and the
// audience tests passed, because the shadowing function did send a report. Two
// suites each blaming the other is exactly how this ships unnoticed.
//
// JavaScript has no linker to catch a collision like this, so it is asserted
// through the REAL path: push a code frame and require the eval verdict to still
// carry its sample finding. The verdict reporter is deliberately not exported -- it
// has no business being, since it only ever runs inside validate-then-commit -- so
// this drives the decode seam rather than calling it. That is also the stronger
// assertion: it proves the finding survives all the way to the POST, not merely
// that two names point at different closures.
func TestTheAudienceReporterDoesNotShadowTheVerdictReporter(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = ["bd"];`)

	// Push one code version through the real validate-then-commit path.
	applyAndSettle(rt, 1, `s("bd")`)

	bodies := evalBodies(t, rt, "/api/eval-result")
	if len(bodies) != 1 {
		t.Fatalf("%d eval verdicts for one push, want 1: %v", len(bodies), bodies)
	}
	present, value := samplesResolved(t, bodies[0])
	if !present {
		t.Fatal("the eval verdict carries no samplesResolved at all. " +
			"reportAudienceSamples has very likely shadowed the verdict reporter that adds it, " +
			"which silently costs every agent the sample finding on every version")
	}
	if !value {
		t.Error("samplesResolved = false for a pattern naming a loaded sound")
	}

	// The audience reporter must not have leaked into the verdict path: reporting
	// the audience is a different POST, and conflating them would post one
	// version's state twice under two different keys.
	for _, b := range audienceBodies(t, rt) {
		if _, leaked := b["version"]; leaked {
			t.Errorf("an audience report carries a version field: %v; "+
				"it is about the audience, not about a document", b)
		}
	}
}
