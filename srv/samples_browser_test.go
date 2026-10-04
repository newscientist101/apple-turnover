package srv

// Sample resolution in the browser (strudel-agent-uvj.18), executed as served.
//
// The defect this file exists for is the most misleading class found so far: the
// system reported SUCCESS for work that provably could not work. `s("bd
// totally_not_a_real_sample_xyz")` validated ok:true with 2 haps and was handed
// to the live repl exactly like a real sample.
//
// The cause is structural. session.js validates in a sandbox whose
// `defaultOutput` is an empty async no-op and whose clock is pinned to 0, and
// sample resolution in Strudel happens on the OUTPUT path. With output stubbed,
// nothing ever resolves a name, so a missing sample cannot produce an error — the
// name is carried through as data. "ok:true" therefore meant only "this parsed
// into a Pattern whose queryArc returns N haps".
//
// So the whole defect lives in the browser, and a Go test that never runs the
// served JavaScript cannot observe any of it. These tests drive the served
// /static/session.js bytes in goja, exactly as coherence_test.go does, and assert
// on the body that would have been POSTed to /api/eval-result.
//
// The fix resolves names against window.strudel.soundMap — the registry the
// pinned @strudel/web@1.3.0 bundle exports, and the very map its own playback
// path consults (Un(l) = Kt.get()[l.toLowerCase()]) — so resolution needs no
// audio, no scheduler and no network, and defaultOutput stays stubbed.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// samplesPrelude extends the session harness with a recording fetch and a FAKE
// strudel registry.
//
// The registry is a stub, so the tests below are only as good as the
// normalisation they ask it to perform. That contract is pinned against the real
// bundle by TestNamesAreNormalisedTheWayTheBundleDoes below, which asserts the
// same rules the bundle's own registerSound applies:
//
//	jt: name.toLowerCase().replace(/\s+/g, "_")
//	Un: soundMap.get()[name.toLowerCase()]
//
// Anything looser here would let these tests pass against a resolver that
// disagrees with playback, which is the one thing a sample resolver must never do.
const samplesPrelude = sessionJSPrelude + `
window.__fetches = [];
var fetch = function (path, init) {
  window.__fetches.push({ path: path, body: init && init.body ? JSON.parse(init.body) : null });
  return apiStub();
};

// The haps the next evaluate() will report, and the outcome it will have.
window.__sandboxBehaviour = null;
function makeSandbox() {
  return {
    evaluate: function (code) {
      var b = window.__sandboxBehaviour;
      window.__sandboxCalls.push(code);
      if (b && b.throws) { throw new Error(b.throws); }
      var haps = (b && b.haps) || [];
      return Promise.resolve({ queryArc: function () { return haps; } });
    },
    get state() {
      var b = window.__sandboxBehaviour;
      return b && b.evalError ? { evalError: new Error(b.evalError) } : {};
    },
  };
}
window.__sandboxCalls = [];

// The fake registry. __sounds holds REGISTERED names, written the way the bundle
// writes them: already normalised. A test registering a name containing a space
// must add it normalised, which is the point — the resolver has to normalise
// before asking.
window.__sounds = [];
window.strudel = {
  soundMap: {
    get: function () {
      var out = {};
      for (var i = 0; i < window.__sounds.length; i++) { out[window.__sounds[i]] = { onTrigger: function () {} }; }
      return out;
    },
  },
};

// A hap shaped the way queryArc returns one: a value object with an "s" key.
function hap(value) { return { value: value }; }
`

// newSamplesRuntime loads the served session.js over the samples prelude.
func newSamplesRuntime(t *testing.T) *syncRuntime {
	t.Helper()
	rt := newSyncRuntime(t, samplesPrelude)
	rt.run(servedSyncJS(t))
	rt.run(servedSessionJS(t))
	rt.run(`window.__live = makeLive(); window.__sandbox = makeSandbox();`)
	return rt
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

func jsString(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(out)
}

// applyAndSettle pushes one code frame through the real decode seam and lets the
// async validate-then-commit path run to its first await.
func applyAndSettle(rt *syncRuntime, version int, code string) {
	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
	  kind: "code",
	  snapshot: { version: ` + itoa(version) + `, code: ` + jsString(code) + `, anchor: null },
	}), window.__sandbox, window.__live);`)
	rt.settle()
}

// evalBodies returns the parsed bodies of every report POSTed to path.
func evalBodies(t *testing.T, rt *syncRuntime, path string) []map[string]any {
	t.Helper()
	raw := rt.eval(`JSON.stringify(window.__fetches.filter(function (f) {
	  return f.path === ` + jsString(path) + `;
	}).map(function (f) { return f.body; }))`).String()
	var out []map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decoding recorded %s bodies %s: %v", path, raw, err)
	}
	return out
}

// lastEvalBody returns the single eval-result report, failing if there is not
// exactly one: "no report" and "two reports" are both defects, and letting them
// surface as an index-out-of-range would hide which.
func lastEvalBody(t *testing.T, rt *syncRuntime) map[string]any {
	t.Helper()
	bodies := evalBodies(t, rt, "/api/eval-result")
	if len(bodies) != 1 {
		t.Fatalf("%d eval-result reports, want exactly 1: %v", len(bodies), bodies)
	}
	return bodies[0]
}

// samplesResolved reads the tri-state off a report body. The three states are
// distinguished by PRESENCE, which is the whole point of the field: a body that
// omits it and a body carrying false are different facts and must not read the
// same to a test.
func samplesResolved(t *testing.T, body map[string]any) (present bool, value bool) {
	t.Helper()
	raw, ok := body["samplesResolved"]
	if !ok {
		return false, false
	}
	b, ok := raw.(bool)
	if !ok {
		t.Fatalf("samplesResolved is %T (%v), want a boolean or absent", raw, raw)
	}
	return true, b
}

// missingSamples reads the named unresolved sounds out of a report's stats.
func missingSamples(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["stats"]
	if !ok {
		return nil
	}
	var stats map[string]any
	switch v := raw.(type) {
	case map[string]any:
		// The prelude's fetch already ran JSON.parse, so stats arrives decoded.
		stats = v
	case string:
		if err := json.Unmarshal([]byte(v), &stats); err != nil {
			t.Fatalf("decoding stats %s: %v", raw, err)
		}
	default:
		t.Fatalf("stats is %T, want a JSON object", raw)
	}
	samples, ok := stats["samples"].(map[string]any)
	if !ok {
		return nil
	}
	list, _ := samples["missing"].([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, v.(string))
	}
	return out
}

// TestAnUnloadedSampleNameIsNotUnqualifiedSuccess is the bead's own reproduction,
// asserted on the served behaviour.
//
// Before the fix this reported ok:true with 2 haps and no further qualification,
// which is the defect: the system said SUCCESS for a sound that does not exist.
func TestAnUnloadedSampleNameIsNotUnqualifiedSuccess(t *testing.T) {
	rt := newSamplesRuntime(t)
	// Only bd is loaded; the name below is invented and registered nowhere.
	rt.run(`window.__sounds = ["bd"];`)
	rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: "totally_not_a_real_sample_xyz" })] };`)

	applyAndSettle(rt, 1, `s("totally_not_a_real_sample_xyz")`)

	body := lastEvalBody(t, rt)
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true: the pattern really did parse, and this fix must not turn a sample gap into a parse failure", body["ok"])
	}
	present, value := samplesResolved(t, body)
	if !present {
		t.Fatal("samplesResolved is ABSENT for a pattern naming an unloaded sample: an unqualified ok:true is the original defect")
	}
	if value {
		t.Error("samplesResolved = true for a name that is in no registry")
	}
	missing := missingSamples(t, body)
	if len(missing) != 1 || missing[0] != "totally_not_a_real_sample_xyz" {
		t.Errorf("stats.samples.missing = %v, want the one invented name", missing)
	}
}

// TestALoadedSampleStillReportsSuccess is the other half, and it is not optional:
// a fix that reports every sample as unresolved has merely traded a false positive
// for a false alarm, and an agent optimising against that signal would learn to
// ignore it.
func TestALoadedSampleStillReportsSuccess(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = ["bd"];`)
	rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: "bd" }), hap({ s: "bd" })] };`)

	applyAndSettle(rt, 1, `s("bd sd cp oh")`)

	body := lastEvalBody(t, rt)
	present, value := samplesResolved(t, body)
	if !present {
		t.Fatal("samplesResolved is ABSENT for a fully loaded pattern: absence must mean unknown, not resolved")
	}
	if !value {
		t.Errorf("samplesResolved = false for a pattern whose every sample IS loaded; missing = %v", missingSamples(t, body))
	}
	if missing := missingSamples(t, body); len(missing) != 0 {
		t.Errorf("stats.samples.missing = %v, want empty for a loaded pattern", missing)
	}
}

// TestSynthNamesAreNeverReportedMissing guards the false-alarm direction from the
// other side. initStrudel awaits registerSynthSounds(), so these are always
// present in a real browser whatever the user loaded.
func TestSynthNamesAreNeverReportedMissing(t *testing.T) {
	for _, name := range []string{"triangle", "square", "sawtooth", "sine"} {
		t.Run(name, func(t *testing.T) {
			rt := newSamplesRuntime(t)
			// No sample pack at all: only the always-registered synths.
			rt.run(`window.__sounds = ["triangle", "square", "sawtooth", "sine"];`)
			rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: "` + name + `" })] };`)

			applyAndSettle(rt, 1, `s("`+name+`")`)

			body := lastEvalBody(t, rt)
			if present, value := samplesResolved(t, body); !present || !value {
				t.Errorf("samplesResolved present=%v value=%v for the built-in synth %q; initStrudel always registers it",
					present, value, name)
			}
		})
	}
}

// TestSilenceAndNonStringSoundsAreSkipped pins the shapes that are NOT sample
// lookups. Each of these would otherwise be reported as a missing sound, and
// `s("bd ~ cp")` is an extremely ordinary pattern that must stay clean.
func TestSilenceAndNonStringSoundsAreSkipped(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"rest", `"~"`},
		{"tilde", `"-"`},
		{"underscore", `"_"`},
		{"empty string", `""`},
		{"number", `7`},
		{"wavetable prefix", `"wt_noise"`},
		{"null", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newSamplesRuntime(t)
			rt.run(`window.__sounds = [];`)
			rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: ` + tc.value + ` })] };`)

			applyAndSettle(rt, 1, `s("bd")`)

			body := lastEvalBody(t, rt)
			if missing := missingSamples(t, body); len(missing) != 0 {
				t.Errorf("stats.samples.missing = %v for a non-sample sound %s; these are not sample lookups", missing, tc.value)
			}
		})
	}
}

// TestAnAbsentRegistryIsUnknownNotResolved is the false-NEGATIVE trap, and it is
// why samplesResolved is a tri-state rather than a boolean.
//
// When window.strudel or its soundMap is missing — an offline CDN, a stripped
// bundle, this harness — the resolver knows NOTHING. Reporting `true` there would
// be the original defect wearing a fix's clothes: a green light over a check that
// never ran. So the field must be ABSENT, and absent must read as unknown.
func TestAnAbsentRegistryIsUnknownNotResolved(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.strudel = undefined;`)
	rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: "bd" })] };`)

	applyAndSettle(rt, 1, `s("bd")`)

	body := lastEvalBody(t, rt)
	if body["ok"] != true {
		t.Errorf("ok = %v, want true: no registry is not a parse failure", body["ok"])
	}
	if present, _ := samplesResolved(t, body); present {
		t.Error("samplesResolved was REPORTED with no registry available; a resolver that could not look must say unknown, not resolved")
	}
}

// TestTheFindingIsDrivenByTheRegistryNotTheToggle guards a specific wrong
// implementation. welcome.html's Samples button flips a label, and its OFF branch
// does not unregister anything: strudel's soundMap keeps every name it was given.
// A resolver driven by that flag would keep reporting a pack as loaded after the
// user turned it off — wrong in the permissive direction, and invisible to a human
// looking at a button that says "Off".
func TestTheFindingIsDrivenByTheRegistryNotTheToggle(t *testing.T) {
	rt := newSamplesRuntime(t)
	// The button says off; the registry has been emptied behind its back.
	rt.run(`window.__sounds = [];`)
	rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: "bd" })] };`)

	applyAndSettle(rt, 1, `s("bd")`)

	present, value := samplesResolved(t, lastEvalBody(t, rt))
	if !present || value {
		t.Errorf("samplesResolved present=%v value=%v; the registry is the source of truth, not the toggle's label", present, value)
	}
}

// TestNamesAreNormalisedTheWayTheBundleDoes pins the resolver's normalisation
// against the REAL pinned bundle's rules rather than against this file's stub,
// which is the only thing stopping the stub from quietly agreeing with a broken
// resolver.
//
// Verified against @strudel/web@1.3.0 (see the bead): registerSound applies
// `name.toLowerCase().replace(/\s+/g, "_")` before storing, and playback looks a
// name up as `soundMap.get()[name.toLowerCase()]`.
func TestNamesAreNormalisedTheWayTheBundleDoes(t *testing.T) {
	t.Run("case folded", func(t *testing.T) {
		rt := newSamplesRuntime(t)
		// Registered lowercase, as the bundle stores it.
		rt.run(`window.__sounds = ["bd"];`)
		rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: "BD" })] };`)
		applyAndSettle(rt, 1, `s("BD")`)
		if present, value := samplesResolved(t, lastEvalBody(t, rt)); !present || !value {
			t.Errorf("samplesResolved present=%v value=%v for BD against a registry holding bd; the bundle lowercases before lookup", present, value)
		}
	})

	t.Run("whitespace runs folded to underscores", func(t *testing.T) {
		rt := newSamplesRuntime(t)
		// The bundle stores a multi-word name with underscores.
		rt.run(`window.__sounds = ["bd_sd"];`)
		rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: "bd sd" })] };`)
		applyAndSettle(rt, 1, `s("bd sd")`)
		if present, value := samplesResolved(t, lastEvalBody(t, rt)); !present || !value {
			t.Errorf("samplesResolved present=%v value=%v for \"bd sd\" against a registry holding bd_sd; the bundle replaces whitespace runs with underscores", present, value)
		}
	})
}

// TestTheFindingSurvivesAFailingEvaluationWithoutInventingOne checks the
// interaction with the failure path, which is where a careless implementation
// would fabricate a second defect.
//
// A syntax error proves NOTHING about samples: the pattern never got far enough to
// name one. Emitting samplesResolved:false there would report an unresolved sound
// the agent never wrote, on top of an error that already says what is wrong. So the
// failure path reports no sample finding at all.
func TestTheFindingSurvivesAFailingEvaluationWithoutInventingOne(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = [];`)
	rt.run(`window.__sandboxBehaviour = { evalError: "Unexpected token '}'" };`)

	applyAndSettle(rt, 1, `s("bd"`)

	body := lastEvalBody(t, rt)
	if body["ok"] != false {
		t.Fatalf("ok = %v, want false for a syntax error", body["ok"])
	}
	if present, value := samplesResolved(t, body); present {
		t.Errorf("samplesResolved = %v on a SYNTAX failure; a parse error proves nothing about samples, so this is an invented finding", value)
	}
}

// TestDryRunCarriesTheSameFinding proves the dry-run path resolves identically.
// The two paths must not disagree: an agent that dry-runs a candidate and is told
// nothing, then pushes it and is told "unresolved", has been given two answers to
// one question.
//
// It also re-proves the property dry-run exists for, because a resolver added to
// that path is new code on the route to the live repl.
func TestDryRunCarriesTheSameFinding(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = ["bd"];`)
	rt.run(`window.__sandboxBehaviour = { haps: [hap({ s: "no_such_sample" })] };`)

	rt.run(`window.strudelSession.handleFrame(JSON.stringify({
	  kind: "dry-run",
	  snapshot: { version: 1, code: 's("bd")', anchor: null },
	  dryRun: { id: 5, code: 's("no_such_sample")' },
	}), window.__sandbox, window.__live);`)
	rt.settle()

	body := evalBodies(t, rt, "/api/dry-run-result")
	if len(body) != 1 {
		t.Fatalf("%d dry-run reports, want exactly 1: %v", len(body), body)
	}
	present, value := samplesResolved(t, body[0])
	if !present {
		t.Fatal("the dry-run verdict carries no samplesResolved: the two evaluation paths disagree about the same code")
	}
	if value {
		t.Error("the dry-run verdict reports samplesResolved = true for an unloaded name")
	}
	if got := rt.eval("String(window.__setPatternCalls.length)").String(); got != "0" {
		t.Errorf("the live repl was hot-swapped %v time(s) during a dry-run carrying the new resolver", got)
	}
}

// TestTheResolverIsActuallyReachedFromTheServedFile is a wiring check. Every test
// above would pass if session.js simply stopped consulting the registry and
// hardcoded a plausible answer, because they all drive it through the same seam.
// This asserts the served bytes actually reach for the registry the pinned bundle
// exports — the difference between a resolver and a decoration.
func TestTheResolverIsActuallyReachedFromTheServedFile(t *testing.T) {
	js := servedSessionJS(t)
	for _, want := range []string{"soundMap", "samplesResolved", "collectSampleReport"} {
		if !strings.Contains(js, want) {
			t.Errorf("session.js is missing %q: the sample resolver is not wired to the registry", want)
		}
	}
	// The safety property that makes resolution possible at all: the sandbox must
	// still discard triggers. Removing the stub would start a scheduler and fetch
	// over the network during validation.
	if !strings.Contains(js, "defaultOutput: async function () {}") {
		t.Error("session.js no longer stubs defaultOutput to a no-op: validation must stay free of audio, scheduling and network")
	}
}

// TestAPatternWithNoSoundsResolvesCleanly covers the ordinary musical case: a
// note-only pattern names no sample, so there is nothing to resolve and nothing to
// complain about.
func TestAPatternWithNoSoundsResolvesCleanly(t *testing.T) {
	rt := newSamplesRuntime(t)
	rt.run(`window.__sounds = [];`)
	rt.run(`window.__sandboxBehaviour = { haps: [hap({ note: 60 }), hap({ note: 62 })] };`)

	applyAndSettle(rt, 1, `note("c4 e4")`)

	body := lastEvalBody(t, rt)
	if present, value := samplesResolved(t, body); !present || !value {
		t.Errorf("samplesResolved present=%v value=%v for a pattern naming no sounds, want present and true", present, value)
	}
}
