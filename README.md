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
| `srv/static/viz.js` | Pattern visualization plus the sounding-token outline |
| `srv/static/sync.js` | Bar-aligned scheduling and drift reporting |

The page also has a local **Mute/Unmute** control plus an automatic sample load. Mute/Unmute drives only this browser's repl — it never touches the server's `playing` intent and never posts to the transport endpoints.

During a performance the code view outlines, in yellow, every mini-notation token that is sounding at the current instant, and clears the outline the instant the token stops sounding. The outline follows the shared-grid playhead (same clock, same haps), so every listener sees the same token lit for the same bar. The outline lands on the token's document span — the sandbox validates with the bundle's transpiler, which is what keeps hap locations in the document's coordinate frame rather than the mini string's. Only mini-notation tokens light up — a plain-JS structure never does — and between sounds the view goes dark. If the CodeMirror CDN fails to load, the plain-text view keeps working with no highlight.

A listener **boots audible**, regardless of the snapshot `playing` value. Observed: a fresh page starts its browser REPL when the first code commit lands — the version advances, the verdict comes back `ok:true`, the code view fills in — and the button reads `Mute`. Audio policy is handled in two tiers: every start attempts to resume a suspended AudioContext (a browser whose policy allows web audio never needs a click), and the **first user gesture of any kind** — a click or a keypress anywhere on the page, not a Mute/Unmute cycle — resumes a context a blocked browser is still holding suspended; the gesture unlock is context-level only, so it never unmutes a muted listener and never touches the shared transport. What a blocked browser was still holding at evaluation time is reported on the wire: every eval and dry-run verdict carries `stats.autoplay` (`{"ctx":"suspended","act":false}`), so an agent reading `/api/state` can tell `ok:true` from `ok:true`-but-blocked-on-first-gesture. The boot start fires before any pattern exists, so it is silently ineffective until that first commit retries it; muting during load keeps the tab silent when the pattern lands. An unmuted listener still follows **transport transitions**: `POST /api/hush` stops its repl, `POST /api/play` restarts it. A locally muted listener stays stopped across transport frames (mute is sticky; no remote frame unmutes it), and unmuting while the shared intent is stopped flips the label back to `Mute` without starting audio. The client states the reason (commits are made with autostart disabled, so new code never force-starts a muted listener), and `playing` remains documented as transport intent rather than per-browser audio state.

**Samples** (`github:tidalcycles/dirt-samples`) load automatically into that one browser at startup — exactly one attempt, no button to press — and are likewise page-local: nothing is shared, and loading them in one tab leaves every other tab without samples. There is no status pill: success is silent. A failed load writes the reason into the page (`Could not load the sample pack github:tidalcycles/dirt-samples: <reason>. Sound stays synth-only; …`) and reveals a **Retry** button; there is no way to unload the pack, so no control implies one. Each browser reports what it could resolve, so a verdict carries `samplesResolved` — see `AGENT_API.md` for the tri-state. That verdict describes the **reporting** browser, not the audience: with several listeners it is whichever reported last. For the audience question — can *anybody* hear this? — each listener also reports its own registry to `POST /api/samples`, and `GET /api/state` returns the whole audience under `samples`, one entry per listener. Three things worth knowing before you rely on it: the verdict is still scoped to one browser, `samples` is `null` until somebody reports rather than zero, and `loaded` means "this listener's registry holds *something*", not "this listener has this pack". While the startup fetch is still in flight a page already reports `loaded:true` with a small count, because the bundle registers its built-in synths at boot, so read `loaded` next to the sounds your pattern actually names rather than on its own.

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
| `make mutation-lint` | Validate the mutation table and anchors, without running tests |
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
| `POST /api/dry-run` | Evaluate a candidate in a connected browser without publishing it |
| `POST /api/dry-run-result` | How a browser answers a dry-run (agents do not call this) |
| `POST /api/sync-result` | How a browser reports where its commit landed; read back from `lastSync` |
| `POST /api/samples` | How one listener reports its sample registry; read back from `samples` |
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

### Try a pattern before publishing it

```bash
echo 's("bd*2, ~ cp")' | bin/agentcli push --dry-run
dry-run: ok, nothing published (dry-run 1, 3 haps)
```

The `3 haps` above is a real reading, not an illustration: it is what one connected browser reported for that pattern. The hap count comes from the browser that evaluated it, so a different browser or a different Strudel bundle can report a different number — read the line as the evaluator's answer, not as a property of the pattern.

`--dry-run` evaluates the document in a connected browser and reports the verdict **without publishing it** — the version, the history and the stored verdict are all left alone, so a broken pattern never becomes the current document. It needs a connected browser just as much as a push does; with none, it fails immediately rather than hanging.

A candidate that does not evaluate exits non-zero, so it composes:

```bash
agentcli push --dry-run -f candidate.js && agentcli push -f candidate.js
```

## Operational limits

- **No server-side audio.** The server stores documents and relays state.
- **Browser-only evaluation.** Invalid Strudel can be accepted by the server and fail only when a browser evaluates it.
- **Audible output rests on ONE human observation, and no automated check can replace it.** A human on a machine with real audio hardware heard `note("<c2 c3 g2 c4>")` at commit `25e14fe`. Nothing in this repository can observe sound: the test sandbox has no audio device, and the Go process never evaluates JavaScript. So no test asserts that audio works, and a green suite is not evidence that it does. `ok:true` means the pattern parsed, evaluated and was committed — not that anything was heard; a browser whose audio has not been started reports exactly the same `ok:true` in complete silence.
- **Bar-aligned, not sample-accurate.** The shared anchor plus next-cycle commit aligns listeners to bars. Different machines can still differ within a bar.
- **Residual drift is visible to the agent, not just to a human.** The browser reports where each commit landed to `POST /api/sync-result`, and an agent reads it from `lastSync` on `GET /api/state` (or `agentcli state`). Without a usable anchor it reports `unscheduled` rather than inventing a bar position, and `lastSync` is `null` — not zero — until some listener has actually committed. It is the newest report from **one** browser, not the spread across listeners.
- **Re-anchoring is recovery.** `POST /api/anchor` replaces the shared timeline.
- **No persistence.** Restarting the server resets the performance to version 0 with an empty history.
- **Network required at page load, and again to load the sample pack.** The pinned browser dependencies are not vendored. Observed with the CDN URLs failing: the shell still renders and `editor.js` still falls back to the plain textarea, but the Strudel bundle never loads, so there is no evaluator — the page never subscribes to `/ws` (it sits in `reconnecting`), the bundle-specific message renders in `#samples-error` with Retry, and no verdict can ever reach the server. The automatic sample load is a second startup network dependency: it fetches `github:tidalcycles/dirt-samples` from GitHub once at startup.

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

# Replacing a verdict the browser already stored for that version needs -force.
bin/agentcli eval-result -version 1 -ok=false -force

# Before acting on a verdict, insist that it is about the code that is live.
bin/agentcli state -require-current || echo "the verdict is not about the current version"

# Block until a browser has evaluated a version, instead of sleeping and polling.
bin/agentcli wait -version 8

# Publish and wait in one command. -timeout covers both halves.
echo 's("bd*2, ~ cp")' | bin/agentcli push --wait
```

Base URL:

```text
-base              explicit URL
$STRUDEL_AGENT_URL environment override
http://localhost:8000 default
```

`-timeout` defaults to 10s per request, and is the whole budget for a command that waits; `-json` emits raw JSON. Exit codes are `0` for success, `1` when the server refused, could not be reached, or did not return what was asked for, and `2` for invalid CLI usage. Server error text is passed through unchanged on stderr.

### Waiting for a verdict

`wait` closes step 4 of the agent loop, the one step that had no command:

```bash
bin/agentcli wait              # whatever version is live when the wait starts
bin/agentcli wait -version 8   # exactly version 8
echo 's("bd*2")' | bin/agentcli push --wait   # publish, then wait for that version
```

It polls `GET /api/state` until a verdict for the requested version exists. Three properties are the reason to use it rather than writing the loop yourself:

- **A verdict for an OLDER version never satisfies it.** This is the whole point. Sleeping too little and reading the previous version's verdict is how an agent convinces itself a bad push worked, and `wait` cannot be talked into it however long it polls.
- **It is bounded by `-timeout`, and fails fast when nobody is listening.** With no connected listener no verdict will ever arrive, so rather than burn the whole budget to report "try again later" it says so on its first read and names the version it was waiting for. On timeout it prints no verdict row at all and exits `1` — not `2`, because the command line was valid and the requests were answered.
- **`push --wait` waits for the version the server just handed back**, so it cannot report a verdict about code you did not publish. It cannot be combined with `--dry-run`: a dry-run publishes nothing, so there is no version to wait for.

`wait` does not heartbeat for you — there is no `agentcli heartbeat` — so keep the wait inside the 15-second presence lease or beat on `POST /api/heartbeat` yourself.

### Verdict currency

`state` prints one `verdict:` row stating, in words, whether the stored evaluation result is about the code that is live right now:

| verdict row | meaning |
|---|---|
| `CURRENT (v8, ok=true)` | the stored verdict is about the current version |
| `STALE -- the verdict is for v7, the current version is v8 (no browser has evaluated v8 yet)` | a verdict exists but is about code that has since been replaced |
| `NONE YET -- no browser has reported an evaluation, so nothing is known about v8` | no verdict exists; nothing is known about whether the code works |
| `UNEXPECTED -- ...` | the verdict names a version the snapshot does not have, which the documented API cannot produce |

This matters because the verdict is the only feedback an agent gets about whether its code actually worked, and during live testing a verdict for a superseded version was twice read as current. The raw `last eval:` row is unchanged and still printed underneath, so nothing that was visible before is lost, and `-json` is still just the snapshot.

A plain `state` **exits `0` on a stale verdict**: the read succeeded and the CLI reported it truthfully, and a lagging verdict is the normal state of the snapshot while an agent waits for a browser to evaluate its latest push — failing there would make the ordinary poll unusable. `state -require-current` is the strict form for a caller about to *act* on the verdict: it exits `1` unless the stored verdict is for the current version. That is the one documented widening of exit `1`, which now also means "the server did not give you what you asked for" — and, with `eval-result -force` below, "the CLI declined to send a request whose consequence you did not ask for".

`anchor` can update either half of the anchor; the omitted half is taken from the current state.

One caveat belongs to that convenience, because the server pins its anchor at start and refuses any `epochMs` more than five minutes from its own clock — so the *stored* epoch is only reusable for the first five minutes of a session. Left alone the CLI would carry it forward anyway, and the server would reject a value the server itself had published. So `anchor -cps` alone carries the stored epoch forward while it is still usable, and past the window it **re-bases the timeline onto the server's current time** instead, saying so on stderr and naming both epochs. That does move the shared bar grid, which is the point: an epoch older than the window cannot be preserved, because preserving it is exactly what the server forbids. `-json` stdout is unaffected — it stays the bare snapshot. An explicit `-epoch-ms` is sent exactly as given and is never rewritten.

`eval-result` is the one command that can **destroy** stored state rather than add to it: a report naming a version that already has a verdict **replaces** it, and that verdict is the only feedback an agent gets about whether its code worked. So a replacement requires `-force`, and without it the CLI refuses and sends nothing (exiting `1`, not `2` — the command line was valid). A **first** verdict for a fresh version needs no flag, and `-force` never permits reporting an *older* version over a newer verdict: that stays accepted-and-ignored, and is still reported as such. The server is unchanged by any of this — `AGENT_API.md` describes the endpoint, not the CLI's caution about it.

Every subcommand documents itself: `bin/agentcli <command> -h` (or `--help`) prints that command's own flags and defaults and exits `0`, without contacting the server. `message`, `hush` and `play` take no flags and say so; `state` takes only `-require-current`. A genuine mistake — an unknown flag, an unparseable value, or a positional argument — still exits `2`, so help (`0`) and a bad flag (`2`) remain distinguishable by exit code alone.

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

The list of mutations is `scripts/mutations.json`, one JSON object per mutation, so the text a mutation targets may contain any character — including `|`, which a plain delimited table cannot carry. Run `make mutation-lint` first: it checks every mutation still matches its file exactly once and costs no test time.

A `make mutation-check` run never modifies your checkout. The grid runs in a throwaway Git worktree built from `HEAD` and then overlaid with your current working-tree files (including uncommitted ones), so what gets tested is what you have. Set `MUTATION_WORKTREE=0` to mutate in place instead; the script says so when it does.

## systemd deployment

```bash
make build
sudo cp srv.service /etc/systemd/system/srv.service
sudo systemctl daemon-reload
sudo systemctl enable --now srv

systemctl status srv
journalctl -u srv -f
```

`systemctl start` can exit `0` while the unit is still failing to exec, so confirm the start with `systemctl is-active srv` (or a `curl` against `/api/state`) rather than trusting the exit code.

`make start`, `make stop` and `make restart` wrap the same commands with `sudo`; they need no `systemctl` argument juggling and fail loudly rather than reporting a server that did not start.

The unit carries checkout-specific `WorkingDirectory`, `ExecStart`, `User` and `Environment` paths. Update all four when deploying from a different checkout or under a different user; `srv/unit_test.go` fails if `ExecStart` stops pointing at the file `make build` writes.

### Verified on the host

The unit was installed and run on a real systemd host (WSL2, `systemd 255`) at commit `38bcf32`. Every row below is copied from that run, not read off the unit file:

| Check | Observed |
|---|---|
| `systemctl is-enabled srv` | `enabled` |
| `systemctl is-active srv` | `active` |
| `systemctl status srv` | `active (running)`, MainPID `/home/exedev/strudel-agent/srv/srv`, no restart loop |
| `journalctl -u srv` | one line per start, `INFO starting server addr=:8000`, no errors |
| `curl -sf localhost:8000/api/state` | real snapshot, `"version":0` |
| `bin/agentcli push` / `state` | `pushed version 1`, round-trip against the unit |
| `curl -sf localhost:8000/` and `/static/*.js` | `200` for the page and all five browser modules |
| `sudo make restart` | new MainPID, `active` again in under a second |
| `sudo kill -9 <MainPID>` | `NRestarts=1`, new MainPID, `active` again after `RestartSec=5` |

Two operational facts that run established, both of which cost a reader time otherwise:

- **`systemctl start` exiting `0` does not mean the server started.** With `srv/srv` deleted, `systemctl start srv` still exited `0` while the unit sat in `activating (auto-restart)` with `status=203/EXEC`. Always confirm with `systemctl is-active srv` or a `curl` against `/api/state`. The 203 failure produced **no** `journalctl -u srv` line, because the process never ran and never wrote to the journal.
- **`make clean` does not stop the service.** It deletes `srv/srv`; the running unit stayed `active` and kept answering `200` from the deleted inode. The next start is what fails, with `203/EXEC`. Run `make stop` first.

Two deployment properties that follow from the design, not from the unit:

- **Restarting resets the performance.** After the restart above, `GET /api/state` returned `"version":0` with an empty history. There is no persistence.
- **`Restart=always` means a clean `make stop` is the only way to leave the server down**; anything else that kills the process, including a `kill -9`, is restarted within `RestartSec=5`.

What this run does **not** prove: no browser was attached, so no WebSocket listener, no audio and no `lastEvalResult` were exercised through the unit. Those are browser-side and were verified separately.

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
  presence.go       agent liveness lease and the presence sweeper
  dryrun.go         dry-run request registry
  static/           browser modules
  templates/        HTML shell
scripts/            mutation-testing tools
                    mutation-check.sh   the grid runner
                    mutation-parse.py   table validator and record emitter
                    mutations.json      the table itself
srv.service         systemd unit
AGENT_API.md        wire contract
AGENTS.md           architecture and development invariants
```
