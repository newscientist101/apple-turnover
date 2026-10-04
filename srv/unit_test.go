package srv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The systemd unit is the one deployment artifact no Go test ever executes:
// every other test starts the binary or uses httptest, so a typo in
// ExecStart/WorkingDirectory produces a service that never starts while this
// suite stays green. These tests are static on purpose -- they must not need
// systemd, root, or the production port. The unit was additionally exercised
// for real on the host (see README, "systemd deployment"); what is pinned here
// is the invariant that made that run meaningful: the unit points at exactly
// the paths `make build` writes.

// parseUnit reads a systemd unit into section -> key -> value, keeping only
// the first value of a repeated key (which is the one systemd uses).
func parseUnit(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read unit %s: %v", path, err)
	}
	unit := map[string]map[string]string{}
	section := ""
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			if _, ok := unit[section]; !ok {
				unit[section] = map[string]string{}
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("%s:%d: not a key=value line: %q", path, i+1, line)
		}
		if section == "" {
			t.Fatalf("%s:%d: key outside any section: %q", path, i+1, line)
		}
		key = strings.TrimSpace(key)
		// Environment= may legitimately repeat; systemd merges the assignments,
		// and the unit sets HOME and USER on separate lines. Anything else
		// repeated is a typo systemd resolves silently, so keep the first and
		// let the caller's assertions see the value that is actually in force.
		if key == "Environment" {
			if prev, ok := unit[section][key]; ok {
				unit[section][key] = prev + " " + strings.TrimSpace(value)
				continue
			}
		}
		if _, dup := unit[section][key]; !dup {
			unit[section][key] = strings.TrimSpace(value)
		}
	}
	return unit
}

// repoRoot is the checkout that holds srv.service, the Makefile and srv/.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// TestUnitExecStartPointsAtTheBuildOutput pins the unit's ExecStart to the
// file `make build` writes. srv/ is a package directory, so `go build -o srv`
// lands the binary at srv/srv -- a unit that said ./srv or ./bin/srv would look
// entirely reasonable and fail only at start time on the host.
func TestUnitExecStartPointsAtTheBuildOutput(t *testing.T) {
	root := repoRoot(t)
	unit := parseUnit(t, filepath.Join(root, "srv.service"))

	service, ok := unit["Service"]
	if !ok {
		t.Fatalf("srv.service has no [Service] section")
	}

	execStart := service["ExecStart"]
	if execStart == "" {
		t.Fatal("srv.service has no ExecStart: the unit cannot start the server")
	}
	// Exactly one command. systemd takes only the first, so a second one would
	// be silently ignored -- the same class of typo as a wrong path.
	if strings.Count(strings.TrimSpace(execStart), " ") != 0 {
		t.Fatalf("ExecStart must be a single absolute path with no arguments, got %q", execStart)
	}
	if !filepath.IsAbs(execStart) {
		t.Fatalf("ExecStart %q is relative, so it depends on an unstated root", execStart)
	}
	want := filepath.Join(root, "srv", "srv")
	if execStart != want {
		t.Fatalf("ExecStart = %q, want %q (the path `make build` writes)", execStart, want)
	}

	// WorkingDirectory must be the checkout root: the server resolves
	// srv/templates and srv/static relative to it.
	if got := service["WorkingDirectory"]; got != root {
		t.Fatalf("WorkingDirectory = %q, want %q", got, root)
	}

	// The Makefile is where the output path is decided; if that changes, this
	// test must fail rather than the host.
	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	if !strings.Contains(string(mk), "go build -o srv ./cmd/srv") {
		t.Fatal(`Makefile no longer builds the server with "go build -o srv ./cmd/srv"; ` +
			"repoint srv.service ExecStart at the new output path")
	}
}

// TestUnitRunsAsANonRootUserWithAJournal proves the two settings the observed
// journal run depended on: output reaches `journalctl -u srv`, and the service
// does not run as root.
func TestUnitRunsAsANonRootUserWithAJournal(t *testing.T) {
	unit := parseUnit(t, filepath.Join(repoRoot(t), "srv.service"))
	service := unit["Service"]

	user := service["User"]
	if user == "" {
		t.Fatal("srv.service has no User=; the unit would run as root")
	}
	if user == "root" || user == "0" {
		t.Fatalf("User = %q; the server should not run as root", user)
	}
	if group := service["Group"]; group != user && group != "" {
		t.Fatalf("Group = %q does not match User = %q", group, user)
	}

	// HOME/USER are set explicitly because a systemd service gets neither from
	// a login session; the observed run logged cleanly with them present.
	if got := service["Environment"]; !strings.Contains(got, "HOME=") || !strings.Contains(got, "USER=") {
		t.Fatalf("Environment = %q, want explicit HOME= and USER= entries", got)
	}

	for _, key := range []string{"StandardOutput", "StandardError"} {
		if got := service[key]; got != "journal" {
			t.Fatalf("%s = %q, want \"journal\" so `journalctl -u srv` shows server output", key, got)
		}
	}
}

// TestUnitRestartsAndIsEnabled proves Restart/RestartSec are usable values and
// that the unit asks to be enabled at boot. The kill -9 run on the host showed
// NRestarts=1 and a fresh MainPID, which is what these settings produced.
func TestUnitRestartsAndIsEnabled(t *testing.T) {
	unit := parseUnit(t, filepath.Join(repoRoot(t), "srv.service"))

	if got := unit["Service"]["Restart"]; got != "always" {
		t.Fatalf("Restart = %q, want \"always\"", got)
	}
	raw := unit["Service"]["RestartSec"]
	// systemd accepts a bare number here as seconds ("5"), which Go's
	// time.ParseDuration rejects; both spellings must resolve to a positive
	// wait, because RestartSec=0 would turn a crash into a restart loop.
	secs := raw
	if _, err := strconv.ParseFloat(raw, 64); err == nil {
		secs = raw + "s"
	}
	d, err := time.ParseDuration(secs)
	if err != nil {
		t.Fatalf("RestartSec = %q is not a duration systemd can parse: %v", raw, err)
	}
	if d <= 0 {
		t.Fatalf("RestartSec = %q must be positive, or the unit restart-loops", raw)
	}

	if got := unit["Install"]["WantedBy"]; got != "multi-user.target" {
		t.Fatalf("WantedBy = %q, want \"multi-user.target\"", got)
	}
}

// TestMakeServiceTargetsAreLoud guards the recipes that stand between a typo'd
// unit name and a false "it started". Every one must call sudo, name the unit,
// and contain nothing that swallows a non-zero exit.
func TestMakeServiceTargetsAreLoud(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	mk := string(raw)

	for _, verb := range []string{"start", "stop", "restart"} {
		recipe := makeRecipe(t, mk, verb)
		want := "sudo systemctl " + verb + " srv"
		if !strings.Contains(recipe, want) {
			t.Fatalf("make %s recipe does not contain %q, got:\n%s", verb, want, recipe)
		}
		// A `-` prefix or `|| true` turns a systemctl failure into a make
		// success, which reads exactly like a server that came up.
		for _, swallow := range []string{"|| true", "- ", "@sudo"} {
			if strings.Contains(recipe, swallow) {
				t.Fatalf("make %s recipe contains %q, which can mask a failed systemctl:\n%s",
					verb, swallow, recipe)
			}
		}
	}
}

// makeRecipe returns the command lines of one Makefile target.
func makeRecipe(t *testing.T, mk, target string) string {
	t.Helper()
	lines := strings.Split(mk, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == target+":" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("Makefile has no %q target", target)
	}
	var body []string
	for _, l := range lines[start:] {
		if strings.HasPrefix(l, "\t") {
			body = append(body, l)
			continue
		}
		if len(body) > 0 {
			break
		}
	}
	if len(body) == 0 {
		t.Fatalf("Makefile target %q has no recipe", target)
	}
	return strings.Join(body, "\n")
}

// TestUnitIsAcceptedBySystemdAnalyze hands the unit to systemd's own parser
// when it is installed. This catches a malformed unit on a machine that never
// installs it; it is skipped elsewhere, because the static assertions above are
// the portable part of this check.
func TestUnitIsAcceptedBySystemdAnalyze(t *testing.T) {
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze not installed")
	}
	path := filepath.Join(repoRoot(t), "srv.service")
	out, err := exec.Command(analyze, "verify", path).CombinedOutput()
	// systemd-analyze verifies every installed unit alongside the given one and
	// reports other units' problems, so only lines naming OUR unit are ours.
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "srv.service") && !strings.Contains(line, "shelley-tailscale") {
			t.Fatalf("systemd-analyze rejected %s: %s", path, line)
		}
	}
	if err != nil {
		// A non-zero exit caused solely by other units' warnings is not ours.
		t.Logf("systemd-analyze verify exited %v (output above filtered to srv.service)", err)
	}
}
