#!/usr/bin/env bash
#
# mutation-bench.sh — measure the per-mutation cost of the mutation grid.
#
# Produces:
#   * a TSV table (name<TAB>raw_seconds<TAB>verdict) on stdout AND in
#     docs/mutation-bench/01-baseline.tsv
#   * whole-grid and baseline timings on stderr
#   * per-mutation logs in /tmp/mutation-bench-logs/
#
# Usage:
#   timeout 2700 ./scripts/mutation-bench.sh 2>&1 | tee /tmp/bench.log
#
# Every go/test invocation is bounded by an explicit timeout. The script is
# safe to interrupt (Ctrl-C) and asserts a clean tree at the end.

set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

RESULTS_DIR="docs/mutation-bench"
mkdir -p "$RESULTS_DIR"
TSV_FILE="$RESULTS_DIR/01-baseline.tsv"
BENCH_LOG_DIR="/tmp/mutation-bench-logs"
mkdir -p "$BENCH_LOG_DIR"

trap 'echo "mutation-bench: interrupted" >&2; exit 130' INT TERM

now_ns() {
    date +%s%N
}

ns_to_secs() {
    local ns="$1"
    awk -v n="$ns" 'BEGIN { printf "%.3f", n / 1000000000 }'
}

# ---- Get mutation names ----
mapfile -t MUTATION_NAMES < <(./scripts/mutation-check.sh --list)
echo "mutation-bench: ${#MUTATION_NAMES[@]} mutations to benchmark" >&2

# ---- Step 1: Measure baseline (unmutated test run) ----
# This is the cost that every individual mutation-check.sh invocation pays
# before it even starts mutating. We measure it once so we can subtract it.
echo "mutation-bench: measuring baseline (unmutated test run)..." >&2
t0=$(now_ns)
timeout 300 go test ./... -race -count=1 -timeout 150s > "$BENCH_LOG_DIR/baseline.log" 2>&1
baseline_rc=$?
t1=$(now_ns)
baseline_secs=$(ns_to_secs $((t1 - t0)))
echo "mutation-bench: baseline = ${baseline_secs}s (exit $baseline_rc)" >&2

# ---- Step 2: Time the whole grid cold ----
echo "mutation-bench: running whole grid cold..." >&2
t0=$(now_ns)
timeout 900 ./scripts/mutation-check.sh > "$BENCH_LOG_DIR/whole-grid.log" 2>&1
whole_rc=$?
t1=$(now_ns)
whole_secs=$(ns_to_secs $((t1 - t0)))
echo "mutation-bench: whole grid = ${whole_secs}s (exit $whole_rc)" >&2

# ---- Step 3: Time each mutation individually ----
echo "mutation-bench: per-mutation timing..." >&2
printf 'name\traw_seconds\tverdict\n' | tee "$TSV_FILE"

for name in "${MUTATION_NAMES[@]}"; do
    printf '  %-42s ' "$name" >&2
    t0=$(now_ns)
    timeout 300 ./scripts/mutation-check.sh "$name" > "$BENCH_LOG_DIR/${name}.log" 2>&1
    rc=$?
    t1=$(now_ns)
    elapsed=$(ns_to_secs $((t1 - t0)))

    # Determine verdict from exit code and log content
    if [ $rc -eq 0 ]; then
        verdict="caught"
    elif [ $rc -eq 124 ]; then
        verdict="TIMEOUT"
    else
        log="$BENCH_LOG_DIR/${name}.log"
        if grep -q 'SURVIVED' "$log" 2>/dev/null; then
            verdict="SURVIVED"
        elif grep -q 'WEAK' "$log" 2>/dev/null; then
            verdict="WEAK"
        elif grep -q 'BROKEN' "$log" 2>/dev/null; then
            verdict="BROKEN"
        elif grep -q 'BASELINE IS ALREADY FAILING' "$log" 2>/dev/null; then
            verdict="BASELINE_FAIL"
        else
            verdict="error(rc=$rc)"
        fi
    fi

    printf '%s\t%s\t%s\n' "$name" "$elapsed" "$verdict" | tee -a "$TSV_FILE"
    echo "${elapsed}s [$verdict]" >&2
done

# ---- Step 4: Assert clean tree ----
echo "mutation-bench: checking git status..." >&2
# Allow only our new files: scripts/mutation-bench.sh and docs/mutation-bench/*
dirty=$(git status --porcelain 2>/dev/null \
    | grep -v '^?? docs/mutation-bench/' \
    | grep -v '^?? docs/$' \
    | grep -v '^?? scripts/mutation-bench.sh' \
    || true)
if [ -n "$dirty" ]; then
    echo "mutation-bench: ERROR: unexpected changes in working tree:" >&2
    echo "$dirty" >&2
    exit 1
fi

# ---- Summary ----
echo "" >&2
echo "mutation-bench: SUMMARY" >&2
echo "  baseline (unmutated): ${baseline_secs}s" >&2
echo "  whole grid cold:      ${whole_secs}s" >&2
echo "  results: $TSV_FILE" >&2
echo "  per-mutation logs: $BENCH_LOG_DIR/" >&2
echo "mutation-bench: done" >&2
