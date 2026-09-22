# Go Shelley Template

This app is a live, multi-user algorithmic music performance. A Go server acts
as the conductor holding the single live performance state, and an external AI
agent drives it by pushing strudel code over HTTP; browsers subscribe over
WebSocket and hot-swap the audio.

## Building and Running

Build with `make build`, then run `./srv/srv`. (Because `srv` is a directory, `go build -o srv` places the binary inside it, at `srv/srv`.) The server listens on port 8000 by default.

## Verification

`make verify` is the repo-owned gate. Run it before every commit, and extend it
rather than writing a throwaway script when a new feature needs proving. It runs:

1. `gofmt -l .` — fails if it prints **any** file.
2. `go vet ./...`
3. `go build ./...`
4. `go test ./... -race -count=1`

Each step is a separate recipe line, so `make` stops at the first failure and
names it; there is no `-` and no `|| true` anywhere in the target, so a failing
step cannot be mistaken for success. Nothing needs the network, a database, a
real port or a running service — the integration harness uses `httptest` and
loopback listeners only, and every operation is bounded, so a hang fails rather
than waits.

### The integration harness

`srv/integration_test.go` boots the **real** handler tree (`Server.routes()`, the
same one `Server.Serve` mounts) and drives it the way the external agent does:

* the full feedback loop — `GET /api/state` (fresh) → `POST /api/code` →
  `POST /api/message` → `POST /api/eval-result` → `GET /api/state` — asserting on
  response **bodies**, not just status codes;
* the whole error/edge matrix as table-driven cases (empty/whitespace code,
  unknown JSON field, trailing JSON, malformed JSON, empty body, missing
  `Content-Type`, unknown/zero/negative version, stale-but-accepted eval report,
  transport bodies, 405 + `Allow`, JSON 404s under `/api`, and the 64 KiB
  payload cap including the oversized-**and**-malformed body that must be 413,
  not 400);
* a concurrency test hammering parallel pushes over a real loopback server and
  requiring the version numbers to be exactly `1..N` (contiguous, unique, no
  lost updates), bounded by a context deadline, per-transport timeouts and a
  watchdog so it can never hang;
* the non-API surface: `GET /` renders to completion (asserting on
  end-of-document markers, which is what catches an `html/template` render that
  aborts mid-document while `HandleRoot` still returns 200), `/static/` still
  serves, and a non-`/api` 404 stays net/http's plain text;
* the WebSocket upgrade path (`srv/ws_test.go`), over both a real loopback
  listener and the in-process recorder: a genuine handshake is answered 101 and
  then closed with a 1000 normal-closure frame, a non-GET verb on `/ws` is 405
  with an `Allow` header, a handshake-less GET of `/ws` is a clear 426 (400 for
  a bad `Sec-WebSocket-Version`, 501 for a writer that cannot be hijacked)
  rather than a panic, the cross-origin handshake is still refused with 403, and
  `/ws` matches exactly one path (`/ws/`, `/ws/extra` and `/api/ws` keep their
  pre-existing 404 shapes). Every dial and read carries an explicit deadline, so
  a wedged handler fails the test instead of hanging it;
* the hub core (`srv/hub_test.go`, issue `.3.3`): one broadcast reaches every
  subscriber with byte-identical payloads, an unsubscribed (or dropped)
  subscriber stops receiving and has its channel closed exactly once (a double
  close panics, so the mutation `28-hub-double-close-allowed` is caught that
  way), concurrent subscribe/unsubscribe/broadcast churn is clean under `-race`
  including concurrent `SubscriberCount` polling, and a non-reading subscriber
  cannot block the fan-out (`30-hub-slow-client-blocks` wedges the hub and is
  reported as a failure after a 10s watchdog instead of hanging);
* the shipped binary: `cmd/srv` is built and run on a loopback port and the loop
  is re-driven through the real process.

Boundedness is a requirement, not a nicety, because a hanging test is itself a
defect. The parallel test therefore has three independent bounds (a context
deadline on every request, a per-transport response-header timeout plus a client
timeout, and a watchdog that waits on a channel rather than on `wg.Wait()`), and
it closes its `httptest.Server` with a timeout on a goroutine rather than
`defer ts.Close()` — `Close` waits for outstanding requests, so a deferred call
would itself hang on a wedged handler. That was verified against a deliberately
deadlocked handler: the test fails in ~12s instead of hanging (mutation
`23-wedged-state-handler`).

Future issues (the WebSocket hub, the browser client, multi-client coherence)
should **extend this file** with their own cases instead of adding a one-off
script.

### Proving the tests are not vacuous

A green test run proves nothing on its own — a test that asserts nothing passes
just as loudly as a real one. `make mutation-check`
(`scripts/mutation-check.sh`) introduces small deliberate defects into the
implementation, runs the suite after each, and **requires** it to fail:

```bash
make mutation-check                             # all mutations, whole suite
MUTATION_TEST_ARGS="-run TestIntegration" \
  ./scripts/mutation-check.sh                   # hold only the integration harness to account
./scripts/mutation-check.sh --list              # list mutation names
./scripts/mutation-check.sh 04-payload-cap-removed   # run one
```

Each mutation is applied with `python3` (no sed portability trap), verified to
have actually changed the file, reverted, and checked byte-identical against a
recorded sha256 — including on Ctrl-C, because the revert runs from an `EXIT`
trap as well as from the loop. Anything other than a **failing test** is
reported as weak evidence rather than a catch, so a mutation that merely fails
to compile cannot masquerade as a passing check; a mutation that survives is
reported and the script exits non-zero. Each test run is bounded by
`MUTATION_TIMEOUT` (default 180s) and `go test -timeout` (default 150s), and
`make mutation-check` wraps the whole thing in `timeout 300`, so a hang is a
failure rather than a wait.

A mutation entry may carry an optional fifth field narrowing the test run for
that one mutation (`-run <regex>`) — needed by the deliberately wedged handler,
which would otherwise make every other test block until the `go test` timeout.

## Running as a systemd service

To run the server as a systemd service:

```bash
# Install the service file
sudo cp srv.service /etc/systemd/system/srv.service

# Reload systemd and enable the service
sudo systemctl daemon-reload
sudo systemctl enable srv.service

# Start the service
sudo systemctl start srv

# Check status
systemctl status srv

# View logs
journalctl -u srv -f
```

To restart after code changes:

```bash
make build
sudo systemctl restart srv
```

## Authorization

exe.dev provides authorization headers and login/logout links
that this template uses.

When proxied through exed, requests will include `X-ExeDev-UserID` and
`X-ExeDev-Email` if the user is authenticated via exe.dev.

## State

There is no database. The performance is live and ephemeral: one in-memory
`Conductor` (`srv/conductor.go`) owns the current strudel code document, a
monotonically increasing version, the shared timeline anchor (epoch ms + cps),
the last agent message, and a bounded history of recent code versions. Nothing
is persisted, and there is no set saving.

The template's SQLite/visitors machinery was replaced by this in-memory core:
the visitors view counter and the `db` package (sqlc generated code, migrations,
queries, and the `modernc.org/sqlite` dependency) are gone.

## Code layout

- `cmd/srv`: main package (binary entrypoint)
- `srv`: HTTP server logic (handlers)
- `srv/conductor.go`: the in-memory Conductor session core
- `srv/api.go`: the agent HTTP API and the whole routing tree (`routes()`)
- `srv/ws.go`: the listener WebSocket endpoint. Currently only the upgrade
  slice (issue `.3.2`): `GET /ws` accepts the handshake and closes cleanly.
  It is not wired to the hub yet, so a listener that connects still receives
  nothing but a close frame until the later hub subtasks (`/api` fan-out,
  snapshot on connect, listener count, ping/pong) land.
- `srv/hub.go`: the listener hub core (issue `.3.3`), decoupled from the
  Conductor and from the wire format: it fans one encoded `[]byte` message out
  to every subscriber. One goroutine owns the subscriber set, so all of
  subscribe/unsubscribe/broadcast are safe concurrently; each subscriber has a
  small buffered send channel and a subscriber that cannot keep up is dropped
  and its channel closed exactly once rather than allowed to block the fan-out.
  No Conductor wiring, no snapshot on connect, no listener count, no ping/pong
  and no encoding yet — those are issues `.3.4`–`.3.6`.
- `srv/integration_test.go`: the end-to-end verification harness (see above)
- `srv/ws_test.go`: the WebSocket slice of that harness
- `srv/hub_test.go`: the hub slice of that harness: fan-out to N subscribers
  with identical bytes, unsubscribe closing exactly once, concurrent churn
  under `-race`, and the slow/non-reading subscriber that must not block anyone
  (bounded by watchdogs so a wedged hub fails instead of hanging)
- `srv/templates`: Go HTML templates
- `scripts/mutation-check.sh`: sabotage check proving the tests are non-vacuous
