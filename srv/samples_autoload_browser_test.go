package srv

// Automatic sample loading (strudel-agent-8f0.3), executed as served.
//
// welcome.html used to load the dirt-samples pack only on a human click of
// #sample-toggle-btn, and a failed load was visible only as a button label
// plus a console.warn. The design (strudel-agent-8f0.2) replaces the toggle
// with an automatic load: a single named loadSamplePack() invoked exactly
// once when window.strudelInitPromise settles, a read-only status pill, a
// visible error element, and a retry button that appears only after failure.
//
// These tests drive the SERVED / template bytes in goja — the inline script
// extracted from the real GET / body — never a Go reimplementation. That is
// the same seam TestAgentIndicatorTemplateAndStyleAreWired uses for the
// badge: asserting on the bytes the server actually ships is the only way to
// prove the page loads samples, because a Go translation of the loader would
// pass while the real page sat silent.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

const autoloadPack = "github:tidalcycles/dirt-samples"

// autoloadFailureFor pins the exact failure text the design requires. The
// message is a contract: agents and users read it, so a rewording must be a
// deliberate commit that updates this test, not drift.
func autoloadFailureFor(reason string) string {
	return "Could not load the sample pack " + autoloadPack + ": " + reason +
		". Sound stays synth-only; patterns naming dirt samples will report samplesResolved:false."
}

const autoloadBundlePrefix = "Could not start Strudel, so samples were not loaded: "
const autoloadBundleSuffix = ". Sound and verdicts are unavailable until the bundle loads."

// servedWelcomePage fetches / through the REAL handler tree, so every
// assertion below runs against the bytes a browser would receive.
func servedWelcomePage(t *testing.T) string {
	t.Helper()
	s := New()
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// servedWelcomeInlineScript extracts the LAST inline <script> block from the
// served page. The loader lives in the trailing block at the end of <body>;
// the head block (window.strudelInitPromise = window.initStrudel()) needs the
// real bundle and is not under test. Taking the last block keeps the harness
// on the loader even though both blocks are inline.
func servedWelcomeInlineScript(t *testing.T) string {
	t.Helper()
	page := servedWelcomePage(t)
	last := ""
	rest := page
	for {
		i := strings.Index(rest, "<script")
		if i < 0 {
			break
		}
		tagEnd := strings.Index(rest[i:], ">")
		if tagEnd < 0 {
			break
		}
		tag := rest[i : i+tagEnd]
		bodyStart := i + tagEnd + 1
		close := strings.Index(rest[bodyStart:], "</script>")
		if close < 0 {
			break
		}
		if !strings.Contains(tag, "src=") {
			last = rest[bodyStart : bodyStart+close]
		}
		rest = rest[bodyStart+close+len("</script>"):]
	}
	if last == "" {
		t.Fatal("served / has no inline script block to execute")
	}
	if !strings.Contains(strings.ToLower(last), "sample") {
		t.Fatal("the last inline script does not mention samples; the harness is driving the wrong block")
	}
	return last
}

// autoloadPrelude is the host surface the inline loader needs to run
// headless: a fake document serving stubs for the status/error/retry
// elements, a controllable strudelInitPromise, injectable loader fakes, and
// a recording console.warn. There is NO network and NO click: the point of
// the feature is that no click is needed.
const autoloadPrelude = `
var window = {};
var __els = {};
function __makeEl(id) {
  var el = {
    __id: id,
    textContent: "",
    hidden: false,
    __handlers: {},
    addEventListener: function (name, fn) {
      this.__handlers[name] = this.__handlers[name] || [];
      this.__handlers[name].push(fn);
    },
    __click: function () {
      var hs = this.__handlers.click || [];
      for (var i = 0; i < hs.length; i++) { hs[i](); }
    },
  };
  return el;
}
__els["samples-status"] = __makeEl("samples-status");
__els["samples-error"] = __makeEl("samples-error");
__els["samples-retry"] = __makeEl("samples-retry");
__els["samples-retry"].hidden = true;
__els["play-btn"] = __makeEl("play-btn");
var __domHandlers = {};
var document = {
  addEventListener: function (name, fn) {
    (__domHandlers[name] = __domHandlers[name] || []).push(fn);
  },
  getElementById: function (id) { return __els[id] || null; },
};
window.__warns = [];
var console = {
  warn: function () {
    window.__warns.push(Array.prototype.slice.call(arguments).map(String).join(" "));
  },
  log: function () {},
  info: function () {},
  error: function () {},
  debug: function () {},
};
// The collaborators under test. __loaderMode is resolve|reject|pending and
// every loader path (window.samples, window.strudel.samples,
// repl.evaluate) funnels through __loaderResult, so a mode flip changes all
// three at once.
window.__sampleCalls = [];
window.__evalCalls = [];
window.__loaderMode = "resolve";
window.__loaderReason = "boom";
window.__loaderResult = function () {
  if (window.__loaderMode === "reject") { return Promise.reject(new Error(window.__loaderReason)); }
  if (window.__loaderMode === "pending") { return new Promise(function () {}); }
  return Promise.resolve();
};
window.samples = function (pack) {
  window.__sampleCalls.push(pack);
  return window.__loaderResult();
};
window.__replStub = {
  evaluate: function (code) {
    window.__evalCalls.push(code);
    return window.__loaderResult();
  },
};
window.__replBareStub = {};
window.__resolveInit = null;
window.__rejectInit = null;
window.strudelInitPromise = new Promise(function (res, rej) {
  window.__resolveInit = res;
  window.__rejectInit = rej;
});
// Observation helpers: what would a viewer (or an assistive technology)
// actually see.
window.__statusText = function () { return __els["samples-status"].textContent; };
window.__errorText = function () { return __els["samples-error"].textContent; };
window.__retryHidden = function () { return !!__els["samples-retry"].hidden; };
window.__clickRetry = function () { __els["samples-retry"].__click(); };
window.__sampleCallCount = function () { return window.__sampleCalls.length; };
`

// autoloadRuntime loads the prelude plus the served inline script into a
// fresh goja runtime. With withInitPromise=false the controllable promise is
// deleted before the script runs, modelling a bundle <script> tag that never
// ran — the script must fail loudly, never throw.
type autoloadRuntime struct {
	t  *testing.T
	vm *goja.Runtime
}

func newAutoloadRuntime(t *testing.T, withInitPromise bool) *autoloadRuntime {
	t.Helper()
	vm := goja.New()
	runJS(t, vm, autoloadPrelude, "the autoload prelude")
	if !withInitPromise {
		runJS(t, vm, `delete window.strudelInitPromise;`, "removing the init promise")
	}
	runJS(t, vm, servedWelcomeInlineScript(t), "the served inline loader")
	return &autoloadRuntime{t: t, vm: vm}
}

func (r *autoloadRuntime) run(expr string) {
	r.t.Helper()
	runJS(r.t, r.vm, expr, "the expression "+expr)
}

func (r *autoloadRuntime) eval(expr string) goja.Value {
	r.t.Helper()
	return runJS(r.t, r.vm, expr, "the expression "+expr)
}

// settle drains the microtask queue. goja runs promise jobs as the runtime
// goes idle, so a few no-op evaluations flush the whole chain: init resolve
// -> then(loadSamplePack) -> loader promise -> then/catch. Eight pumps bound
// the wait without sleeping.
func (r *autoloadRuntime) settle() {
	r.t.Helper()
	for i := 0; i < 8; i++ {
		r.run("0")
	}
}

func (r *autoloadRuntime) statusText() string {
	r.t.Helper()
	return r.eval(`window.__statusText()`).String()
}

func (r *autoloadRuntime) errorText() string {
	r.t.Helper()
	return r.eval(`window.__errorText()`).String()
}

func (r *autoloadRuntime) retryHidden() bool {
	r.t.Helper()
	return r.eval(`window.__retryHidden()`).ToBoolean()
}

func (r *autoloadRuntime) sampleCallCount() int64 {
	r.t.Helper()
	return r.eval(`window.__sampleCallCount()`).ToInteger()
}

func (r *autoloadRuntime) warns() string {
	r.t.Helper()
	return r.eval(`window.__warns.join("\n")`).String()
}

// TestAutoLoadIsAttemptedOnceOnStartup is the behaviour the feature exists
// for: resolving the init promise must attempt the pack load exactly once,
// with no click dispatched anywhere.
func TestAutoLoadIsAttemptedOnceOnStartup(t *testing.T) {
	rt := newAutoloadRuntime(t, true)
	rt.run(`window.__resolveInit(window.__replStub)`)
	rt.settle()
	if got := rt.sampleCallCount(); got != 1 {
		t.Fatalf("loader calls after init resolve = %d, want exactly 1 (no click was dispatched)", got)
	}
	if got := rt.eval(`JSON.stringify(window.__sampleCalls)`).String(); got != `["`+autoloadPack+`"]` {
		t.Fatalf("loader called with %s, want exactly [%q]", got, autoloadPack)
	}
}

// TestAutoLoadSuccessUpdatesSampleState pins the success sink: the promise
// resolving flips the pill to On, clears any error, and keeps retry hidden —
// and settling further never issues a second load.
func TestAutoLoadSuccessUpdatesSampleState(t *testing.T) {
	rt := newAutoloadRuntime(t, true)
	rt.run(`window.__resolveInit(window.__replStub)`)
	rt.settle()
	if got := rt.statusText(); got != "Samples: On" {
		t.Errorf("status after successful load = %q, want %q", got, "Samples: On")
	}
	if got := rt.errorText(); got != "" {
		t.Errorf("error after successful load = %q, want empty", got)
	}
	if !rt.retryHidden() {
		t.Error("retry is visible after a successful load; it must stay hidden until a failure")
	}
	rt.settle()
	if got := rt.sampleCallCount(); got != 1 {
		t.Errorf("loader calls after settling = %d, want 1: success must not re-trigger the load", got)
	}
}

// TestAutoLoadFailureRendersTheErrorElement pins the failure sink: a
// rejected load must write the exact error text into the alert element, flip
// the pill to Error, keep the console.warn secondary cue, and reveal retry.
func TestAutoLoadFailureRendersTheErrorElement(t *testing.T) {
	rt := newAutoloadRuntime(t, true)
	rt.run(`window.__loaderMode = "reject"`)
	rt.run(`window.__resolveInit(window.__replStub)`)
	rt.settle()
	want := autoloadFailureFor("boom")
	if got := rt.errorText(); got != want {
		t.Errorf("error element = %q, want %q", got, want)
	}
	if got := rt.statusText(); got != "Samples: Error" {
		t.Errorf("status after failed load = %q, want %q", got, "Samples: Error")
	}
	if warns := rt.warns(); !strings.Contains(warns, "Failed to load sample pack:") {
		t.Errorf("console.warn was not called with the pack failure; warns = %q", warns)
	}
	if rt.retryHidden() {
		t.Error("retry stayed hidden after a failed load; the user would be stranded with no recourse short of reload")
	}
}

// TestAutoLoadNeverSwallowsAFailure guards the warn-only regression: a
// failing loader must leave the error element non-empty, and the rejection
// path must funnel through the single failure sink rather than ending at
// console.warn.
func TestAutoLoadNeverSwallowsAFailure(t *testing.T) {
	rt := newAutoloadRuntime(t, true)
	rt.run(`window.__loaderMode = "reject"`)
	rt.run(`window.__resolveInit(window.__replStub)`)
	rt.settle()
	if got := rt.errorText(); got == "" {
		t.Fatal("loader rejected but the error element stayed empty: the failure is console-only again")
	}
	script := servedWelcomeInlineScript(t)
	if !strings.Contains(script, "showSamplesFailure") {
		t.Error("served loader has no showSamplesFailure sink: every rejection path must funnel through one visible failure sink")
	}
	if !strings.Contains(script, "samples-error") {
		t.Error("served loader never references the samples-error element: a warn-only catch would pass silently")
	}
}

// TestAutoLoadDoesNotDoubleLoad pins the once-guard in both directions:
// clicks while loading or after success are dropped, a retry after failure
// loads exactly once more, and that success clears the error.
func TestAutoLoadDoesNotDoubleLoad(t *testing.T) {
	rt := newAutoloadRuntime(t, true)
	rt.run(`window.__loaderMode = "pending"`)
	rt.run(`window.__resolveInit(window.__replStub)`)
	rt.settle()
	if got := rt.sampleCallCount(); got != 1 {
		t.Fatalf("loader calls after init resolve = %d, want 1", got)
	}
	// While the first attempt is still in flight the pill must read Loading,
	// not On: reporting loaded before the promise resolves is the defect
	// mutation autoload-reports-loaded-early reintroduces.
	if got := rt.statusText(); got != "Samples: Loading\u2026" {
		t.Errorf("status while load pending = %q, want %q", got, "Samples: Loading\u2026")
	}
	if got := rt.errorText(); got != "" {
		t.Errorf("error while load pending = %q, want empty (pending is not failure)", got)
	}
	rt.run(`window.__clickRetry()`)
	rt.settle()
	if got := rt.sampleCallCount(); got != 1 {
		t.Errorf("loader calls after retry-while-loading = %d, want 1: concurrent attempts must be dropped", got)
	}
	// Resolving the init promise again must not load again either.
	rt.run(`window.__resolveInit(window.__replStub)`)
	rt.settle()
	if got := rt.sampleCallCount(); got != 1 {
		t.Errorf("loader calls after second init resolve = %d, want 1", got)
	}

	// Failure, then retry: exactly one more attempt, and its success clears
	// the error and hides retry again. A fresh runtime is used because the
	// pending loader above never settles by construction.
	rt2 := newAutoloadRuntime(t, true)
	rt2.run(`window.__loaderMode = "reject"`)
	rt2.run(`window.__resolveInit(window.__replStub)`)
	rt2.settle()
	if got := rt2.sampleCallCount(); got != 1 {
		t.Fatalf("loader calls after failed attempt = %d, want 1", got)
	}
	if rt2.retryHidden() {
		t.Fatal("retry stayed hidden after failure; the retry half of this test cannot run")
	}
	rt2.run(`window.__clickRetry()`)
	rt2.settle()
	if got := rt2.sampleCallCount(); got != 2 {
		t.Errorf("loader calls after one retry click = %d, want 2: retry must load exactly once more", got)
	}
	rt2.run(`window.__loaderMode = "resolve"`)
	rt2.run(`window.__clickRetry()`)
	rt2.settle()
	if got := rt2.sampleCallCount(); got != 3 {
		t.Errorf("loader calls after second retry click = %d, want 3", got)
	}
	if got := rt2.statusText(); got != "Samples: On" {
		t.Errorf("status after retry success = %q, want %q", got, "Samples: On")
	}
	if got := rt2.errorText(); got != "" {
		t.Errorf("error after retry success = %q, want empty", got)
	}
	if !rt2.retryHidden() {
		t.Error("retry still visible after retry success; recovery must hide it again")
	}
	// And success is terminal: further retry clicks are dropped.
	rt2.run(`window.__clickRetry()`)
	rt2.settle()
	if got := rt2.sampleCallCount(); got != 3 {
		t.Errorf("loader calls after post-success retry click = %d, want 3", got)
	}
}

// TestAutoLoadMissingBundleFailsLoudly pins the bundle-absent fallback: with
// no strudelInitPromise the script must run without throwing, name the
// bundle in the error element, and never call the loader. The rejection
// variant asserts the same sink carries the rejection reason.
func TestAutoLoadMissingBundleFailsLoudly(t *testing.T) {
	t.Run("undefined promise", func(t *testing.T) {
		rt := newAutoloadRuntime(t, false)
		rt.settle()
		got := rt.errorText()
		if !strings.HasPrefix(got, autoloadBundlePrefix) || !strings.HasSuffix(got, autoloadBundleSuffix) {
			t.Errorf("error element = %q, want the bundle-specific message", got)
		}
		if got := rt.statusText(); got != "Samples: Error" {
			t.Errorf("status with missing bundle = %q, want %q", got, "Samples: Error")
		}
		if got := rt.sampleCallCount(); got != 0 {
			t.Errorf("loader calls with missing bundle = %d, want 0", got)
		}
	})
	t.Run("rejected promise", func(t *testing.T) {
		rt := newAutoloadRuntime(t, true)
		rt.run(`window.__rejectInit(new Error("cdn down"))`)
		rt.settle()
		want := autoloadBundlePrefix + "cdn down" + autoloadBundleSuffix
		if got := rt.errorText(); got != want {
			t.Errorf("error element = %q, want %q", got, want)
		}
		if got := rt.sampleCallCount(); got != 0 {
			t.Errorf("loader calls after init rejection = %d, want 0", got)
		}
	})
}

// TestAutoLoadMissingLoaderFailsLoudly pins the loader-absent fallback: init
// resolves but no loader exists anywhere, so the error must name the reason
// instead of failing silently. The complements prove the chain still prefers
// window.samples, then window.strudel.samples, then repl.evaluate.
func TestAutoLoadMissingLoaderFailsLoudly(t *testing.T) {
	t.Run("no loader anywhere", func(t *testing.T) {
		rt := newAutoloadRuntime(t, true)
		rt.run(`delete window.samples; window.strudel = {};`)
		rt.run(`window.__resolveInit(window.__replBareStub)`)
		rt.settle()
		want := autoloadFailureFor("Samples function unavailable")
		if got := rt.errorText(); got != want {
			t.Errorf("error element = %q, want %q", got, want)
		}
		if got := rt.statusText(); got != "Samples: Error" {
			t.Errorf("status with no loader = %q, want %q", got, "Samples: Error")
		}
	})
	t.Run("repl evaluate fallback", func(t *testing.T) {
		rt := newAutoloadRuntime(t, true)
		rt.run(`delete window.samples; window.strudel = {};`)
		rt.run(`window.__resolveInit(window.__replStub)`)
		rt.settle()
		if got := rt.eval(`window.__evalCalls.length`).ToInteger(); got != 1 {
			t.Fatalf("repl.evaluate calls = %d, want 1: the repl fallback must still load", got)
		}
		if got := rt.statusText(); got != "Samples: On" {
			t.Errorf("status via repl fallback = %q, want %q", got, "Samples: On")
		}
	})
	t.Run("strudel.samples fallback", func(t *testing.T) {
		rt := newAutoloadRuntime(t, true)
		rt.run(`delete window.samples; window.strudel = { samples: function (pack) { window.__sampleCalls.push(pack); return window.__loaderResult(); } };`)
		rt.run(`window.__resolveInit(window.__replStub)`)
		rt.settle()
		if got := rt.sampleCallCount(); got != 1 {
			t.Fatalf("strudel.samples calls = %d, want 1", got)
		}
		if got := rt.statusText(); got != "Samples: On" {
			t.Errorf("status via strudel.samples = %q, want %q", got, "Samples: On")
		}
	})
}

// TestAutoLoadToggleIsGone pins the button fate: the dishonest toggle (its
// off branch unloaded nothing) is gone entirely, replaced by the read-only
// pill, the alert element, and the recovery retry — in the served markup and
// in the served stylesheet, which must not keep orphaned toggle rules.
func TestAutoLoadToggleIsGone(t *testing.T) {
	page := servedWelcomePage(t)
	if strings.Contains(page, "sample-toggle-btn") {
		t.Error("served / still contains sample-toggle-btn: the toggle must be removed entirely, including its lying off branch")
	}
	if strings.Contains(page, "Samples: Off") {
		t.Error(`served / still contains "Samples: Off": no control may imply the pack can be unloaded`)
	}
	for _, id := range []string{"samples-status", "samples-error", "samples-retry"} {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("served / has no element with id %q", id)
		}
	}
	if !strings.Contains(page, "Samples: Loading\u2026") {
		t.Error("served / does not start the pill at Samples: Loading\u2026: auto-load starts at startup, not Off")
	}

	s := New()
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/style.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/style.css = %d, want 200", rec.Code)
	}
	css := rec.Body.String()
	if strings.Contains(css, "sample-toggle-btn") {
		t.Error("style.css still carries .sample-toggle-btn rules orphaned by the toggle's removal")
	}
	for _, sel := range []string{".samples-status", ".samples-error", ".samples-retry"} {
		if !strings.Contains(css, sel) {
			t.Errorf("style.css has no %s rule: the new element would render unstyled", sel)
		}
	}
}
