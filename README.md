# Strudel Agent

A live, multi-user algorithmic music performance. The Go server is the conductor
holding the one live performance state; an external AI agent drives it by pushing
strudel code over HTTP.

The browser side is not built yet: `GET /` still renders the template's welcome
page and nothing plays audio. The listener WebSocket endpoint and the hub that
feeds it exist and are wired to each other, so the fan-out path works end to end
from the hub onwards — see Code layout for what is still unwired.

## Building and Running

`make build` writes `srv/srv` (`-o srv` places the binary inside the `srv/`
directory); run `./srv/srv`. It listens on `:8000` by default — override with
`-listen`.

## Verification

`make verify` is the repo-owned gate — run it before every commit, and extend it
rather than writing a throwaway script when a new feature needs proving:

1. `gofmt -l .` — fails if it prints **any** file.
2. `go vet ./...`
3. `go build ./...`
4. `go test ./... -race -count=1`

Each step is its own recipe line, so `make` stops at and names the first failure;
the `verify:` comments in the Makefile say why each is there. Nothing needs the
network, a database, a real port or a running service, and every operation is
bounded, so a hang fails rather than waits.

### The integration harness

`srv/integration_test.go` boots the **real** handler tree (`Server.routes()`, the
same one `Server.Serve` mounts) and drives it the way the external agent does.
Together with `srv/ws_test.go` and `srv/hub_test.go` it covers:

* **the feedback loop** — `GET /api/state` → `POST /api/code` →
  `POST /api/message` → `POST /api/eval-result` → `GET /api/state` — asserting on
  response **bodies**, not just status codes;
* **the whole error/edge matrix** as table-driven cases: malformed and oversized
  bodies, missing `Content-Type`, unknown/zero/negative versions, stale
  eval reports, 405 + `Allow`, JSON 404s under `/api`, and the 64 KiB payload cap
  — where a body that is oversized **and** malformed must be 413, not 400;
* **concurrency**: parallel pushes over a real loopback server must yield version
  numbers exactly `1..N` — contiguous, unique, no lost updates;
* **the non-API surface**: `GET /` renders to completion, asserted on
  end-of-document markers, which is what catches an `html/template` render that
  aborts mid-document while `HandleRoot` still returns 200; `/static/` serves;
  a non-`/api` 404 stays net/http's plain text;
* **the WebSocket contract** over both a real loopback listener and the
  in-process recorder: 101 and staying subscribed, the hub message arriving as
  one text frame carrying exactly the broadcast bytes in order, unsubscribe on
  disconnect, a clean 1000 on hub shutdown, 1001 for a listener arriving after
  it, 405 + `Allow` for a non-GET verb, a clear 426 for a handshake-less GET
  (400 on a bad `Sec-WebSocket-Version`, 501 on an unhijackable writer) rather
  than a panic, 403 for a cross-origin handshake, exactly one matching path, and
  a wedged listener — one that never reads a 1 MiB frame through a 1 KiB receive
  buffer — that cannot hold up shutdown;
* **the hub core**: one broadcast reaching every subscriber with byte-identical
  payloads, an unsubscribed or dropped subscriber having its channel closed
  exactly once (a double close panics — that is how the mutation
  `28-hub-double-close-allowed` is caught), concurrent churn clean under `-race`
  including concurrent `SubscriberCount` polling, and a non-reading subscriber
  unable to block the fan-out (`30-hub-slow-client-blocks` wedges the hub and
  fails after a 10s watchdog instead of hanging);
* **the shipped binary**: `cmd/srv` is built and run on a loopback port, and the
  loop is re-driven through the real process.

Boundedness is a requirement, not a nicety, because a hanging test is itself a
defect. The parallel test has three independent bounds — a context deadline on
every request, per-transport response-header and client timeouts, and a watchdog
waiting on a channel rather than on `wg.Wait()` — and it closes its
`httptest.Server` with a timeout on a goroutine instead of `defer ts.Close()`,
because `Close` waits for outstanding requests and would itself hang on a wedged
handler. Verified against a deliberately deadlocked handler: the test fails in
~12s instead of hanging (mutation `23-wedged-state-handler`).

Future work — the browser client, multi-client coherence — should **extend these
files** rather than adding a one-off script.

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
recorded sha256 — including on Ctrl-C, because the revert also runs from an
`EXIT` trap. Only a **failing test** counts as a catch: a mutation that merely
fails to compile is reported as weak evidence, and a survivor exits non-zero.

`AGENTS.md` is the authoritative guide to the grid — the four verdicts
(`caught`/`SURVIVED`/`WEAK`/`BROKEN`), the timeout knobs (`MUTATION_TIMEOUT`,
`GO_TEST_TIMEOUT`), per-mutation `-run` routing as an optional fifth table
field, and the invariant that a bound may make a hang fast but may never make it
pass.

## Running as a systemd service

```bash
sudo cp srv.service /etc/systemd/system/srv.service
sudo systemctl daemon-reload
sudo systemctl enable --now srv

systemctl status srv                          # check status
journalctl -u srv -f                          # follow logs
make build && sudo systemctl restart srv      # pick up code changes
```

## Authorization

exe.dev provides the authorization headers and login/logout links this app uses.
Proxied through exed, requests carry `X-ExeDev-UserID` and `X-ExeDev-Email` when
the caller is authenticated.

## State

There is no database; the performance is live and ephemeral. One in-memory
`Conductor` (`srv/conductor.go`) owns the current strudel code document, a
monotonically increasing version, the shared timeline anchor (epoch ms + cps),
the last agent message, a bounded history of recent code versions, a playing flag
and a listener count. Nothing is persisted and there is no set saving.

The template's SQLite/visitors machinery — the visitors view counter, the `db`
package (sqlc generated code, migrations, queries) and `modernc.org/sqlite` — was
replaced by this in-memory core.

## Code layout

- `cmd/srv`: main package (binary entrypoint; owns the `-listen` flag)
- `srv/server.go`: `Server`, `Serve`, and `HandleRoot`, which takes identity from
  the proxy headers purely to greet the viewer
- `srv/api.go`: the agent HTTP API and the whole routing tree (`routes()`)
- `srv/conductor.go`: the in-memory Conductor session core
- `srv/hub.go`: the listener hub core, decoupled from the Conductor and from the
  wire format — it fans one encoded `[]byte` out to every subscriber. One
  goroutine owns the subscriber set, so subscribe/unsubscribe/broadcast are safe
  concurrently; each subscriber has a small buffered send channel, and one that
  cannot keep up is dropped with its channel closed exactly once rather than
  allowed to block the fan-out
- `srv/ws.go`: the listener WebSocket endpoint. `GET /ws` accepts the handshake,
  registers the listener with the hub, relays every hub message as one text frame
  under a per-frame write deadline, and unsubscribes on every exit path. The read
  side is `Conn.CloseRead`, whose cancelled context is the "client disconnected"
  signal, so a client that sends nothing cannot block the handler and no read of
  its own is needed
- `srv/integration_test.go`: the end-to-end verification harness (see above)
- `srv/ws_test.go`, `srv/hub_test.go`: its WebSocket and hub slices, every dial
  and read carrying an explicit deadline so a wedged handler fails the test
  instead of hanging it
- `srv/templates`: Go HTML templates (`welcome.html`, still the template's page)
- `srv/static`: `style.css` and `script.js` (still the template's assets)
- `scripts/mutation-check.sh`: sabotage check proving the tests are non-vacuous
- `scripts/mutation-bench.sh` + `docs/mutation-bench/`: grid timing baselines,
  the unbounded-wait audit, and the class-level proof of the fixes

**Not yet wired:** no snapshot on connect, no `/api`-to-hub fan-out, no listener
count published through the Conductor (`SetListenerCount` has no production
caller, so it serialises as 0), no ping/pong, no dead-socket reaping, and no
message encoding beyond the raw bytes the hub is handed.
