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
#   * expressed as a literal old->new string pair in scripts/mutations.json,
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
#   ./scripts/mutation-check.sh --lint             # validate the table only
#   ./scripts/mutation-check.sh <name>...          # selected mutations
#   MUTATION_TEST_ARGS="-run TestIntegration" \
#     ./scripts/mutation-check.sh                  # only the integration harness
#
# The last form is the one that matters when a NEW test file is added: it
# proves the new tests on their own catch the defects, rather than relying on
# the older unit tests to do the work.
#
# --lint runs no tests at all. It validates the table and checks that every
# anchor still matches its file exactly once, which is worth running before a
# grid: a typo in the table otherwise costs a full baseline run and then
# reports BROKEN, which reads as "the implementation moved" when in fact the
# table did.
#
# Any mutating run (including a single named row) happens in a throwaway git
# worktree, so this checkout is left untouched and two runs cannot poison each
# other. --list and --lint are read-only and stay in place. Set
# MUTATION_WORKTREE=0 to force in-place mutation; it announces itself.
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

# ------------------------------------------------------------------ worktree --
#
# The grid deliberately rewrites tracked source files. Doing that in the
# caller's checkout has two costs that have actually bitten this repo: an
# interrupted run can leave a mutation applied, and two concurrent runs poison
# each other's baseline (one run's mutation was still in the tree when the other
# took its baseline, and the second reported a red baseline for a tree nobody
# had broken). Neither is the caller's problem to manage by hand, so the
# mutating part of this script runs in a throwaway git worktree and the
# caller's checkout is never modified.
# The worktree is created from HEAD and then OVERLAID with the caller's current
# working-tree content, so uncommitted work — including a table that does not
# exist in any commit yet — is what actually gets mutated and tested. Testing
# HEAD instead would quietly grade something the caller did not ask about.
#
# Read-only modes (--list, --lint) touch nothing, so they stay in place and
# stay instant. MUTATION_WORKTREE=0 forces in-place mutation for the cases
# where a worktree is impossible; that fallback announces itself and is never
# silent, because a mutation left in someone's checkout is worth knowing about.

WT_PATH=""

# cleanup_worktree removes the throwaway worktree and prunes its admin entry.
# Idempotent, and safe to call from a trap on a path that was never created.
cleanup_worktree() {
	[ -n "$WT_PATH" ] || return 0
	if [ -d "$WT_PATH" ]; then
		git -C "$REPO_ROOT" worktree remove --force "$WT_PATH" >/dev/null 2>&1 ||
			rm -rf "$WT_PATH"
	fi
	git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1 || true
	WT_PATH=""
}

# overlay_worktree copies the caller's current files over the worktree checkout:
# every tracked path and every untracked-but-not-ignored one, each with its
# present content, and any path deleted in the working tree removed. Paths are
# read NUL-separated, because a filename may contain a newline.
overlay_worktree() {
	local src="$1" dst="$2" f
	while IFS= read -r -d '' f; do
		[ -e "$src/$f" ] || continue # deleted in the working tree
		mkdir -p "$dst/$(dirname "$f")"
		cp -p "$src/$f" "$dst/$f"
	done < <(git -C "$src" ls-files -co --exclude-standard -z)
	while IFS= read -r -d '' f; do
		rm -f "$dst/$f"
	done < <(git -C "$src" ls-files -d -z)
}

# run_in_worktree re-runs this same script inside the worktree and returns its
# exit status. Returns 2 to mean "no worktree; the caller should run in place".
run_in_worktree() {
	if ! git -C "$REPO_ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		echo "mutation-check: not a git checkout, so there is no worktree to isolate this run." >&2
		echo "mutation-check: falling back to IN-PLACE mutation; files will be restored on exit." >&2
		return 2
	fi
	local wt
	wt="$(mktemp -d "${TMPDIR:-/tmp}/mutation-check-wt.XXXXXX")" || return 2
	rmdir "$wt" # `git worktree add` creates the directory itself
	if ! git -C "$REPO_ROOT" worktree add --detach "$wt" HEAD >/dev/null 2>&1; then
		echo "mutation-check: could not create a git worktree at $wt." >&2
		echo "mutation-check: falling back to IN-PLACE mutation; files will be restored on exit." >&2
		rm -rf "$wt"
		return 2
	fi
	WT_PATH="$wt"
	trap 'cleanup_worktree' EXIT
	trap 'cleanup_worktree; exit 130' INT TERM

	overlay_worktree "$REPO_ROOT" "$wt" || {
		echo "mutation-check: could not overlay the working tree into $wt; discarding it." >&2
		cleanup_worktree
		return 2
	}
	if [ ! -f "$wt/scripts/mutation-check.sh" ]; then
		echo "mutation-check: the worktree is missing scripts/mutation-check.sh; discarding it." >&2
		cleanup_worktree
		return 2
	fi

	echo "mutation-check: running the grid in a throwaway worktree ($wt)"
	echo "mutation-check: $REPO_ROOT is not modified by this run."
	local rc=0
	MUTATION_IN_WORKTREE=1 "$wt/scripts/mutation-check.sh" "$@" || rc=$?
	cleanup_worktree
	return $rc
}

# Only the mutating modes need isolation. --list and --lint are read-only, and
# keeping them in place is what lets `--list` be compared against a recorded
# baseline without building a worktree to do it.
READ_ONLY_RUN=no
for arg in "$@"; do
	case "$arg" in
	--list | --lint) READ_ONLY_RUN=yes ;;
	esac
done

if [ "$READ_ONLY_RUN" = no ] && [ "${MUTATION_WORKTREE:-1}" != 0 ] &&
	[ -z "${MUTATION_IN_WORKTREE:-}" ]; then
	run_in_worktree "$@"
	wt_rc=$?
	# 2 means "no worktree available": fall through and run in place, having
	# already said so. Anything else is the grid's own verdict and is passed
	# through unchanged, so the worktree never launders an exit code.
	if [ "$wt_rc" -ne 2 ]; then
		exit "$wt_rc"
	fi
elif [ "$READ_ONLY_RUN" = no ] && [ -z "${MUTATION_IN_WORKTREE:-}" ]; then
	echo "mutation-check: MUTATION_WORKTREE=0 — mutating THIS checkout in place." >&2
fi

BACKUP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mutation-check.XXXXXX")"
trap 'restore_all; rm -rf "$BACKUP_DIR"' EXIT

# ---------------------------------------------------------------- mutations --
#
# The table is scripts/mutations.json, parsed by scripts/mutation-parse.py.
# It USED to be a pipe-delimited heredoc in this file, one row per line:
#
#     <name>|<file>|<old>|<new>|<optional -run regex>
#
# That format could not express an anchor text containing a `|`. The
# delimiter split the row, `old` was silently truncated at the bar, and the
# remainder was read as the `-run` regex. None of the outcomes was
# diagnostic: a truncated anchor either matched nothing (BROKEN, which blames
# the implementation for a typo in the table), failed to compile as a regexp
# (WEAK, which looks like a routing mistake), or -- worst -- happened to
# match exactly once, so the grid mutated the WRONG text and reported the row
# healthy. Commit 12f3a3d worked around that by re-anchoring a row onto a
# line with no pipe rather than by fixing the format.
#
# JSON removes the class of bug instead of relocating it. A literal `|` is
# just a character; a newline or tab is an ordinary escape. Nothing in an
# anchor can terminate a field, so there is no delimiter left to collide with,
# and no truncation to detect after the fact. `--lint` and the parser's own
# selftest cover the regression directly.
#
# Each row is {name, file, old, new} with an OPTIONAL run key: omit `run`
# entirely to mean "whole suite". An empty run is rejected, so "no -run" stays
# distinguishable from "".
#
# The old text must appear exactly once. Keep each mutation SMALL and tied to
# one behavioural claim, so a reviewer can see the intent.
#
# `run` narrows the test run for that one mutation (e.g. a deliberately
# WEDGED handler makes every other test block until the go test timeout, so
# hold it to the one test that is supposed to notice). It REPLACES
# MUTATION_TEST_ARGS for that mutation rather than narrowing it further, so a
# mutation with a run key ignores an outer `-run` restriction; an alternation
# such as TestA|TestB routes one mutation to several tests. Most mutations
# omit it and run the whole suite.
#
# Two rows were re-anchored when this table moved, and both had been silently
# reporting BROKEN against the current implementation rather than catching
# anything. Re-anchoring changes what a row proves, so each was re-checked to
# confirm it is still CAUGHT, not merely applied:
#
#   12  The anchor `if err := rejectUnexpectedBody(r); err != nil {` also
#       occurs in handleAPIHeartbeat, so it matched twice and the row could
#       never apply. It now includes the handleAPITransport signature, which
#       makes it unique while still disabling the same body rejection. Caught
#       by TestAgentAPIDocErrorTableMatchesTheServer.
#
#   101 The anchor named printFlaglessUsage(stderr, "state", ...), but
#       `state` grew a -require-current flag and the flagless path was
#       generalized over message/hush/play, so the text no longer exists. The
#       claim is "the help check stays narrow", which now lives in
#       wantsFlaglessHelp's `len(args) != 1` guard; widening that guard is the
#       same defect. Caught by TestFlaglessSubcommandHelpExitsZero.
#
# A BROKEN row is not a cosmetic defect: it is a row the grid cannot run, so
# the behaviour it exists to prove is currently unproven. `make mutation-lint`
# is the cheap way to notice, and it is why it checks anchors and not just
# JSON syntax.
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
# old and new arrive already unescaped: scripts/mutations.json is JSON, so a
# newline or tab in an anchor is a real character by the time it gets here.
# Nothing is decoded at this layer, which is why a literal `|` needs no
# special handling anywhere.
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

# Read the table through the parser, which validates it and emits NUL-separated
# fields. NUL is the one byte that cannot occur inside a field, so an anchor
# containing a bar, a newline or a tab is carried across intact -- there is no
# delimiter for it to collide with. The parser exits non-zero on a malformed
# row, and this script inherits that failure rather than running a grid from a
# table nobody could parse.
#
# The records go through a FILE, not a command substitution: bash silently
# strips NUL bytes from `$(...)` ("warning: command substitution: ignored null
# byte in input"), which would quietly delete every field terminator and run
# the grid off garbage. That trap is why this reads a file.
GRID_RECORDS="$BACKUP_DIR/grid.records"
if ! ./scripts/mutation-parse.py >"$GRID_RECORDS"; then
	echo "mutation-check: the mutation table is invalid (see above); not running the grid." >&2
	exit 1
fi

while IFS= read -r -d '' name &&
      IFS= read -r -d '' file &&
      IFS= read -r -d '' old &&
      IFS= read -r -d '' new &&
      IFS= read -r -d '' run &&
      IFS= read -r -d '' _; do
	names+=("$name"); files+=("$file"); olds+=("$old"); news+=("$new"); runs+=("$run")
done <"$GRID_RECORDS"

selection=()
if [ "${1:-}" = "--list" ]; then
	for n in "${names[@]}"; do echo "$n"; done
	exit 0
fi
if [ "${1:-}" = "--lint" ]; then
	# Validate rows against the tree without running a single test.
	exec ./scripts/mutation-parse.py --lint
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
