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
