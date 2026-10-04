package srv

// Sample resolution on the server side (strudel-agent-uvj.18).
//
// The browser resolves sample names and reports the result as `samplesResolved`.
// That report has to survive the whole path — request decoding, the Conductor, the
// stored verdict, the snapshot — or the field is decoration and the agent learns
// nothing, which is the original defect in a different costume.
//
// The load-bearing property is the TRI-STATE. `samplesResolved` is a *bool with
// omitempty, so three distinguishable facts travel on the wire:
//
//	true   every sound the pattern named resolved in the reporting browser
//	false  at least one did not
//	absent UNKNOWN — no registry to ask, so nothing was checked
//
// A plain bool would collapse the last two, and that collapse IS the defect: "I
// checked and it failed" and "I could not check" are different facts, and an agent
// that cannot tell them apart will read an unchecked pattern as a healthy one.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// triState builds a *bool, which is how the wire field distinguishes its three
// states. nil is UNKNOWN.
func triState(b bool) *bool { return &b }

// stateSnapshot reads and decodes the live snapshot.
func stateSnapshot(t *testing.T, base string) Snapshot {
	t.Helper()
	code, raw := fanoutGet(t, base, "/api/state")
	if code != http.StatusOK {
		t.Fatalf("GET /api/state = %d %q", code, bodyOf(raw))
	}
	var snap Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("decoding state %s: %v", bodyOf(raw), err)
	}
	return snap
}

// TestEvalResultAcceptsSamplesResolved is the first link in the chain and the one
// most likely to break silently: decodeBody runs with DisallowUnknownFields, so a
// request struct without the field answers 400 to the browser's own report. The
// symptom would be a console full of failed reports and no stored verdict at all,
// which looks like a listener problem and is not one.
func TestEvalResultAcceptsSamplesResolved(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"true", `{"version":1,"ok":true,"samplesResolved":true,"stats":{"haps":2}}`},
		{"false", `{"version":1,"ok":true,"samplesResolved":false,"stats":{"haps":2}}`},
		{"absent", `{"version":1,"ok":true,"stats":{"haps":2}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, base, _ := fanoutServer(t)
			pushCode(t, base, `{"code":"s(\"bd\")"}`)
			code, raw := fanoutPost(t, base, "/api/eval-result", tc.body)
			if code != http.StatusOK {
				t.Fatalf("POST /api/eval-result = %d %q, want 200: the browser's own report must be decodable", code, bodyOf(raw))
			}
		})
	}
}

// TestStoredVerdictPreservesTheTriState proves the distinction survives storage.
// Asserting only "it round-trips" would pass with a plain bool, because a bool
// round-trips fine — what matters is that absent stays ABSENT.
func TestStoredVerdictPreservesTheTriState(t *testing.T) {
	_, base, _ := fanoutServer(t)

	cases := []struct {
		name       string
		field      string
		wantAbsent bool
		wantValue  bool
	}{
		{"true", `"samplesResolved":true,`, false, true},
		{"false", `"samplesResolved":false,`, false, false},
		{"absent", ``, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh version per case, so each report is the stored verdict
			// rather than a stale one.
			pushCode(t, base, `{"code":"s(\"bd*2\")"}`)
			version := latestVersion(t, base)
			body := fmt.Sprintf(`{"version":%d,"ok":true,%s"stats":{"haps":1}}`, version, tc.field)
			if code, raw := fanoutPost(t, base, "/api/eval-result", body); code != http.StatusOK {
				t.Fatalf("POST /api/eval-result = %d %q", code, bodyOf(raw))
			}

			snap := stateSnapshot(t, base)
			if snap.LastEvalResult == nil {
				t.Fatal("no stored verdict: the report was understood but nothing was kept")
			}
			got := snap.LastEvalResult.SamplesResolved
			if tc.wantAbsent {
				if got != nil {
					t.Errorf("samplesResolved = %v, want ABSENT: nothing was checked, so the field must not claim a result", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("samplesResolved is ABSENT, want %v", tc.wantValue)
			}
			if *got != tc.wantValue {
				t.Errorf("samplesResolved = %v, want %v", *got, tc.wantValue)
			}
		})
	}
}

// TestTheSnapshotDoesNotAliasTheTriState guards an aliasing hazard that a test
// asserting only the value would miss. Snapshot must not hand out a pointer into
// Conductor state: a caller that wrote through it would corrupt the stored verdict
// for every later reader, and the write would succeed, so nothing would report it.
func TestTheSnapshotDoesNotAliasTheTriState(t *testing.T) {
	c := NewConductor(4)
	c.Publish(`s("bd")`, "")

	if _, err := c.RecordEvalResult(EvalResult{Version: 1, OK: true, SamplesResolved: triState(true)}); err != nil {
		t.Fatalf("RecordEvalResult: %v", err)
	}
	snap := c.Snapshot()
	if snap.LastEvalResult == nil || snap.LastEvalResult.SamplesResolved == nil {
		t.Fatal("the snapshot lost the stored samples finding")
	}

	// Corrupt the copy this caller holds.
	*snap.LastEvalResult.SamplesResolved = false

	again := c.Snapshot()
	if again.LastEvalResult.SamplesResolved == nil || !*again.LastEvalResult.SamplesResolved {
		t.Error("writing through a snapshot's samplesResolved corrupted Conductor state: Snapshot must deep-copy the tri-state, as it does stats")
	}
}

// TestAnUnknownSampleFindingIsAbsentFromTheWireNotNull is the byte-level half of
// the tri-state, and it is the half a Go-level assertion cannot see.
//
// Every other test here decodes the verdict back into a struct, where a nil
// *bool and a missing key look identical. So dropping `omitempty` from the json
// tag — turning UNKNOWN into an emitted `"samplesResolved":null` — passes all of
// them while breaking the documented contract, which says empty fields are
// omitted and that an absent field means UNKNOWN. A reader doing
// `verdict.samplesResolved === null` in a language with a real null would then see
// a fourth state the documentation never promised, and one that reads like a
// finding rather than an absence.
//
// The assertion is on the raw JSON text, not on a decoded value, because the
// decoded value is exactly what cannot tell the two apart.
func TestAnUnknownSampleFindingIsAbsentFromTheWireNotNull(t *testing.T) {
	_, base, _ := fanoutServer(t)
	pushCode(t, base, `{"code":"s(\"bd\")"}`)

	// A report with no samplesResolved at all: the browser had no registry.
	version := latestVersion(t, base)
	if code, raw := fanoutPost(t, base, "/api/eval-result",
		fmt.Sprintf(`{"version":%d,"ok":true,"stats":{"haps":1}}`, version)); code != http.StatusOK {
		t.Fatalf("POST /api/eval-result = %d %q", code, bodyOf(raw))
	}

	_, raw := fanoutGet(t, base, "/api/state")
	if strings.Contains(string(raw), "samplesResolved") {
		t.Errorf("GET /api/state contains samplesResolved for an UNKNOWN verdict: %s\n"+
			"an unknown finding must be ABSENT; an emitted null is a fourth state the API does not document",
			bodyOf(raw))
	}

	// The positive control, in the same breath: with a finding stored, the field
	// IS on the wire. Without this, the assertion above would also pass against a
	// server that never emitted the field at all — which would satisfy "absent
	// means unknown" by making "false means unresolved" unreachable.
	pushCode(t, base, `{"code":"s(\"cp\")"}`)
	version = latestVersion(t, base)
	if code, resp := fanoutPost(t, base, "/api/eval-result",
		fmt.Sprintf(`{"version":%d,"ok":true,"samplesResolved":false,"stats":{"haps":1}}`, version)); code != http.StatusOK {
		t.Fatalf("POST /api/eval-result (with a finding) = %d %q", code, bodyOf(resp))
	}
	_, raw = fanoutGet(t, base, "/api/state")
	if !strings.Contains(string(raw), `"samplesResolved":false`) {
		t.Errorf("GET /api/state does not carry samplesResolved:false for a stored finding: %s", bodyOf(raw))
	}
}

// TestStaleReportsCannotRegressTheSampleFinding checks the field obeys the same
// currency rule as the verdict carrying it. An accepted-but-discarded report must
// not overwrite a stored finding, or a browser that checked nothing could replace
// another browser's real "unresolved" with silence.
func TestStaleReportsCannotRegressTheSampleFinding(t *testing.T) {
	c := NewConductor(4)
	c.Publish(`s("bd")`, "")
	c.Publish(`s("cp")`, "")

	if stored, err := c.RecordEvalResult(EvalResult{Version: 2, OK: true, SamplesResolved: triState(false)}); err != nil || !stored {
		t.Fatalf("storing the v2 verdict: stored=%v err=%v", stored, err)
	}

	// v1 is stale and claims everything resolved.
	stored, err := c.RecordEvalResult(EvalResult{Version: 1, OK: true, SamplesResolved: triState(true)})
	if err != nil {
		t.Fatalf("a stale report must be UNDERSTOOD, not refused: %v", err)
	}
	if stored {
		t.Error("a stale report was stored, so it can replace the current verdict")
	}

	got := c.Snapshot().LastEvalResult.SamplesResolved
	if got == nil || *got {
		t.Errorf("samplesResolved = %v after a stale report, want false: the stored finding regressed", got)
	}
}

// TestSamplesResolvedIsNotConfusedWithOK states the separation the bead turns on.
// `ok:true` means the pattern parsed, evaluated and was committed. It says nothing
// about whether it will be audible, and a report that collapsed the two would
// reintroduce the original defect under a new field name.
func TestSamplesResolvedIsNotConfusedWithOK(t *testing.T) {
	_, base, _ := fanoutServer(t)
	pushCode(t, base, `{"code":"s(\"nope\")"}`)

	version := latestVersion(t, base)
	body := fmt.Sprintf(`{"version":%d,"ok":true,"samplesResolved":false,"stats":{"haps":2}}`, version)
	if code, raw := fanoutPost(t, base, "/api/eval-result", body); code != http.StatusOK {
		t.Fatalf("POST /api/eval-result = %d %q", code, bodyOf(raw))
	}

	snap := stateSnapshot(t, base)
	if !snap.LastEvalResult.OK {
		t.Error("ok = false: the pattern really did parse, so reporting a parse failure would be a second invented defect")
	}
	if snap.LastEvalResult.SamplesResolved == nil || *snap.LastEvalResult.SamplesResolved {
		t.Error("samplesResolved did not survive as false alongside ok:true: the two facts must be independent")
	}
}

// TestDryRunResultCarriesTheTriState covers the other endpoint. Dry-run state
// lives on the Server rather than the Conductor, so there is no stored verdict to
// inspect — but the finding still has to reach the waiting agent, or an agent
// pre-checking a candidate learns nothing and pushes it anyway.
//
// The request is answered from the LISTENER SOCKET rather than from a goroutine
// guessing, for the reason dryRunAndAnswer documents: reading the frame the
// browser was actually sent is what proves the candidate reached it.
func TestDryRunResultCarriesTheTriState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		field      string
		wantAbsent bool
		wantValue  bool
	}{
		{"true", `,"samplesResolved":true`, false, true},
		{"false", `,"samplesResolved":false`, false, false},
		{"absent", ``, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := dryRunServer(t)
			ts, conn := wsListener(t, s)

			type answer struct {
				code int
				body string
			}
			done := make(chan answer, 1)
			go func() {
				c, b, err := dryRunPostQuiet(ts, "/api/dry-run", dryRunBody(`s("nope")`))
				if err != nil {
					done <- answer{0, err.Error()}
					return
				}
				done <- answer{c, b}
			}()

			req := readDryRunRequest(t, conn)
			dryRunPost(t, ts, "/api/dry-run-result",
				fmt.Sprintf(`{"dryRunId":%d,"ok":true%s,"stats":{"haps":2}}`, req.ID, tc.field))

			res := <-done
			if res.code != http.StatusOK {
				t.Fatalf("POST /api/dry-run = %d %q, want 200", res.code, res.body)
			}
			var v DryRunVerdict
			if err := json.Unmarshal([]byte(res.body), &v); err != nil {
				t.Fatalf("dry-run verdict is not JSON: %v (%q)", err, res.body)
			}

			if tc.wantAbsent {
				if v.SamplesResolved != nil {
					t.Errorf("samplesResolved = %v, want ABSENT: nothing was checked, so the field must not claim a result", *v.SamplesResolved)
				}
				return
			}
			if v.SamplesResolved == nil {
				t.Fatal("the dry-run verdict lost samplesResolved: the two evaluation paths must not disagree")
			}
			if *v.SamplesResolved != tc.wantValue {
				t.Errorf("samplesResolved = %v, want %v", *v.SamplesResolved, tc.wantValue)
			}
		})
	}
}

// TestDryRunVerdictIsNeverStored pins the separation from the Conductor. A verdict
// about unpublished code must not become the stored verdict for the live version,
// or a dry-run would overwrite the only real feedback signal the agent has — and
// with it the sample finding, which would then describe code nobody is playing.
func TestDryRunVerdictIsNeverStored(t *testing.T) {
	s := dryRunServer(t)
	ts, conn := wsListener(t, s)

	s.Conductor.Publish(`s("bd")`, "")
	before := s.Conductor.Snapshot()

	issued := make(chan struct{})
	go func() {
		_, _, _ = dryRunPostQuiet(ts, "/api/dry-run", dryRunBody(`s("nope")`))
		close(issued)
	}()
	req := readDryRunRequest(t, conn)
	dryRunPost(t, ts, "/api/dry-run-result",
		fmt.Sprintf(`{"dryRunId":%d,"ok":true,"samplesResolved":false,"stats":{"haps":2}}`, req.ID))
	<-issued

	after := s.Conductor.Snapshot()
	if after.Version != before.Version {
		t.Errorf("version moved %d -> %d on a dry-run", before.Version, after.Version)
	}
	if after.LastEvalResult != nil {
		t.Errorf("a dry-run verdict was stored as the live verdict: %+v", after.LastEvalResult)
	}
}
