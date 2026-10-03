# Strudel Agent

A live, multi-user algorithmic music performance. The Go server is the conductor
holding the one live performance state; an external AI agent drives it by pushing
strudel code over HTTP.

Today, `GET /` serves a fully wired browser client and an external agent can
drive it over HTTP:

* **The server** (`cmd/srv` → `srv/`) is a pure-Go HTTP + WebSocket front end
  holding one in-memory `Conductor`. It never evaluates JavaScript and never
  plays audio.
* **The browser** is the only thing that runs strudel. `srv/static/session.js`
  subscribes to `/ws`, evaluates each pushed version in its **own sandbox
  repl**, and only commits to the live repl on success — so a broken push never
  silences the performance.
* **The agent** pushes code, narration and the shared timeline anchor, and reads
  back the verdict a browser reported for each version. `cmd/agentcli` is a
  convenience client for exactly that loop.

The browser page ships four modules behind one shell:

| Module | Responsibility |
|---|---|
| `srv/static/session.js` | `/ws` subscription, validate-then-commit, per-version eval verdicts, `#sync-status` |
| `srv/static/editor.js` | CodeMirror live code view, upgraded in place over the shell's `<textarea>` |
| `srv/static/viz.js` | custom canvas renderers driven by `Pattern.queryArc` (`@strudel/draw` is not in the bundle) |
| `srv/static/sync.js` | the cycle-aligned commit: next bar boundary, self-correcting timer, drift observations |

`welcome.html` also wires the two page controls: **Play/Pause** (a local
`repl.start()` / `repl.stop()`, which does not touch the server's `playing`
intent) and **Samples**, which lazily loads the `tidalcycles/dirt-samples`
pack on first click — the default voice is strudel's own synths.

## Requirements

* Go 1.27 (the module declares `go 1.27.1`; no other build tooling — there is
  no npm/Vite step).
* A browser, and network access **at page load**: CodeMirror 5.65.16 and
  `@strudel/web` 1.3.0 load from public CDNs (jsdelivr, unpkg), pinned.
* Nothing else. There is no database, no external service, and no port other
  than the one you choose.

## Building and Running

`make build` writes two binaries — the server to `srv/srv` (`srv/` is a package
directory, so `-o srv` lands the binary inside it) and the CLI to
`bin/agentcli`:

```bash
make build
./srv/srv                    # listens on :8000 by default; override with -listen
./srv/srv -listen 127.0.0.1:9000
```

Then open <http://localhost:8000/>.

| Target | What it does |
|---|---|
| `make build` | `srv/srv` and `bin/agentcli` |
| `make test` | `go test ./...` |
| `make verify` | the full gate: `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./... -race -count=1` |
| `make mutation-check` | breaks the implementation on purpose and requires the tests to catch it |
| `make start` / `stop` / `restart` | `sudo systemctl` wrappers for the unit in `srv.service` |
| `make clean` | removes both built binaries |

## API

The external agent drives the performance over HTTP; listeners subscribe over
WebSocket. Bodies are JSON and capped at 64 KiB.

| Endpoint | Purpose |
|---|---|
| `GET /api/state` | the current snapshot |
| `POST /api/code` | push new strudel code; bumps the version |
| `POST /api/message` | set the message shown to listeners |
| `POST /api/anchor` | re-anchor the shared timeline every listener schedules against |
| `POST /api/play`, `POST /api/hush` | start / stop the performance |
| `POST /api/eval-result` | report how the last code evaluated |
| `GET /ws` | listener WebSocket; one JSON frame per state change |

A snapshot is `{version, code, lastAgentMessage, anchor{epochMs,cps}, history,
playing, listenerCount, lastEvalResult}`. Every write returns the new snapshot,
except `POST /api/eval-result`, which answers `{accepted, version}`. A fresh
server starts at version 0 and already playing.

`listenerCount` is the live number of connected `/ws` listeners, updated on
connect, on disconnect, and when a dead socket is reaped. It is not a change to
the performance, so it does not bump the version — but it does reach the
listeners that are already watching, as a `listener-count` frame. The listener
that caused the change is not sent it: one that just connected has that exact
count in the snapshot it opened with.

The server pings each listener every 30s and reaps it if it cannot answer within
5s. This is what reclaims a listener that vanished without closing — a yanked
cable or a killed tab — which the hub's own drop-on-overflow rule cannot catch
on a quiet performance, since such a listener is sent nothing at all.

Listeners get one frame shape: `{"kind": ..., "snapshot": {...}}`, where the
snapshot is the same object `GET /api/state` returns, byte for byte. A listener
that connects receives a `snapshot` frame immediately, so it lands
mid-performance. The complete vocabulary is seven kinds:

| Kind | Raised by |
|---|---|
| `snapshot` | the catch-up frame sent once on subscribe |
| `code` | an accepted `POST /api/code` (the version bumped) |
| `message` | an accepted `POST /api/message` |
| `anchor` | an accepted `POST /api/anchor` |
| `transport` | an accepted `POST /api/hush` or `POST /api/play` |
| `eval-result` | an **accepted, stored** browser verdict |
| `listener-count` | somebody connected, left, or was reaped |

A rejected request — or a stale eval result that is accepted but not stored —
sends nothing at all, and neither `anchor` nor `listener-count` bumps the
version.

`AGENT_API.md` is the full contract: the endpoints, the snapshot, the loop an
agent should run, and the complete frame vocabulary.

`GET /` serves the page and `/static/` the assets.

## Quickstart: one agent step

With a browser open on the page, one full push → verdict cycle is:

```bash
curl -s localhost:8000/api/state                          # what is playing, and to how many listeners
curl -s -X POST localhost:8000/api/code \
  -d '{"code":"s(\"bd*2, ~ cp\")","message":"four on the floor"}'
# a connected browser validates it in its sandbox repl and posts the verdict
curl -s localhost:8000/api/state | jq .lastEvalResult     # {version,ok,error?,stats?,epochMs}
```

Note the direction of the last step: the agent does not learn whether its code
worked by being told. It reads back what a **browser** reported. With no
listener connected, `lastEvalResult` stays `null` forever — which is a fact
about the system, not a bug to route around. `AGENT_API.md` §"The agent loop"
and §"Worked example" develop this into the full loop.

## Operational limits

These are properties of the design, stated here so nobody has to infer them
from the source:

* **Audio is real in the browser; the server has none and never will.** It holds
  a document, not a sound.
* **Timing is bar-accurate, never sample-accurate.** strudel's `NeoCyclist`
  shares a clock only between instances in the *same* browser, so there is no
  cross-machine clock to align to and no server-side audio clock that could
  correct a listener afterwards. Coherence is entirely app-layer: the shared
  anchor plus a commit deferred to the next cycle boundary.
* **Residual drift within a bar is real** — different audio clocks, different
  output latencies, different machines. The client reports it under
  `#sync-status` (connection state, cycle and bar, countdown to a pending
  commit, last commit bar, observed drift) rather than assuming it away, and
  says `unscheduled` when there is no usable anchor instead of rendering a
  confident bar number it cannot justify.
* **`POST /api/anchor` is the recovery** for a listener that has drifted: it
  re-anchors onto the shared grid. `cps` must be in `(0, 1000]` and `epochMs`
  within five minutes of the server's clock, and a refused re-anchor changes
  nothing and is not broadcast.
* **Unvalidated code must never reach the live repl.** `repl.evaluate()`
  hushes *before* parsing, so pushing straight to the live repl would silence
  every listener on a syntax error. Hence validate-then-commit.
* **The page needs the network at load time** (pinned CodeMirror and
  `@strudel/web` from public CDNs). There is no vendored copy and no offline
  mode: no network, no audio. If CodeMirror fails to load, `editor.js`
  degrades to the plain `<textarea>` rather than to a blank view.
* **No persistence.** No database, no saved sets, no session recovery: a
  restart is an empty performance at version 0.

## The agent CLI

`cmd/agentcli` is a pure-Go client for the endpoints above, so a harness does
not have to hand-roll `curl` on every iteration. `make build` writes it to
`bin/agentcli`.

```bash
bin/agentcli state                             # version, code, playing, listeners, verdict
echo 's("bd*2, ~ cp")' | bin/agentcli push     # or: push -f pattern.js
bin/agentcli message "four on the floor"
bin/agentcli hush
bin/agentcli play
bin/agentcli eval-result -version 1 -ok=false -error "x is not a function"
bin/agentcli anchor -cps 0.75                  # or: anchor -epoch-ms 1757000000000
```

`anchor` republishes the shared timeline. Either flag may be given alone — the
other half is carried over from the current anchor, so a tempo change cannot
silently drop the epoch.

The base URL comes from `-base` or from `$STRUDEL_AGENT_URL`, defaulting to
`http://localhost:8000`; a bare `host:port` is accepted. `-timeout` (10s) bounds
each request, and `-json` prints the raw snapshot for piping into `jq`.

Two properties matter more than the convenience:

* **A rejection is never softened.** The server's own `{"error": ...}` text is
  printed verbatim on stderr and the exit code is non-zero, so a script can tell
  a refusal from a success without parsing prose. Exit codes are `0` success,
  `1` the server refused or could not be reached, `2` a bad command line.
* **"Accepted" is not "applied."** The server deliberately answers
  `accepted:true` for a report it understood and then discarded as stale, and
  `hush`/`play` are idempotent. The CLI reads the state first and says plainly
  which happened — `eval-result: accepted but IGNORED for version 1`, or
  `hush: accepted, no change` — instead of printing an unqualified success over a
  performance that did not move.

The CLI adds no behaviour to the server; it is a client of the contract above.
`AGENT_API.md` remains authoritative, and `curl` still reaches anything the CLI
does not cover.

## Verification

`make verify` is the repo-owned gate every change must pass, and each step is a
separate line so make stops at and names the first failure:

```text
gofmt -l .          # fails if it prints ANY file
go vet ./...
go build ./...
go test ./... -race -count=1   # -count=1 so a cached PASS cannot mask a regression
```

Nothing in it needs the network, a database, a real port or a running service:
the harness listens on loopback only and every operation is bounded, so a hang
is a failure rather than an infinite wait. `make test` is the plain
`go test ./...` for a quick inner loop.

A green suite is not evidence on its own, so there is a second gate.
`make mutation-check` applies each of **98 deliberate defects** to the
implementation — one per invariant — runs the suite, and **requires it to
fail**. A mutation that survives makes the script exit non-zero. Every mutation
is reverted and verified byte-identical afterwards, including on Ctrl-C.

```bash
./scripts/mutation-check.sh --list                    # the 98 row names
./scripts/mutation-check.sh 30-hub-slow-client-blocks # one row
MUTATION_TEST_ARGS="-run TestIntegration" \
  ./scripts/mutation-check.sh                         # only the harness has to catch them
```

The full grid takes roughly ten minutes (bounded by `timeout 2400` in the
Makefile). Do not run two invocations concurrently, and measure in a worktree
when a mutation must be applied by hand. `AGENTS.md` covers the harness, the
architecture invariants, and why the grid is the gate and `gomutants` is not;
`docs/mutation-bench/README.md` indexes the timing baselines and audit reports.

## Running as a systemd service

```bash
sudo cp srv.service /etc/systemd/system/srv.service
sudo systemctl daemon-reload
sudo systemctl enable --now srv

systemctl status srv                          # check status
journalctl -u srv -f                          # follow logs
make build && sudo systemctl restart srv      # pick up code changes
```

`make start`, `make stop` and `make restart` are `sudo systemctl` wrappers for
the same unit. They fail loudly rather than swallowing a typo'd unit name, so a
missing privilege reads as a missing privilege instead of a broken unit. The
unit's `WorkingDirectory` and `ExecStart` point into this checkout
(`/home/exedev/strudel-agent`) and run the `make build` output as user
`exedev`; both paths must be edited for a different checkout or user.

## State

There is no database; the performance is live and ephemeral. One in-memory
`Conductor` (`srv/conductor.go`) owns the current strudel code document, a
monotonically increasing version, the shared timeline anchor (epoch ms + cps,
defaulting to 0.5 cycles/second — strudel's own two-second cycle), the last
agent message, a bounded history of the most recent 32 code versions, a playing
flag and a listener count. Nothing is persisted and there is no set saving: a
restart is version 0 with empty code.

Only `POST /api/code` bumps the version. `message`, `anchor`, `play` and `hush`
change what listeners experience without changing what is playing, so they stay
out of the code history.

## Code layout

**Commands**

- `cmd/srv`: server entrypoint; the only flag is `-listen` (default `:8000`)
- `cmd/agentcli`: pure-Go client for the agent API — `state`, `push`,
  `message`, `anchor`, `hush`, `play`, `eval-result`, one per documented endpoint

**Server (`srv/`)**

- `server.go`: `Server`, `New`, `Serve`, `Handler`, `HandleRoot`
- `api.go`: the agent HTTP API and the whole routing tree (`routes()`), the
  64 KiB body cap, and the JSON error convention
- `conductor.go`: the in-memory session core — code document, version, history,
  anchor, transport intent, listener count
- `hub.go`: the listener hub core — fan-out to every subscriber, drop-on-overflow
- `ws.go`: the listener WebSocket endpoint (`GET /ws`), ping/reap keepalive
- `event.go`: the listener frame shape and the broadcast helper
- `templates/welcome.html`: the page shell (agent panel, canvas, editor, the two
  page controls)
- `static/`: the browser assets — `session.js`, `editor.js`, `viz.js`,
  `sync.js`, `style.css`

**Tests**

- `srv/integration_test.go`: the end-to-end harness; boots the real handler tree
  over `httptest` and includes `TestReadmeAPITableMatchesRoutes`, which holds
  this file's API table to `routes()` in both directions
- `srv/ws_test.go`, `srv/hub_test.go`: WebSocket and hub slices
- `srv/api_test.go`, `srv/server_test.go`, `srv/conductor_test.go`,
  `srv/conductor_eval_test.go`: per-unit slices
- `srv/coherence_test.go`: executes the **served** `session.js` and `sync.js`
  in goja against a fake clock, so the client's own arithmetic is proved on the
  bytes that ship rather than by a Go reimplementation of it — including that
  two clients on one anchor commit on the same bar, that the lead is what saves
  the late client, and that `session.js` actually defers the commit
- `srv/session_client_test.go`, `srv/editor_view_test.go`, `srv/viz_test.go`:
  pin that the shell wires each browser module and that each is served with its
  load-bearing markers, so a later shell edit cannot silently drop one
- `srv/agent_api_doc_test.go`: machine-checks `AGENT_API.md` against a running
  server and the route definitions — endpoints, frame kinds and payload shapes,
  both directions
- `cmd/srv/main_test.go`, `cmd/agentcli/main_test.go`: the shipped binaries and
  the CLI's output/exit-code contract, the latter against `srv.Handler()`

**Verification and docs**

- `Makefile`: `build`, `test`, `verify`, `mutation-check`, `start`/`stop`/`restart`, `clean`
- `scripts/mutation-check.sh`: the sabotage grid proving the tests are non-vacuous
- `scripts/mutation-bench.sh` + `docs/mutation-bench/`: grid timing baselines and
  audit reports (start at `docs/mutation-bench/README.md`)
- `srv.service`: the systemd unit
- `AGENTS.md`: developer instructions, architecture invariants, verification
  standards, and the bead (`bd`) workflow
- `AGENT_API.md`: the agent/listener wire contract
- `LICENSE`: public domain (Unlicense)

The invariants these files must hold are in `AGENTS.md`; the wire contract is
in `AGENT_API.md`. Neither belongs here, which is why this file defers to both
rather than restating them.
