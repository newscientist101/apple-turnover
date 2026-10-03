# Agent Instructions

This file holds the **invariants and the conventions that keep them true**.
Three documents, three jobs — do not blur them:

| Document | Owns |
| --- | --- |
| `README.md` | how to build, run and deploy it; what it does and does not promise a user |
| `AGENT_API.md` | the wire contract an external agent codes against (HTTP bodies, frame kinds) |
| `AGENTS.md` (this file) | the architecture invariants, and how each one is proved |

Concretely: a build or run instruction belongs in `README.md`, a field name or
status code belongs in `AGENT_API.md`, and a rule that a test enforces belongs
here. If you find yourself adding a fourth kind of statement to the wrong file,
move it rather than duplicating it — the duplication is what goes stale.

Both other documents are **machine-checked against the source and against a
running server**, so "the doc is probably fine" is not available here:
`TestReadmeAPITableMatchesRoutes` and `TestAgentAPIDoc*` fail the build when
they disagree with `routes()` or with live bytes. A change to the contract is a
change to `AGENT_API.md` **and** the code, in the same commit.

## Repository map

```text
cmd/srv/            main package; the only thing that calls srv.New().Serve()
cmd/agentcli/       pure-Go client for the documented HTTP API (a harness
                    should not hand-roll curl for this)
srv/                the server package
  server.go         Server struct, New(), Serve(), HandleRoot, Handler()
  api.go            the agent HTTP API and the whole routing tree (routes())
  conductor.go      the in-memory Conductor: the one live performance
  hub.go            the fan-out core: one goroutine owns the subscriber set
  ws.go             GET /ws, the listener endpoint (handshake, relay, reaping)
  event.go          the ONE listener frame shape, and Server.broadcast
  static/           browser assets: session.js, editor.js, viz.js, sync.js
  templates/        welcome.html, the three-region shell
scripts/            mutation-check.sh (the non-vacuity gate), mutation-bench.sh
docs/mutation-bench/ the grid timing baselines and the gomutants verdict
srv.service         the systemd unit the deployment uses
```

There is no database, no build step for the browser assets, and no vendored
copy of anything: the page loads CodeMirror and `@strudel/web` from pinned CDN
URLs in `welcome.html`. That is why there is no npm/Vite toolchain here.

## Task tracking: beads (`bd`)

Run `bd prime` for the current workflow context and this project's persistent
memories (it needs a project that has memories, and auto-injects via
`bd hooks install`). The notes below cover what `bd prime` does not: the Dolt
sync and lease contract specific to this repo.

Tasks live in a beads database (embedded Dolt, database `strudel_agent`, schema
v66). **On a fresh clone the database does not exist** — the Dolt history is
published to the git origin under `refs/dolt/data`, which a plain `git clone`
does not fetch. Bootstrap it before doing anything else:

```bash
bd bootstrap      # clones refs/dolt/data from origin and wires the Dolt remote
```

`bd bootstrap` is non-destructive and idempotent; on an already-bootstrapped
workspace `bd sync` is the cheaper way to catch up.

**Keep bd versions aligned across machines.** The schema is part of the shared
contract: bd refuses in-place migration on a remote-backed database precisely
because two clones migrating independently fork the schema so that
`bd dolt pull` can no longer merge — silently and unrecoverably. So migrations
run once, by a single designated migrator, who then publishes:

```bash
bd dolt push      # publish a migrated schema (see `bd migrate` --force)
```

An installer that takes *latest* bd will pull a version whose bootstrap rewrites
`.beads/config.yaml` and `.beads/.gitignore`, which dirties the tree in a way
that looks like a setup failure. `sync.remote` is nested under `sync:` in the
current format; a clone whose bootstrap prints "Smart gate (BD_SMART_GATE):
auto-applying N pending schema migrations" is running an out-of-date binary
against a newer schema — upgrade rather than resetting the tree.

**Sync discipline.** The database is shared, so a task you close is visible to
everyone and a claim you take is respected. Use `bd sync`, which runs the whole
cycle for you:

```bash
bd sync            # pull -> check conflicts -> recompute is_blocked -> push
```

`bd sync` is the supported way to do this by hand. It exists because the loop is
easy to get subtly wrong, and two of its steps are not what a hand-rolled
pull/push does:

* Conflicts are checked **positively**, from the merge's own conflict rows and
  from Dolt's conflict tables — never inferred from the pull's exit status,
  which is not a trustworthy conflict signal in either direction. A zero exit
  does not mean the merge was clean.
* `is_blocked` is recomputed after the pull, so dependency edges merged in from
  another replica do not leave `bd ready` stale. Skipping this is how you end up
  working a bead that is actually blocked.
* Push retries a bounded number of times when another replica wins the race.
* The pull underneath does auto-settle the conflict classes it can settle
  convergently (machine-local metadata, audit-only dependency rows,
  last-write-wins on issue cells). Anything beyond those is **never**
  auto-resolved.

**Git hooks.** The bd gate is installed via `bd hooks install --shared`, which
writes the five shims to the *committed* `.beads-hooks/` directory and points
`core.hooksPath` at it (local config, so each clone re-runs the install). The
shims are marker-managed (`# --- BEGIN/END BEADS INTEGRATION ---`) and delegate
to `bd hooks run <hook>`, so a bd upgrade changes behavior without a reinstall
and any non-bd content outside the markers is preserved.

**Auto-push.** `dolt.auto-push: true` (committed) makes bd push the Dolt store to
the `origin` remote after any write, including a claim, debounced by
`dolt.auto-push-interval` (60s here; the default is 5m). This build does not
auto-enable on the mere presence of an `origin` remote — the flag is required.
It is **push-only**: it publishes your claims but does not pull others', so it is
not by itself a cross-machine double-claim guard. That still needs a pull before
claiming, or an external `bd sync` timer (the loop `bd sync --help` documents).
Auto-push state lives in the local, gitignored `.beads/push-state.json`.

Branch on the exit code rather than parsing output:

| code | meaning | action |
| ---- | ------- | ------ |
| 0 | synced, or nothing to do | continue |
| 1 | error (transport, auth, storage) | investigate; do not assume it synced |
| 2 | merge conflict — halted, nothing pushed | **operator** resolves by hand |
| 3 | retries exhausted (push race, or a concurrent writer's dirty working set) | transient, nothing pushed; retry next tick |
| 4 | dirty working set is *stuck*, not busy | nothing pushed; no later tick will publish until an operator clears it |

Repeated runs keep halting the same way for exit 2 — it is not self-healing.

* `bd sync` **before** you read the task list, so you do not work from a stale
  view or re-do a bead another agent already closed.
* `bd sync` **after** you finish a unit of work. Unpushed work exists only on
  your machine — this is the failure mode that loses a session's claims.
* `bd dolt commit` first if you hit `cannot merge with uncommitted changes` on a
  pull. Never reach for `--force` to resolve a conflict you have not read; Dolt
  merges cell-level and the losing side is usually the stale one.

**Claims are leases, not advisory hints.** Claiming takes a lease with a TTL:

* `bd update <id> --claim` claims atomically: sets assignee to you and status to
  `in_progress`. Idempotent if you already hold it. Exclusivity is real on the
  node that granted the lease, so two agents no longer silently hold the same
  bead.
* `bd heartbeat <id>` pushes `lease_expires_at` forward while you work. Without
  it a long task loses its claim to expiry. Heartbeats write no Dolt commit, so
  any cadence comfortably below the TTL is fine.
* `bd unclaim <id>` releases a claim you are abandoning (clears assignee, resets
  to `open`). Only the current assignee may release its own claim.
* `bd reclaim` reverts `in_progress` issues whose lease has gone **stale** back
  to `ready`, recording a recovery event. This is the dead-worker reaper: use it
  when an agent crashed mid-task, with `--older-than` as a grace window past
  expiry (roughly 2x the claim TTL) so a worker briefly paused by GC or clock
  skew is not robbed of live work.
* **Verify the claim/close, not the sync.** A clean `bd sync` (exit 0) only
  reports on the sync; it does **not** mean a preceding `--claim` or `close`
  applied. Assert on the claim/close exit code *and* on `bd show <id> --json`
  state (`status`/`assignee`), never on `bd sync`'s exit code. The read can also
  lag the write by an instant, so confirm the transition rather than trusting a
  single read taken the moment the write returns.

`bd update --force` overrides another actor's **live** `in_progress` claim. Use
it only for genuinely abandoned claims, and prefer `bd reclaim`, which
distinguishes an expired lease from a live one. Leases are only enforceable on
the node that granted them; cross-machine claim visibility rides the issue's
`status` and `assignee`, which do commit.

**Dispatch convention.** Three levels: epic `.3vo`, task `.3`/`.4`, subtask
`.3.2`/`.4.1`. Dispatch only leaf subtasks (two-dot IDs), and keep a parent open
until all of its children are closed. `bd ready` lists parents and leaves alike
— filter to the leaves with `bd ready --exclude-type=epic`.

`bd ready` uses blocker-aware ready-work semantics (the same as
`bd list --ready`) and excludes `in_progress`, `blocked`, `deferred`, and
`hooked` issues, so it shows only genuinely claimable work. Add
`--include-deferred` or `--include-ephemeral` when you need those, and
`--explain` to see why something is or is not ready.

`.beads/config.yaml`, `metadata.json`, `README.md` and `.beads/.gitignore` are
tracked; the database itself (`.beads/embeddeddolt/`), backups and runtime state
are not. Do not add the database directory to git, and do not use
`.beads/issues.jsonl` as a sync channel — JSONL import is upsert-only and cannot
reconcile a deletion. This repo is deliberately **not** in bd stealth mode: every
`.beads/` ignore rule lives in the committed `.beads/.gitignore`, not the
per-machine `.git/info/exclude`, so all clones ignore the same set and cannot
diverge; `no-git-ops: false` is set in the committed `.beads/config.yaml` for the
same reason.

## Architecture invariants

These are easy to break and each has tests. Changes should expect failures until
the invariant is preserved. Where a mutation row proves one, it is named — the
grid in `scripts/mutation-check.sh` is the executable form of this section.

**Conductor (`srv/conductor.go`)**

* One Conductor owns performance state; nothing is persisted.
* It stores the code document, a monotonically increasing version, the timeline
  anchor, the last agent message, a bounded history ring, the playing flag and
  the listener count. `Publish` takes the write lock, `Snapshot` the read lock,
  and every operation is safe for concurrent use.
* Versions are contiguous, unique, never reused, and never skipped, including under concurrent pushes.
* Only `Publish` bumps the version. `SetMessage`, `SetAnchor`, `SetPlaying` and
  `SetListenerCount` all leave it alone: a narration, a tempo change, a
  transport move and somebody connecting are not new revisions of the document,
  and a version bump would put each of them in the code history.
* A `Snapshot` never aliases Conductor internals — `History` is rebuilt and
  `LastEvalResult`/`Stats` are deep-copied on every call. A caller holding a
  snapshot must not be able to reach back into the live state through it.
* `RecordEvalResult` returns `(stored bool, err error)`. The bool is the whole
  point: `nil` error cannot distinguish "this changed the performance" from
  "this was understood and deliberately dropped as stale", and only the former
  may be broadcast.

**Hub (`srv/hub.go`)**

* One goroutine exclusively owns the subscriber set.
* It is independent of the Conductor and wire format and broadcasts one encoded `[]byte` to subscribers.
* The optional count hook is `func(int) []byte`: it is handed the count and
  returns a frame the HUB delivers, to every subscriber except the one that
  caused the change. It must not call back into the Hub — it runs on the hub
  goroutine, so that deadlocks.
* `SubscriberCount` is a synchronous round-trip and can block forever if the hub
  wedges, so every call to it in a test is bounded (see Boundedness below).
* Slow subscribers are dropped and their channel is closed exactly once; a double
  close would panic (mutation `28-hub-double-close-allowed`). `Unsubscribe` is
  idempotent and reports whether it was the one that removed the subscriber.
* Hub shutdown and subscriber drop both appear as channel closure, and a receiver
  must use the two-value receive form — `ok == false` means "no longer
  subscribed", which is the same signal either way.
* The per-subscriber buffer is `HubDefaultSendBuffer` (64) MESSAGES, not bytes. At
  the 64 KiB body cap the worst case held for one stalled client is a few MiB,
  and such a client is dropped at the first message that does not fit.

**WebSocket (`srv/ws.go`)**

* `/ws` must never block on a client.
* Use `CloseRead`/its canceled context to detect disconnects; the handler does
  not need its own read loop. `CloseRead` reads in the library's goroutine and
  answers control frames, so the pong is consumed there and not by the handler.
* Every frame write has a per-frame write deadline via `defaultWSWriteTimeout`
  (5s). Without one, a frame larger than a dead client's socket buffers waits
  forever for space the client will never provide.
* Unsubscribe on every exit path so a listener cannot leak a subscriber slot —
  `defer s.Hub.Unsubscribe(sub)` sits immediately after `Subscribe`, above every
  later return, so an early return cannot skip it. `CloseNow` is the deferred
  backstop: it cannot block on a peer that never answers a close handshake.
* The handler selects on the subscriber channel CLOSING as well as on `Hub.Done`
  — a listener the hub dropped for falling behind looks exactly like one the hub
  shut down, and treating them differently would strand a dropped client.
* A failed catch-up snapshot is **not** fatal. The listener is still subscribed
  and will receive every later change; dropping the connection instead would turn
  a transient write timeout into a listener that can never hear anything again.
* Handshake/error behavior is fixed: 426 for a handshake-less GET, 400 for a bad
  `Sec-WebSocket-Version`, 501 for a writer that cannot be hijacked, 403 for a
  cross-origin handshake, 405 + `Allow`, 1001 for a listener arriving after hub
  shutdown, and clean 1000 on hub shutdown. The 426/400/501/403 cases are written
  by `websocket.Accept` itself from `nil` options, not by code in this repo — the
  same-origin rule is the library's default. `TestWSUpgradeIsSameOriginOnly` pins
  the behaviour, so a future `AcceptOptions` that loosened it would fail; do not
  "fix" a handshake failure by adding options here.
* `Server.Serve` sets no `WriteTimeout`, so the 5s close handshake is bounded by
  the library's own deadline and cannot be cut short by the `http.Server`.
* Match exactly one `/ws` path.

**API (`srv/api.go`)**

* Apply the 64 KiB `APIMaxBodyBytes` payload cap centrally in
  `limitAPIRequestBody`, so no endpoint can forget it. Handlers detect the trip
  via `*http.MaxBytesError` and answer 413.
* Size checking occurs before parsing, so oversized + malformed requests return
  413, not 400. `decodeBody` drains the capped body before decoding for the same
  reason: a stream decoder can hit a *syntax* error in the first few bytes of an
  oversized body and report a 400 instead of the cap violation.
* `/api` errors are JSON — always exactly `{"error": "..."}` — and every `/api`
  route carries an explicit non-GET fallback and a catch-all, so the agent never
  receives net/http's plain-text 404/405. Non-`/api` net/http errors stay plain
  text on purpose.
* `decodeBody` rejects unknown fields and trailing data. A typo'd `messge` must
  fail loudly, and a second JSON value in one body must not be silently ignored.
* The argument-free endpoints (`/api/hush`, `/api/play`) go through
  `rejectUnexpectedBody`, so a client cannot smuggle a `code` field through them
  believing it changed the music.
* An empty `code` or `message` is a 400, never a silent no-op: a no-op in the
  agent's loop is indistinguishable from a lost request.
* Broadcasting happens only AFTER a write is accepted, and the snapshot that is
  broadcast is the same one that is returned. `Server.broadcast` is the single
  place that encodes and fans out, so no call site can invent its own path.
* `Handler()` exists so out-of-package tests can drive the REAL `routes()`. It is
  deliberately behaviour-free; `Serve` mounts `routes()` exactly as before.

**Listener frames (`srv/event.go`)**

* There is exactly ONE frame shape: `{"kind": ..., "snapshot": {...}}`. A
  listener that connects mid-performance gets the same shape as one watching a
  change, so the browser client has a single decode path. A bare snapshot on
  connect plus an envelope on change would force every client to branch on shape
  before it could read a field.
* The `snapshot` is a marshalled `Snapshot` VALUE, not a re-shaped copy. That is
  what keeps the frame body byte-identical to `GET /api/state`; the harness
  asserts byte equality, so do not "simplify" the encoder into a second shape.
* `encodeEvent` is the single place a frame is built (both broadcast and
  snapshot-on-connect go through it). It returns `nil` on a marshal failure —
  a dropped message, not a corrupt one — and logs, because a silent drop is a
  silent wrongness.
* The kinds are named for what the AGENT did, not for which endpoint produced
  them, so two routes with the same listener-visible effect agree on one name.
  `transport` covers hush *and* play on purpose: what a listener needs is the
  resulting `playing` flag, which the snapshot carries.

**Anchor and coherence (`srv/conductor.go`, `POST /api/anchor`)**

* A re-anchor REPLACES the whole timeline; it is not a patch. Both `epochMs` and
  `cps` are required, and a body naming only one is a 400. Deliberate: the anchor
  is the one number every client maps its whole scheduler position onto, so there
  is no per-client fallback that could soften a half-applied one.
* Validation lives in `Conductor.SetAnchor`, not in the HTTP handler. A guard at
  one call site is a guard a second caller forgets.
* `validateAnchorAt` takes the reference clock as a **parameter**. That is not
  tidiness: the bound case (`epochMs` exactly `AnchorMaxSkewMS` from now) is
  otherwise asserted against a moving target and fails intermittently for reasons
  that have nothing to do with the guard.
* `!(cps > 0)` rejects zero, negative and NaN in one clause, so there is no
  separate NaN case to forget. Skew is compared as an ABSOLUTE value, so a clock
  running fast is refused on the same terms as one running slow.
* A refused re-anchor leaves the stored anchor UNCHANGED and broadcasts NOTHING.
  Telling listeners to adopt a timeline the server rejected is unrecoverable: each
  client re-derives its position from the anchor it was last told, so the belief
  is invisible to it and surfaces only later as music that does not line up.
* The `POST /api/anchor` response bytes are the `GET /api/state` bytes. An agent
  that re-anchors and then reads the state must not see a different epoch than
  the one its own 200 reported.

**Page shell and static assets (`srv/server.go`, `srv/api.go`)**

* `GET /{$}` is exactly `/`, and `pageData` is a **struct passed to the
  template**, not `nil`. `html/template` only raises an error for an
  unresolvable field when the data is a struct; against `nil` a bad reference
  renders an empty string and `Execute` returns nil, so `HandleRoot` would serve
  a silently truncated page with a 200. Mutation row 14 depends on this.
* The shell is asserted on **end-of-document markers** (`</main>`, `</html>`, plus
  a `HasSuffix` on `</html>`), not on a substring near the top. A shell that
  renders its first half and dies is the defect that matters.
* `/static/` is served by `http.FileServer` and must never be swallowed by the
  API's JSON error handling — the tests assert the `Content-Type` is not
  `application/json` for every module, because that failure is otherwise silent.
* Every browser module is wired by an explicit `<script src="/static/...">` tag
  in `welcome.html`, and each tag is pinned by a test asserting it is still
  there. Dropping a tag is a one-character edit that silently removes a feature,
  so the tag itself is the invariant.

## Listener fan-out and the browser client

Everything in this section is shipped and load-bearing. Each property names the
mutation row that proves it, because "the grid catches it" is only true while the
row is anchored where the property actually lives.

* **The agent CLI** (`cmd/agentcli`, issues strudel-agent-3vo.9.1,
  strudel-agent-3vo.8.4): a pure-Go client for the documented HTTP API, so the
  harness stops hand-rolling `curl`. It adds NO server behaviour — every
  subcommand maps one-to-one onto an endpoint, and `srv.Server.Handler()` exists
  (behaviour-free) purely so its tests can drive the REAL `routes()` instead of
  a fake that could drift from the contract. Three properties are load-bearing and
  each has a mutation (rows 70-73, 90-92):
  - **A rejection is never softened.** The server's `{"error": ...}` string is
    printed verbatim on stderr and the exit code is non-zero (1 for a refusal,
    2 for a bad command line). A CLI that paraphrased the reason, or exited 0 on
    a 400, would let an agent believe a refused push had landed.
  - **"Accepted" is not "applied."** The server deliberately answers
    `accepted:true` for a report it understood and then DISCARDED as stale, and
    `hush`/`play` are idempotent — nothing on the wire distinguishes the cases.
    So `eval-result` and `hush`/`play` read `/api/state` first and report the
    comparison ("accepted but IGNORED", "no change") rather than an unqualified
    success. The pre-read is a bounded extra round trip and a deliberately racy
    report, which is why the wording says what was OBSERVED, never what the
    server holds now.
  - **`anchor` carries over the half it was not given.** `POST /api/anchor`
    REPLACES the whole timeline rather than patching one half of it, so a body
    naming only `cps` is a 400 — the contract tells an agent to resend the
    current `epochMs` read from `/api/state`. The CLI does that read for them
    (which is also the pre-read the no-op report needs), so `anchor -cps 0.75`
    cannot silently drop the epoch. Mutating that carry-over to send a zero
    (row 91) is caught by the resulting 400, and removing the pre-read
    altogether (row 92) is caught too — the two are separate defects, so both
    rows are needed rather than one.
  - `-base` or `STRUDEL_AGENT_URL` selects the server; `-timeout` (10s) bounds
    each request and is shared by the pre-read and the write, so one command
    cannot spend twice the budget the caller asked for; `-json` prints the raw
    server JSON for piping into `jq`. Subcommands: `state`, `push`, `message`,
    `anchor`, `hush`, `play`, `eval-result`.

* **Snapshot on connect** (`srv/ws.go`, `srv/event.go`): a listener that
  subscribes is sent the full state immediately as a `snapshot` frame, so a late
  joiner lands mid-performance instead of waiting for the next change. The
  subscribe-then-snapshot order is load-bearing: subscribing first means no
  change can slip through the gap between reading the state and joining the
  fan-out.
* **Write fan-out** (`srv/api.go`): every accepted `POST /api/code`,
  `/api/message`, `/api/hush`, `/api/play` and stored `/api/eval-result`
  broadcasts one frame to every listener. A REJECTED or accepted-but-ignored
  request broadcasts NOTHING — telling listeners about a change that did not
  happen is worse than silence, because the frame carries no "nothing changed"
  marker. This is why `Conductor.RecordEvalResult` returns `(stored bool, err
  error)`: a nil error alone cannot tell "stored" from "understood and dropped as
  stale", and the stale case must not broadcast.
* **Listener count** (`srv/hub.go`, `srv/server.go`): the hub goroutine owns the
  subscriber set, so it publishes the count through an optional `func(int) []byte`
  hook that `New` wires to `Server.listenerCountFrame`. Two properties are
  load-bearing and each has a mutation:
  - **The hook fires BEFORE the command is acknowledged.** `srv/ws.go` subscribes
    and then immediately snapshots, so publishing afterwards would let a
    listener's catch-up frame report a count that excludes the listener reading
    it. Mutation 56 inverts the order; the test catches it by BLOCKING the hook,
    which makes the interleaving observable instead of raced. Reading the count
    straight after `Subscribe` returns does NOT work and survived that mutation —
    an intermittent test is not evidence.
  - **A drop publishes too.** The hub drops a slow subscriber from its own
    goroutine, so a count maintained by the ws handler would go stale until that
    handler happened to wake. A broadcast that drops nobody publishes nothing.
* **Listener-count frames** (`srv/event.go`, `srv/hub.go`, `srv/server.go`): a
  connect, a disconnect and a drop each send one `listener-count` frame to
  every OTHER listener. Three decisions are load-bearing, and each is a place a
  later change could quietly undo:
  - **The hook RETURNS the frame; the hub delivers it.** The hook runs on the hub
    goroutine, so a hook that called `Hub.Broadcast` would send on the unbuffered
    channel of the goroutine executing it — a deadlock that takes every listener
    with it. Handing the bytes back keeps delivery on the one goroutine that owns
    the set, which is also why count frames stay totally ordered with performance
    frames instead of racing a goroutine of their own. Mutations 65 and 68 break
    each half of this (never built, built and thrown away).
  - **The listener that caused the change is excluded.** `srv/ws.go` snapshots
    AFTER `Subscribe` returns, so the connecting listener already holds this
    exact count; a second copy right after the frame every client decodes on
    connect would be noise, and the count frame would need a per-client special
    case. Mutation 67 sends it anyway.
  - **No coalescing.** A burst of N connects produces N frames, in order. That is
    affordable because the frames are queued before the hub acknowledges each
    change, so by the time `/api/state` shows the final count every frame is
    already delivered and a client applying them in order lands exactly where the
    server is.
  The count frame never bumps the version (`SetListenerCount` does not touch it,
  mutation 60) and is documented in `AGENT_API.md`, which is where a new kind has
  to be recorded.
* **Keepalive and dead-client reaping** (`srv/ws.go`, `srv/server.go`): each
  listener is pinged every `wsPingInterval` (30s) and reaped if it cannot answer
  within `wsPongTimeout` (5s). Two properties are load-bearing:
  - **The pong deadline is what distinguishes slow from dead.** This is the only
    path that ends a connection the hub would otherwise keep forever: a listener
    on a quiet performance is sent nothing, so neither the hub's drop-on-overflow
    nor the per-write deadline ever fires for it. Reaping on ping failure alone
    (no deadline) would hang; reaping without pinging would never fire.
  - **Reaping decrements the listener count for free.** The reaper ends the
    handler, and the handler's deferred `Unsubscribe` publishes the new count
    through the hook above. There is deliberately no counting code in `ws.go`,
    so the reaped client cannot leave a phantom listener in `/api/state`.
  Both budgets live on `Server` as fields, like `wsWriteTimeout`, so the tests
  exercise the real policy in milliseconds. Mutation 63 removes the pong
  deadline and wedges instead of failing.

* **Visible per-client sync status** (`srv/templates/welcome.html`,
  `srv/static/style.css`, `srv/static/session.js`, issue
  strudel-agent-3vo.8.5): the drift that bar alignment reduces but cannot
  eliminate is now VISIBLE, which is what makes the honesty in AGENT_API.md
  checkable from the UI rather than a claim. `#sync-status` (in the agent panel,
  `aria-live="polite"`) shows connection state, current cycle/bar, the ms to a
  pending commit, the bar the last commit landed on, and the observed drift.
  Three properties are load-bearing, and each has a mutation (rows 93-97):
  - **It reports what it OBSERVED, not what it hopes.** With no usable anchor
    `sync.js` records `{unscheduled: true}` and the region says "last:
    unscheduled"; there is no grid to align to, and rendering a confident
    "bar N" there would be the exact dishonesty this bead exists to remove.
  - **The region writes ONLY when the rendered text changes.** The countdown
    moves several times a second and the region is `aria-live`, so an
    unconditional `innerHTML` assignment announces every tick — the feature
    works and is still unusable with a screen reader. `aria-live` announces on a
    MUTATION, so an identical write is what stays quiet (row 96).
  - **The interval is torn down on `pagehide`.** `pagehide` rather than `unload`
    because it also fires when the page enters the back/forward cache, where the
    interval would otherwise keep ticking against a frozen clock. This makes
    `stopStatusTimer` reachable rather than dead code (row 97).
  Proof is by EXECUTING the served bytes in goja (`srv/coherence_test.go`), for
  the same reason as the cycle-aligned commit above: a status region that
  silently fails to render leaves the source perfectly readable. Two harness
  facts are load-bearing for that: the fake `setInterval` RE-ARMS (a one-shot
  push would make the countdown untestable and leave `pagehide` nothing to stop),
  and the fake `window` carries `addEventListener` — modelled rather than
  guarded in `session.js`, so an unmodelled API fails loudly instead of letting
  an untested path through.

* **A browser client that renders what it receives** (`srv/templates/welcome.html`,
  `srv/static/session.js`, `editor.js`, `viz.js`, `sync.js`; issues
  strudel-agent-3vo.5, 3vo.6, 3vo.7, 3vo.8.2, 3vo.8.5): this is what removes the
  old claim that the verified fan-out path stopped at the API. `session.js` runs
  a real `strudel.repl`, evaluates each pushed pattern in its own sandbox, defers
  the commit to the shared bar, and posts the verdict back — so the loop is now
  closed by a browser rather than by a polling agent. Three things are
  load-bearing:
  - **The served bytes are what is proved, not a reimplementation.**
    `srv/coherence_test.go` fetches `/static/*.js` and runs it in goja. A test
    harness that modelled the client's arithmetic instead would agree with a
    client bug; executing the shipped file is the only version of this claim
    worth making.
  - **`session.js` must be in that harness, not just `sync.js`.** The decision to
    defer lives in `session.js` (`scheduleCommit`); `sync.js` only offers the
    scheduler. A suite driving only `sync.js` cannot see `session.js` quietly
    reverting to an immediate commit — which is exactly how mutation row 87
    survived its first run.
  - **Validation and the eval report stay immediate.** Only the audio commit
    defers. The report is the agent's feedback loop, and holding it for a bar
    line would stall the loop for no coherence gain.

  What this does **not** make true: the audio is real but the *timing* is only
  bar-accurate. Nothing here is sample-accurate or cross-machine, and the
  per-client drift the UI reports is reduced by the anchor, not eliminated by it.

* **Validate-then-commit, and why the order is the invariant**
  (`srv/static/session.js`, `srv/session_client_test.go`): each pushed version is
  evaluated in a SEPARATE sandbox repl, and only on success is it committed to the
  live repl via `live.setPattern(pattern, false)`. This is live-change safety, not
  tidiness: `repl.evaluate()` calls `hush()` BEFORE parsing, so pushing
  unvalidated code at the live repl would silence every listener on bad code. The
  sandbox absorbs that hush — its scheduler never starts — while the live repl
  keeps playing. The served file is asserted to contain no `live.evaluate(` call;
  that string is the invariant in code form.

* **The live code view is read-only, and degrades** (`srv/static/editor.js`,
  `srv/editor_view_test.go`, issue strudel-agent-3vo.6): the agent is the sole
  writer of the one performer instance, so the view is read-only by default
  (`spellcheck="false" readonly aria-readonly="true"`, and `readOnly` in both the
  `fromTextArea` options and the later `setOption`). `editor.js` owns the ONLY
  CodeMirror touchpoint: it upgrades the shell's `<textarea id="editor">` in
  place when the pinned CDN bundle loaded, and drives the textarea directly
  otherwise, so a blocked CDN degrades to a plain-text view rather than a blank
  one. `session.js` reaches it only through `setCode`/`flashUpdate`/`markError`/
  `clearError` — never the CodeMirror instance or the textarea value.

* **The visualization is ours, not a dependency** (`srv/static/viz.js`,
  `srv/viz_test.go`, issue strudel-agent-3vo.7): canvas renderers driven by
  `Pattern.queryArc` (`@strudel/draw` is not in the bundle), fed by
  `strudelViz.setPattern` and `strudelViz.onSnapshot`. It reads the shared anchor
  for its playhead, so the picture sits on the same bar grid the audio does.

* **Cycle-aligned client commit** (`srv/static/sync.js`, `srv/static/session.js`,
  issue strudel-agent-3vo.8.2): the client half of the shared timeline. A
  listener used to commit via `live.setPattern` the instant a frame landed, so
  each client committed mid-cycle at whatever moment its OWN frame arrived and
  two listeners on one anchor landed on different bars. The commit now defers to
  `window.strudelSync.scheduleAtBoundary`. Four properties are load-bearing, and
  each has a mutation (rows 86-89):
  - **Validation stays immediate; only the commit defers.** The
    `POST /api/eval-result` report is the agent's feedback loop, so delaying it
    behind a bar line would stall the loop for no coherence gain.
  - **The LEAD is load-bearing** (row 88, the subtle one). The commit is
    targeted the LEAD past the bar line, not AT it. Targeting the line leaves no
    margin, so any lateness carries the commit into the next bar and two clients
    that received the same frame milliseconds apart end up a full bar apart.
  - **The re-check is a SEPARATE property from the lead.** A `setTimeout` that
    fires early must not commit early — that is the same off-by-one-bar defect
    arriving through the scheduler instead of through the arithmetic. So the
    tick recomputes against the clock and re-arms rather than firing.
  - **A newer version cancels a pending commit** (row 89). Deferral introduces a
    hazard the immediate commit did not have: a pattern sitting in a timer,
    superseded. Uncancelled it lands AFTER its replacement, and every
    version-stamped surface then disagrees with the audio.
  - **With no usable anchor, commit IMMEDIATELY and say so.** There is no grid to
    align to, so `sync.js` runs the commit at once and records
    `{unscheduled: true}`. An unaligned commit is strictly better than no commit;
    what is not acceptable is rendering a confident "bar N" the client cannot
    justify.

  **These properties are proved by EXECUTING the served bytes in goja, not by
  grepping for a marker** (`srv/coherence_test.go`). That is the same reasoning as
  `TestAgentAPIDocPayloadShapesMatchARunningServer`: a subtly wrong `floor()` or
  a session.js that quietly stops deferring both leave the source perfectly
  readable while every listener drifts a bar apart. Concretely:
  - `TestSessionDefersTheCommitToTheBoundary` drives the served **session.js**,
    because the decision to defer lives there. A suite exercising only sync.js
    cannot see it — which is exactly how row 87 SURVIVED its first run. Do not
    "simplify" that harness back to sync.js alone.
  - The fake clock is advanced explicitly and `runJS` interrupts any script that
    exceeds its budget, so a timing test can never hang the gate.
  - Bar numbers are computed by the **client's own** `cyclePosition`, not by the
    test's arithmetic: a test that duplicated the client's bug would agree with
    it, and the drift would be invisible.
  - goja's `Interrupt` takes the value to interrupt WITH, not a channel to wait
    on — passing a channel interrupts the very next `Run*`. The watchdog joins
    before `ClearInterrupt`, because a concurrent interrupt would leave the
    runtime permanently broken for every later call.

* **The agent contract doc is machine-checked** (`AGENT_API.md`,
  `srv/agent_api_doc_test.go`, issue strudel-agent-3vo.9.2): the document an
  external agent codes against is held to the source and to a running server, in
  both directions, and a check that cannot find what it is looking for FAILS
  rather than skips. Three things are load-bearing, and each was proved by a RED
  before being believed:
  - **The payload shapes are compared to a LIVE server, not to the source.**
    `TestReadmeAPITableMatchesRoutes` and the event-kind check both read source,
    so a json tag renamed in `srv/conductor.go` leaves them perfectly happy while
    every agent reading the doc breaks. Only driving the real handler tree and
    diffing live bytes catches that.
  - **Request shapes are pinned from both sides.** A body with exactly the
    documented fields must be accepted AND the same body plus one undocumented
    field must be rejected, which is what stops the doc from under-claiming
    (a field the server accepts but nobody wrote down).
  - **The frame-table parse is anchored.** The snapshot's field table is written
    in the same markdown shape as the frame-kind table, so an unscoped row scan
    reports `version` and `code` as frame kinds the server never sends — a check
    that cries wolf on the document's own tables trains the next agent to ignore
    it. `<!-- frame-kinds -->` scopes it.
  The anchors (`<!-- shape:NAME -->`, `<!-- frame-kinds -->`) are load-bearing:
  a document with several ```json fences is otherwise ambiguous, and a check
  that binds to the wrong block is a coin flip. `TestAgentAPIDocShapesAreStable`
  asserts the anchor set, so the set of checked shapes cannot quietly shrink.

## Standing limitations

These are **not** open work and not bugs. They are properties of the design, and
the point of writing them down is that an agent must not promise a listener more
than the system delivers. If one of these ever stops being true, that is a change
to `AGENT_API.md` and `README.md` in the same commit as the code.

* **Bar-aligned, never sample-accurate.** Strudel has no cross-machine clock:
  `NeoCyclist` shares one only between instances in the *same* browser. The anchor
  plus the cycle-aligned commit is the entire coherence mechanism, so two machines
  land on the same *bar* and can still be tens of milliseconds apart within it —
  different audio clocks, different output latencies. Never call this lockstep.
* **Drift is made VISIBLE, not eliminated.** `#sync-status` reports the drift the
  client actually observed, and says `unscheduled` rather than a confident bar
  number when there is no usable anchor. An agent reading a nonzero drift has
  learned something true about its listeners.
* **Nothing plays audio on the server, and nothing ever will.** There is no audio
  in Go and the server never evaluates JavaScript. A verdict reaches the agent
  only because a *browser* evaluated the pattern and posted it, so a performance
  with **no listener connected produces no `eval-result` at all**. The agent loop
  closes by polling `GET /api/state`, not by a reply. A push that looks
  unacknowledged is usually just an unheard one.
* **The browser is the only evaluator.** A pattern that is valid JSON and invalid
  strudel is accepted with a 200 and a bumped version. Only a browser turns that
  into a verdict — which is what `POST /api/eval-result` carries, and why the
  version number is the join key between the two halves.
* **The page needs the network at load time.** CodeMirror 5.65.16 and
  `@strudel/web` 1.3.0 load from public CDNs, pinned. There is no vendored copy
  and no offline mode: no network, no audio. `editor.js` degrades to the plain
  `<textarea>` when CodeMirror is absent — the *view* survives, the audio does not.
* **Nothing is persisted.** No database, no saved sets, no history across a
  restart: the server comes up at version 0, already playing, with an empty
  history. `HistoryLimit` (32) bounds the in-memory ring only.

## Verification conventions

`make verify` is the repo gate:

```text
gofmt -l .
go vet ./...
go build ./...
go test ./... -race -count=1
```

Each step is a separate recipe line and there is no `-` and no `|| true`, so make
stops at the first failure and names it: a failing step cannot be mistaken for
success. Nothing in the gate needs the network, a database, a real port or a
running service, and `-count=1` defeats the test cache so a stale cached PASS
cannot mask a regression.

Run it before every commit. Commit each piece of completed work when the task
finishes; never leave finished changes sitting uncommitted in the tree, and do
not close a bead until its work is committed.

When adding verification, extend `make verify`; do not create throwaway scripts.

Tests must:

* avoid network, databases, real ports, and running services — and must never
  bind `:8000`, the production default, or they collide with a real deployment;
* use `httptest` and loopback where appropriate;
* bound every operation so a hang fails rather than waits.

Main test locations — **extend these rather than adding a new harness**:

* `srv/integration_test.go` — boots the real handler tree (`Server.routes()`, the
  same one `Server.Serve` mounts); end-to-end API and WebSocket behaviour, plus
  the README/`AGENT_API.md` route tables and the shipped-binary checks.
* `srv/ws_test.go` — WebSocket behaviour: upgrade, snapshot-on-connect, relay,
  teardown, keepalive, reaping.
* `srv/hub_test.go` — Hub behaviour: fan-out, ordering, churn, slow subscribers,
  close-exactly-once, count-hook transitions.
* `srv/api_test.go` — the agent API per endpoint: status codes AND bodies.
* `srv/conductor_test.go`, `srv/conductor_eval_test.go` — the state core:
  versioning, history ring, anchor, transport, listener count.
* `srv/coherence_test.go` — the anchor route **and** the goja harness that
  executes the served `session.js`/`sync.js` against a fake clock.
* `srv/agent_api_doc_test.go` — holds `AGENT_API.md` to live bytes.
* `srv/session_client_test.go`, `srv/editor_view_test.go`, `srv/viz_test.go` —
  the wiring anchors for the browser modules: the shell must still load the tag,
  and the served module must still carry its load-bearing markers.
* `cmd/agentcli/main_test.go` — the CLI driven in-process against the REAL
  handler tree over loopback `httptest`, asserting output **and** exit code.

Coverage should include:

* API feedback loop asserting on response bodies, not just status codes;
* malformed/oversized/error cases;
* concurrent versioning with parallel pushes yielding exactly `1..N`;
* complete `/` rendering asserted on end-of-document markers;
* WebSocket behavior including a wedged listener — one that never reads a 1 MiB frame through a 1 KiB receive buffer;
* Hub fan-out, close exactly once, churn, slow clients (mutation `30-hub-slow-client-blocks` wedges the hub and fails after a 10s watchdog rather than hanging), and concurrent count polling;
* the shipped `cmd/srv` binary.

## Boundedness in tests

A hanging test is itself a defect, so bounding is a requirement, not a nicety.

Bounds must apply to the operation that can block — the parallel test carries
three independent bounds (a context deadline on every request, per-transport
response-header and client timeouts, and a watchdog waiting on a channel rather
than on `wg.Wait()`):

* request context;
* transport/client timeouts;
* watchdogs waiting on channels;
* server shutdown must itself be bounded.

Do not rely on `wg.Wait()`, `httptest.Server.Close()`, or a deadline checked only after a blocking call.

Close each `httptest.Server` with a timeout on a goroutine instead of `defer ts.Close()`, because `Close` waits for outstanding requests and would itself hang on a wedged handler. Verified against a deliberately deadlocked handler: the test fails in ~12s instead of hanging (mutation `23-wedged-state-handler`).

## Documentation needs no mutation proof

Documentation does not require mutation testing. It requires a check that
compares it to something real:

* `README.md`'s API table and `AGENT_API.md`'s endpoint table are both checked
  against `routes()`, in BOTH directions, by `TestReadmeAPITableMatchesRoutes`. A
  documented endpoint the server does not mount fails, and so does a mounted one
  neither document names.
* `AGENT_API.md`'s payload shapes are checked against a **running server** by
  `TestAgentAPIDocPayloadShapesMatchARunningServer`, which drives the real
  handler tree and diffs live JSON. Checking the doc against the *source* is not
  enough: a json tag renamed in `srv/conductor.go` leaves a source-reading check
  perfectly happy while every agent reading the doc breaks.
* The frame-kind list is derived from `srv/event.go` itself, so a new kind is
  picked up with no doc edit — and an undocumented one fails the build.
* **A missing document FAILS; it does not skip.** `AGENT_API.md` is a shipped
  artefact, and a skipped check here would be exactly the vacuous pass these
  tests exist to prevent. (`README.md` is read with a `Skipf` only because the
  harness itself lives inside the module and may be run from elsewhere.)

## Building

`make build` produces two binaries:

```text
srv/srv        # the server
bin/agentcli   # the agent CLI
```

Both paths are deliberate and both are awkward. `srv/` is a package directory, so
`go build -o srv` lands the binary *inside* it — which is why `srv/srv` is in
`.gitignore` and why `srv.service` points at that path. The CLI is built into
`bin/` for the same reason: a stray executable sitting next to the package it was
built from is confusing, and `bin/` is ignored wholesale.

Default listen address is `:8000`; override with `-listen`. Tests must not bind
`:8000`.

`README.md` has the user-facing build/run/deploy instructions. Keep it there.

## Deployment

`make start`, `make stop` and `make restart` wrap `sudo systemctl {start,stop,restart} srv`.
The `sudo` is required: a bare `systemctl start srv` fails with "Interactive
authentication required" rather than starting anything, which reads like a broken
unit instead of a missing privilege.

Every recipe fails LOUDLY. `systemctl` exits non-zero when the unit is not
installed and make stops at the first failing line, so a typo'd unit name can
never be mistaken for a server that started. This is deliberate: a `start` target
that reported success on a unit that does not exist would be worse than no target.

`README.md` has the install sequence (`cp srv.service`, `daemon-reload`,
`enable --now`). Note that `srv.service` hardcodes this checkout's
`WorkingDirectory` and `ExecStart`; a moved directory yields a unit that starts
and immediately exits, so those two paths are part of the deployment, not
boilerplate.

## Mutation testing

`scripts/mutation-check.sh` applies deliberate defects and requires tests to catch them.

Examples:

```text
make mutation-check
./scripts/mutation-check.sh --list
./scripts/mutation-check.sh 30-hub-slow-client-blocks
./scripts/mutation-check.sh 04-payload-cap-removed
MUTATION_TEST_ARGS="-run TestHub" ./scripts/mutation-check.sh
```

Controls:

* `MUTATION_TIMEOUT` (180s) — bounds one mutation run.
* `GO_TEST_TIMEOUT` (150s) — `go test -timeout`, which is what turns a wedged test
  into a failure with a stack dump rather than a hung run.
* `MUTATION_TEST_ARGS` — extra test arguments, e.g. to hold only the integration
  harness to account. This is the form that matters when a NEW test file is
  added: it proves the new tests catch the defects on their own rather than
  relying on older unit tests to do the work.

The full grid takes several minutes — it is 98 rows, of which 65 are ROUTED to a
narrow `-run` regex and only 33 pay for a whole-suite run, so the wall time is
made of those 33. Start it in the background with its output redirected to a log
file, then STOP — do not poll or sleep-wait for it. Tell the user the run has
started and that they should prompt you again once it has finished; on that
prompt, read the log and report the verdicts.

Get the row count from the script, not from a doc:

```text
./scripts/mutation-check.sh --list | grep -cE '^[0-9]+[a-z]?-'
```

Mutations must:

* actually modify the intended source;
* be reverted automatically and checked byte-identical against a recorded sha256 — including on Ctrl-C, because the revert also runs from an `EXIT` trap;
* treat assertion failures as catches (a failing test);
* be applied with `python3` (no sed portability trap), verified to have actually changed the file, reverted as above;
* treat compile/panic failures as `WEAK` — weak evidence rather than a catch;
* treat survivors as failures.

## Mutation verdicts

| Verdict    | Meaning                                                               |
| ---------- | --------------------------------------------------------------------- |
| `caught`   | A test failed as intended.                                            |
| `SURVIVED` | The suite passed; coverage may be missing or routing matched nothing. |
| `WEAK`     | Compile/panic failure; does not prove the assertion caught it.        |
| `BROKEN`   | Mutation anchor no longer matches the implementation.                 |

`SURVIVED` and `WEAK` are both failures. `BROKEN` is not a test result but a
maintenance signal: the anchor no longer matches the implementation, so the grid
can no longer see the defect that row exists to catch. It must be re-anchored
before the grid is trusted again.

## Why there is no gomutants gate

`gomutants` was evaluated as a replacement for this grid and **rejected**
(epic `strudel-agent-kki`; verdict and numbers in
`docs/mutation-bench/06-decision.md`). Do not re-litigate it without new
evidence. Two independent reasons, both measured:

* **It is slower.** 231.32s cold against this grid's 128.042s for all 41
  mutations. Its one fast number (2.52s) needs an unchanged tree and a
  populated cache — the wrong regime, because a gate runs after an edit.
* **It passes a deliberate deadlock.** `TIMED OUT` and `PENDING` are both
  excluded from the efficacy denominator *and* exit 0. Three wedged infinite
  loops scored exit 0, efficacy 81.82%, coverage 100% — and the verdict was
  cached and replayed. Independently, 23 of the 41 curated mutations have no
  gomutants mutant expressing the same defect, including every `ws.go` row and
  both hang rows.

Every number in that verdict was measured against the grid **as it stood in
epic `strudel-agent-kki`**, when it had 41 rows. The grid has grown since (it is
98 rows now), so treat the figures as the recorded measurement of that
comparison, not as a current benchmark — the *reasons* are structural (a tool
whose timeout verdict exits 0 cannot be the thing that says the suite is
non-vacuous) and the structural reasons have not changed. Re-running gomutants
means re-running the whole bench, not re-reading this paragraph.

So: no `make verify` step, no `.gomutants.yml`, no pinned dependency. A tool
whose `TIMED OUT` and `PENDING` verdicts both exit 0 must not be the thing that
says the suite is non-vacuous.

It remains useful as **non-gating breadth** in the dev loop, where its off-anchor
survivors are how new curated mutations get found:

```text
gomutants -w 1 --exclude-files 'conductor\.go$' \
  -cache=/tmp/gomutants-cache -o=/tmp/gomutants-out ./srv/...
```

Treat that as triage input, never a pass/fail. Keep `-cache`/`-o` outside the
repo and use a fresh cache path per run. Expect noise: ~77% efficacy, and
`conductor.go` is excluded because one of its mutants reaches 2.5 GB in ~7s.

## The invariant that overrides everything else

**A bound may make a hang fast; it must never make a hang pass.**

Adding a timeout must not remove the assertion that proves the defect.

A test that previously hung and now passes because the wait was merely bounded has lost coverage.

## Unbounded waits in tests

Two common bad patterns:

* Waiting for a goroutine that itself is blocked in the wedged component.
* Checking a deadline only after calling an API that may block forever.

For APIs backed by a single owning goroutine, bound the API call itself:

```go
got := make(chan int, 1)
go func() { got <- h.SubscriberCount() }()

select {
case n := <-got:
    return n, true
case <-time.After(d):
    return 0, false
}
```

A parked goroutine after timeout is acceptable when the test is about to fail.

Never call the same blocking API again while constructing the failure message.

## Making a RED

Reproduce the actual defect before claiming it is proven.

For Hub count-path hangs, use a count-only wedge such as replacing:

```go
reply <- len(subs)
```

with:

```go
_ = reply
```

A valid RED includes:

* `panic: test timed out`;
* parked goroutine frames identifying the relevant file/line.

The stack frames are the evidence, not the timeout alone.

## Adding a feature, and the order to do it in

The order matters more than it looks, because several of these steps *fail the
build* until the earlier ones are done:

1. **Write the failing test first**, in the existing harness for that layer
   (`srv/integration_test.go`, `srv/ws_test.go`, `srv/hub_test.go`,
   `srv/coherence_test.go`, …). Watch it fail for the right reason. A test that
   passes before the implementation exists is not testing the feature.
2. **Implement it in the layer that owns the property.** A broadcast belongs in
   `Server.broadcast`; a listener-count change belongs in the hub's count hook;
   a client-timing property belongs in `sync.js`. Putting a rule one layer out
   from where it belongs is how the next change quietly undoes it.
3. **Update `AGENT_API.md`** if the HTTP surface or the frame vocabulary changed,
   in the same commit. `TestAgentAPIDoc*` will tell you if you forgot.
4. **Update this file** if you added or changed an invariant. An invariant with no
   entry here is one the next agent will break without knowing.
5. **Add a mutation row** for the new behaviour, anchored in the source that owns
   it, routed if it is expensive. Prove it is caught *before* you commit — a row
   added and never run is worse than no row, because it reads as coverage.
6. **`make verify`**, then commit.

Two habits worth keeping:

* **Assert on the body, not the status code.** A 200 with a truncated or empty
  body is a real defect and a status-only assertion is blind to it.
* **Prove the real thing, not a model of it.** For the browser that means running
  the served bytes in goja; for the docs it means diffing against a live server;
  for the CLI it means driving the real `routes()`. A harness that reimplements
  the thing under test agrees with its bugs.

## Mutation routing

A mutation may specify a 5th pipe-delimited field containing a `-run` regex.

Rules:

* The 5th field **replaces** `MUTATION_TEST_ARGS` (it does not narrow it
  further), so a routed row ignores an outer `-run` restriction.
* A regex matching no tests produces `SURVIVED`. That is the desired outcome for
  a typo — a row that can never fail is a row that proves nothing, and reporting
  it as `SURVIVED` rather than passing is what makes the typo visible.
* Verify routed names with `go test ./srv/... -list '<regex>'`. An alternation
  such as `TestA|TestB` routes one mutation to several tests.
* Route only genuinely expensive mutations.
* Include all tests required to catch a mutation. Under-routing is the quiet
  failure here: the row still reports `caught`, but only because some unrelated
  test noticed.

Routing is an optimization, not a substitute for fixing unbounded waits. Today 65
of the 98 rows are routed and 33 pay for a whole-suite run; the wall time is made
of those 33, which is why a routed row costs nearly nothing to add.

## Measurement discipline

* Never mutate the main tree; use a Git worktree.
* Worktrees start from the commit, so copy required uncommitted files explicitly.
* Assert mutation anchors actually matched.
* Never run mutation checks concurrently when measuring performance.
* Use a real Git worktree/clone for binary-building integration tests.
* Do not casually run `scripts/mutation-bench.sh`; it rewrites the historical baseline.
* Preserve actual exit codes when piping command output.
* Report exact commands, timings, and relevant output rather than adjectives.

## Evidence standards

Claims about a defect or fix should include:

**Before**

* timeout panic;
* parked goroutine frames identifying the wedge;
* duration.

**After**

* no hang;
* clean failure naming the wedged component;
* duration.

Use prior mutation-bench documents as reference for evidence format.

Known corrections:

* Mutation 30 does not prove the WebSocket hang; it wedges `deliver`.
* Mutation 32 is caught only by `-race`; without `-race` it is not an assertion catch.
