# Strudel Agent

A live, multi-user algorithmic music performance. The Go server is the conductor
holding the one live performance state; an external AI agent drives it by pushing
strudel code over HTTP.

The browser side is not built yet: `GET /` still renders a minimal welcome
page and nothing plays audio. The listener WebSocket endpoint, the hub that
feeds it, and the fan-out from the agent API are all wired, so a push reaches
connected listeners — `AGENTS.md` lists what is still unwired.

## Building and Running

`make build` writes `srv/srv`; run `./srv/srv`. It listens on `:8000` by
default — override with `-listen`.

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
snapshot is the same object `GET /api/state` returns. A listener that connects
receives a `snapshot` frame immediately, so it lands mid-performance. Accepted
writes then arrive as `code`, `message`, `transport` or `eval-result` frames,
and a listener arriving or leaving produces a `listener-count` frame.
A rejected request — or a stale eval result that is accepted but not stored —
sends nothing at all.

`AGENT_API.md` is the full contract: the endpoints, the snapshot, the loop an
agent should run, and the complete frame vocabulary.

`GET /` serves the page and `/static/` the assets.

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

`make verify` is the repo-owned gate: `gofmt -l .`, `go vet ./...`,
`go build ./...`, and `go test ./... -race -count=1`.

`AGENTS.md` covers the integration harness, the mutation grid that proves those
tests are non-vacuous, and the architecture invariants.

## Running as a systemd service

```bash
sudo cp srv.service /etc/systemd/system/srv.service
sudo systemctl daemon-reload
sudo systemctl enable --now srv

systemctl status srv                          # check status
journalctl -u srv -f                          # follow logs
make build && sudo systemctl restart srv      # pick up code changes
```

## State

There is no database; the performance is live and ephemeral. One in-memory
`Conductor` (`srv/conductor.go`) owns the current strudel code document, a
monotonically increasing version, the shared timeline anchor (epoch ms + cps),
the last agent message, a bounded history of recent code versions, a playing flag
and a listener count. Nothing is persisted and there is no set saving.

## Code layout

- `cmd/srv`: main package (binary entrypoint)
- `cmd/agentcli`: pure-Go client for the agent API (`state`, `push`, `message`, `anchor`, `hush`, `play`, `eval-result`)
- `srv/server.go`: `Server`, `Serve`, `HandleRoot`
- `srv/api.go`: the agent HTTP API and the whole routing tree (`routes()`)
- `srv/conductor.go`: the in-memory Conductor session core
- `srv/hub.go`: the listener hub core — fan-out to every subscriber
- `srv/ws.go`: the listener WebSocket endpoint (`GET /ws`)
- `srv/event.go`: the listener frame shape and the broadcast helper
- `srv/integration_test.go`: the end-to-end verification harness
- `srv/ws_test.go`, `srv/hub_test.go`: its WebSocket and hub slices
- `srv/templates`: Go HTML templates
- `srv/static`: the stylesheet
- `scripts/mutation-check.sh`: sabotage check proving the tests are non-vacuous
- `scripts/mutation-bench.sh` + `docs/mutation-bench/`: grid timing baselines and
  audit reports
- `AGENT_API.md`: the agent/listener wire contract

The invariants these files must hold are in `AGENTS.md`.
