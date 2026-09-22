.PHONY: build clean stop start restart test verify mutation-check

# srv/ is a package directory, so -o srv writes the binary to srv/srv.
build:
	go build -o srv ./cmd/srv

clean:
	rm -f srv/srv

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
# so the wall time grows with the number of mutations. It is currently ~5
# minutes for 41 mutations; 900s leaves real headroom on a loaded machine while
# still turning an infinite hang into a failure. Raise it when the grid grows.
mutation-check:
	timeout 900 ./scripts/mutation-check.sh
