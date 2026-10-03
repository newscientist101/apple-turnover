# Strudel Agent

A live, multi-user algorithmic music performance. A Go server holds one in-memory performance state; an external agent drives it over HTTP; browsers are the only components that evaluate Strudel and produce audio.

## Architecture

- **Server (`cmd/srv` → `srv/`)** — pure-Go HTTP + WebSocket front end. It stores the live performance, but never evaluates JavaScript or plays audio.
- **Browser** — subscribes to `/ws`, validates pushed Strudel in a sandbox REPL, and commits only successful versions to the live REPL.
- **Agent (`cmd/agentcli`)** — convenience client for pushing code, narration, transport, and timeline anchors, then reading browser evaluation results.

Browser modules:

| Module | Responsibility |
|---|---|
| `srv/static/session.js` | WebSocket subscription, validate-then-commit, evaluation verdicts, sync status |
| `srv/static/editor.js` | CodeMirror editor over the page textarea |
| `srv/static/viz.js` | Pattern visualization |
| `srv/static/sync.js` | Bar-aligned scheduling and drift reporting |

The page also has local **Play/Pause** and **Samples** controls. Play/Pause calls the browser REPL directly and does not change the server's `playing` intent.

## Requirements

- Go 1.27.1.
- A browser with network access while the page loads.
- CodeMirror 5.65.16 and `@strudel/web` 1.3.0 are loaded from pinned public CDN URLs.
- No npm/Vite build and no database.

## Build and run

```bash
make build
./srv/srv
```

Default listen address: `:8000`. Override with:

```bash
./srv/srv -listen 127.0.0.1:9000
```

Then open `http://localhost:8000/`.

| Target | Purpose |
|---|---|
| `make build` | Build `srv/srv` and `bin/agentcli` |
| `make test` | Run the ordinary Go test suite |
| `make verify` | `gofmt`, `go vet`, build, and race-enabled tests |
| `make mutation-check` | Run the mutation-testing gate |
| `make start` / `stop` / `restart` | Control the systemd service |
| `make clean` | Remove built binaries |

## API at a glance

The agent uses HTTP; listeners use WebSocket. `/api` request bodies are JSON and capped at 64 KiB.

| Endpoint | Purpose |
|---|---|
| `GET /api/state` | Read the current snapshot |
| `POST /api/code` | Publish new Strudel code; increments the version |
| `POST /api/message` | Change narration |
| `POST /api/anchor` | Replace the shared timeline anchor |
| `POST /api/play` | Set transport intent to playing |
| `POST /api/hush` | Set transport intent to stopped |
| `POST /api/eval-result` | Record a browser evaluation result |
| `POST /api/heartbeat` | Renew the agent liveness lease (decays after 15s) |
| `GET /ws` | Listener WebSocket |
| `GET /` | Browser application |
| `/static/` | Browser assets |

`AGENT_API.md` is the authoritative wire contract.

## One agent iteration

With a browser connected:

```bash
curl -s localhost:8000/api/state
curl -s -X POST localhost:8000/api/code \
  -H 'Content-Type: application/json' \
  -d '{"code":"s(\"bd*2, ~ cp\")","message":"four on the floor"}'

# A connected browser evaluates the version and reports the result.
curl -s localhost:8000/api/state | jq .lastEvalResult
```

A successful `POST /api/code` only means the document was stored. It does not mean Strudel evaluated successfully. `lastEvalResult` is browser-supplied; with no listener connected, no verdict arrives.

## Operational limits

- **No server-side audio.** The server stores documents and relays state.
- **Browser-only evaluation.** Invalid Strudel can be accepted by the server and fail only when a browser evaluates it.
- **Bar-aligned, not sample-accurate.** The shared anchor plus next-cycle commit aligns listeners to bars. Different machines can still differ within a bar.
- **Residual drift is visible.** The browser reports observed drift and uses `unscheduled` when no usable anchor exists.
- **Re-anchoring is recovery.** `POST /api/anchor` replaces the shared timeline.
- **No persistence.** Restarting the server resets the performance to version 0 with an empty history.
- **Network required at page load.** The pinned browser dependencies are not vendored.

## Agent CLI

`bin/agentcli` wraps the documented API:

```bash
bin/agentcli state
echo 's("bd*2, ~ cp")' | bin/agentcli push
bin/agentcli message "four on the floor"
bin/agentcli anchor -cps 0.75
bin/agentcli hush
bin/agentcli play
bin/agentcli eval-result -version 1 -ok=false -error "x is not a function"
```

Base URL:

```text
-base              explicit URL
$STRUDEL_AGENT_URL environment override
http://localhost:8000 default
```

`-timeout` defaults to 10s per request; `-json` emits raw JSON. Exit codes are `0` for success, `1` for server/connection failure, and `2` for invalid CLI usage. Server error text is passed through unchanged on stderr.

`anchor` can update either half of the anchor; the omitted half is taken from the current state.

Every subcommand documents itself: `bin/agentcli <command> -h` (or `--help`) prints that command's own flags and defaults and exits `0`, without contacting the server. `state`, `message`, `hush` and `play` take no flags and say so. A genuine mistake — an unknown flag or an unparseable value — still exits `2`, so help (`0`) and a bad flag (`2`) remain distinguishable by exit code alone.

## Verification

The normal gate is:

```bash
gofmt -l .
go vet ./...
go build ./...
go test ./... -race -count=1
```

Tests use loopback/`httptest`, not the production port, and blocking operations are bounded so a hang becomes a failure.

`make mutation-check` deliberately injects defects and requires the tests to catch them. Mutation results are only trustworthy when the mutation really changed the intended source and was restored afterward.

## systemd deployment

```bash
sudo cp srv.service /etc/systemd/system/srv.service
sudo systemctl daemon-reload
sudo systemctl enable --now srv

systemctl status srv
journalctl -u srv -f
```

The service unit contains checkout-specific `WorkingDirectory` and `ExecStart` paths. Update both when deploying from a different checkout or under a different user.

## Repository map

```text
cmd/srv/            server entrypoint
cmd/agentcli/       HTTP API client
srv/                server package
  server.go         server lifecycle and page handling
  api.go            HTTP API and routes
  conductor.go      in-memory performance state
  hub.go            listener fan-out
  ws.go             WebSocket listener endpoint
  event.go          listener frame encoding and broadcast
  static/           browser modules
  templates/        HTML shell
scripts/            mutation-testing tools
srv.service         systemd unit
AGENT_API.md        wire contract
AGENTS.md           architecture and development invariants
```
