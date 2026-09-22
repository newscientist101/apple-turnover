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
# timeout, so hold it to the one test that is supposed to notice). Most
# mutations leave it empty and run the whole suite.
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
14-shell-render-silently-truncates|srv/server.go|s.renderTemplate(w, "welcome.html", data)|s.renderTemplate(w, "welcome.html", struct{ Hostname string }{data.Hostname})
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
29-hub-unsubscribe-does-not-close|srv/hub.go|close(sub.ch)\n\tdelete(subs, sub)\n\treturn true|delete(subs, sub)\n\treturn true
30-hub-slow-client-blocks|srv/hub.go|select {\n\tcase sub.ch <- msg:\n\t\treturn true\n\tdefault:\n\t\treturn false\n\t}|sub.ch <- msg\n\treturn true
31-hub-broadcast-skips-a-subscriber|srv/hub.go|case msg := <-h.broadcast:\n\t\t\tfor sub := range subs {\n\t\t\t\tif !h.deliver(sub, msg) {|case msg := <-h.broadcast:\n\t\t\tskipFirst := true\n\t\t\tfor sub := range subs {\n\t\t\t\tif skipFirst {\n\t\t\t\t\tskipFirst = false\n\t\t\t\t\tcontinue\n\t\t\t\t}\n\t\t\t\tif !h.deliver(sub, msg) {
32-hub-count-escapes-the-hub-goroutine|srv/hub.go|case reply := <-h.count:\n\t\t\treply <- len(subs)|case reply := <-h.count:\n\t\t\tgo func() { reply <- len(subs) }()
33-hub-dropped-client-not-removed|srv/hub.go|// hub down. removeSubscriber closes its channel exactly once.\n\t\t\t\t\tremoveSubscriber(subs, sub)|// hub down. removeSubscriber closes its channel exactly once.\n\t\t\t\t\t_ = sub
34-hub-removal-not-recorded|srv/hub.go|close(sub.ch)\n\tdelete(subs, sub)\n\treturn true|close(sub.ch)\n\treturn true
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
