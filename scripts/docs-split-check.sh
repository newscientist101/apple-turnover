#!/usr/bin/env bash
# docs-split-check.sh — prove the README/AGENTS.md split holds (epic strudel-agent-48k).
#
# README.md carries what a user needs (build, run, API, systemd, state, file
# map); AGENTS.md carries what an agent building the project needs
# (architecture invariants, verification conventions, the mutation grid).
# PASS requires all four halves:
#   (A) every MOVE item is greppable in AGENTS.md  (relocated, not lost)
#   (B) every MOVE item is GONE from README.md     (actually moved, not copied)
#   (C) every DELETE item is gone from README.md and justified
#   (D) every KEEP item is still in README.md      (not over-cut)
# Prose is line-wrapped, so all matching runs against a newline-stripped copy.
#
# Run: ./scripts/docs-split-check.sh (from the repo root; also wired into
# `make verify`, per the "extend the gate, don't write a throwaway script"
# convention this very script enforces).
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 2
A=AGENTS.md; R=README.md
red(){ tr "\n" " " < "$1" | tr -s " "; }
RA="$(red "$A")"; RR="$(red "$R")"
fail=0
move(){ # name pattern
  local n="$1" p="$2" inA=0 inR=1
  echo "$RA" | grep -qiE "$p" && inA=1
  echo "$RR" | grep -qiE "$p" && inR=0
  if   [ $inA -eq 1 ] && [ $inR -eq 1 ]; then printf '  MOVED  %s\n' "$n"
  elif [ $inA -eq 0 ] && [ $inR -eq 1 ]; then printf '  LOST   %s  (in neither file!)\n' "$n"; fail=1
  elif [ $inA -eq 1 ] && [ $inR -eq 0 ]; then printf '  COPIED %s  (in AGENTS.md but still in README)\n' "$n"; fail=1
  else printf '  UNMOVED %s  (still only in README)\n' "$n"; fail=1; fi
}
del(){ local n="$1" p="$2"
  if echo "$RR" | grep -qiE "$p"; then printf '  STILL-THERE %s\n' "$n"; fail=1
  else printf '  DELETED %s\n' "$n"; fi; }
keep(){ local n="$1" p="$2"
  if echo "$RR" | grep -qiE "$p"; then printf '  KEPT    %s\n' "$n"
  else printf '  OVERCUT %s\n' "$n"; fail=1; fi; }

echo "== (A/B) MOVE -> AGENTS.md =="
move "harness boots real handler tree"  "boots the .{0,3}real.{0,3} handler tree|Server\.routes\(\), the same one"
move "assert bodies not status"          "bodies.{0,20}not just status codes"
move "413 not 400"                       "413, not 400"
move "64 KiB payload cap"                "64 KiB .APIMaxBodyBytes. payload cap"
move "versions exactly 1..N"             "exactly .1\.\.N"
move "end-of-document markers"           "end-of-document markers"
move "426/400/501 split"                 "Sec-WebSocket-Version"
move "501 unhijackable writer"           "unhijackable|cannot be hijacked"
move "403 cross-origin"                  "cross-origin"
move "1 MiB / 1 KiB wedged listener"     "1 MiB frame through a 1 KiB receive buffer"
move "clean 1000 on hub shutdown"        "clean 1000 on hub shutdown"
move "1001 after hub gone"               "1001 for a listener arriving after"
move "double close panics / mutation 28" "28-hub-double-close-allowed"
move "mutation 30 + 10s watchdog"        "30-hub-slow-client-blocks"
move "hanging test is a defect"          "hanging test is itself a"
move "three independent bounds"          "three independent bounds"
move "wg.Wait watchdog"                  "wg\.Wait"
move "defer ts.Close trap"               "defer ts.Close"
move "~12s verified / mutation 23"       "23-wedged-state-handler"
move "extend these files not a script"   "extend these.{0,10}files|one-off script"
move "python3 no sed trap"               "no sed portability trap"
move "sha256 byte-identical revert"      "sha256"
move "EXIT trap on Ctrl-C"               "EXIT. trap"
move "weak evidence not a catch"         "weak evidence"
move "4 mutation-check commands"         "04-payload-cap-removed"
move "MUTATION_TEST_ARGS subset"         "MUTATION_TEST_ARGS"
move "--list mutation names"             "mutation-check.sh --list"
move "run verify before every commit"    "before every commit"
move "extend verify not throwaway"       "throwaway script"
move "bounded so a hang fails"           "hang fails rather than waits|hang fails instead of hanging"
move "hub: one goroutine owns subs"      "one goroutine (exclusively )?owns the subscriber set"
move "hub: drop slow + close exactly once" "slow subscribers are dropped|closed exactly once"
move "ws: CloseRead cancelled ctx"       "CloseRead"
move "ws: per-frame write deadline"      "per-frame write deadline"
move "ws: unsubscribe every exit path"   "unsubscribe.? on every exit path"
# the -o srv parenthetical is agent-only build rationale; the bare "make build writes srv/srv" is user-facing and stays
move "-o srv binary-path rationale"     "go build -o srv. places the binary inside the .srv|package directory, .go build -o srv"
keep "make build writes srv/srv"        "make build. writes .srv/srv"
keep "-listen flag"                     "\-listen"
move "not yet wired list"                "no snapshot on connect"
move "SetListenerCount no prod caller"   "SetListenerCount. has no production caller|serialises as 0"

echo "== (C) DELETE from README =="
del "SQLite/visitors replaced paragraph"  "SQLite/visitors machinery|visitors view counter"
del "modernc.org/sqlite"                  "modernc.org/sqlite"
del "sqlc generated code/migrations"      "sqlc generated code"
del "templates 'still the template page'"  "still the template.s page"
del "static 'still the template assets'"   "still the template.s assets"
# The '## Authorization' section was template boilerplate about exe.dev's proxy
# and was deliberately removed from README by owner decision, not relocated.
del "Authorization proxy headers"          "## Authorization"
del "X-ExeDev-UserID header"               "X-ExeDev-UserID"
# The Go module was renamed off the template's srv.exe.dev path when epic
# strudel-agent-rka closed, so that identity cannot drift back into user-facing
# docs. (docs/mutation-bench/ still quotes the old name: those are recorded
# measurements taken at a named commit, not live configuration.)
del "old exe.dev module path"              "srv\.exe\.dev"

echo "== (D) KEEP in README =="
keep "title Strudel Agent"      "^# Strudel Agent|Strudel Agent"
keep "live multi-user perf"     "multi-user algorithmic music performance"
keep "browser side not built"   "browser side is not built yet"
keep "make build"               "make build"
keep "run ./srv/srv"            "srv/srv"
keep ":8000 default"            ":8000"
keep "make verify named"        "make verify"
keep "systemd cp srv.service"   "cp srv.service /etc/systemd/system"
keep "daemon-reload"            "daemon-reload"
keep "enable --now"             "enable --now"
keep "journalctl -u srv -f"     "journalctl -u srv -f"
keep "restart after changes"    "make build && sudo systemctl restart srv"

keep "no database"              "There is no database"
keep "timeline anchor"          "timeline anchor"
keep "bounded history"          "bounded history"
keep "playing flag"             "playing flag"
keep "listener count"           "listener count"
keep "no set saving"            "no set saving"
keep "cmd/srv"                  "cmd/srv"
keep "srv/server.go"            "srv/server.go"
keep "srv/api.go"               "srv/api.go"
keep "srv/conductor.go"         "srv/conductor.go"
keep "srv/hub.go"               "srv/hub.go"
keep "srv/ws.go"                "srv/ws.go"
keep "integration_test.go"      "srv/integration_test.go"
keep "srv/templates"            "srv/templates"
keep "srv/static"               "srv/static"
keep "mutation-check.sh"        "scripts/mutation-check.sh"
keep "mutation-bench"           "mutation-bench"

echo
echo "README: $(wc -w < $R) words / $(wc -l < $R) lines"
echo "AGENTS: $(wc -w < $A) words / $(wc -l < $A) lines"
echo "issue IDs left in README: $(grep -cE 'issues \.|issue \.|\.3\.[0-9]' $R)"
[ $fail -eq 0 ] && echo "RESULT: GREEN" || echo "RESULT: RED"
exit $fail
