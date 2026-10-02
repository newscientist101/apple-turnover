.PHONY: build clean stop start restart test verify mutation-check

# srv/ is a package directory, so -o srv writes the binary to srv/srv.
# -o bin/agentcli keeps the CLI's binary out of the source directories, where a
# stray executable would sit next to the package it was built from.
build:
	go build -o srv ./cmd/srv
	go build -o bin/agentcli ./cmd/agentcli

clean:
	rm -f srv/srv bin/agentcli

test:
	go test ./...

# verify is the repo-owned gate every future issue must pass. Each step is a
# separate recipe line, so make stops at the first failure and names it; there
# is no `-` and no `|| true`, so a failing step cannot be mistaken for success.
#
#   gofmt -l the whole tree, failing if it prints ANY file (an unformatted file
#             is a review smell and hides real diffs)
#   go vet    the static checks, including the ones that catch a lock copied by
#             value or a printf verb that does not match its argument
#   go build  every package/command, so the shipped binary cannot rot
#   go test   the whole suite under -race with the test cache disabled, so a
#             stale cached PASS cannot mask a regression
#
# Nothing here needs the network, a database, a real port or a service running:
# the integration harness listens on loopback only and every operation is
# bounded, so a hang is a failure rather than an infinite wait.
verify:
	@echo "==> gofmt -l ."
	@unformatted="$$(gofmt -l . 2>&1)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: these files need formatting (run: gofmt -w .), or do not parse:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi; \
	echo "gofmt: clean"
	@echo "==> go vet ./..."
	@go vet ./...
	@echo "==> go build ./..."
	@go build ./...
	@echo "==> go test ./... -race -count=1"
	@go test ./... -race -count=1
	@echo "==> verify: all checks passed"

# mutation-check confirms the test suite is not vacuous: it breaks the
# implementation on purpose, runs the tests, and requires them to fail. The
# script reverts every mutation and reports any it did not catch. See
# scripts/mutation-check.sh and the README.
#
# The outer timeout bounds the whole grid: each mutation costs one full test run
# (and the deliberate wedge/binary-search mutations cost the go test timeout),
# so the wall time grows with the number of mutations. It is ~581 seconds for
# 78 mutations, measured end to end on this machine (WALL_SECONDS=581, rc=0,
# 78 caught / 0 survived / 0 weak / 0 broken, issue strudel-agent-3vo.6, which
# added rows 74-78 for the live code view and re-anchored row 14). The previous
# figure was ~582 seconds for 69 rows (issue strudel-agent-3vo.3.7, which added
# rows 65-69 and re-anchored six older ones). The nine rows added since then
# (70-78) cost about nothing extra because every one is ROUTED to a narrow -run
# regex: of the 78 rows, 45 are routed and only 33 pay for a whole-suite run,
# and the whole-suite runs are the ones the wall time is made of.
# 2400s is still over 4x the measured figure, because a loaded machine is
# exactly the case the bound exists for. Raise it when the grid grows; do not
# lower it toward the measured figure.
mutation-check:
	timeout 2400 ./scripts/mutation-check.sh
