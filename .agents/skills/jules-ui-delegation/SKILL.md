---
name: jules-ui-delegation
description: "Delegate browser-less UI work to a Jules remote session, start to finish."
---

# jules-ui-delegation

Jules is the async UI worker. The local agent owns task tracking (bd/beads),
the repo gate (`make verify`), and the final commit. Jules owns the patch
inside its session. The handoff in both directions is `jules remote pull`.

Proven on: `7393488358784355191` (sample-toggle patch, pulled+applied as
`8278f59`), `17542198629625995985` (shell-test strengthening, pulled+applied
as `7e050e3` with a local scope fix on top).

## When to use

- The bead is a two-dot leaf ID (e.g. `strudel-agent-3vo.4.3`) touching
  `srv/templates/`, `srv/static/`, or UI assertions in `srv/server_test.go`.
- The work needs no live browser check locally (browser acceptance stays a
  separate bead, e.g. `strudel-agent-3vo.4.4`).
- Do NOT use for Go server/hub/websocket behavior, mutation-grid work, or
  anything under `docs/mutation-bench/` -- that stays local.

## Instructions

### 1. Sync and claim (local)

```bash
bd sync                                   # exit 0 before reading task list
bd show <bead-id>                         # read full scope + constraints
bd update <bead-id> --claim               # verify: status in_progress, lease live
```

Keep the bead claimed. `bd heartbeat <bead-id>` if the session runs long.
Only the assignee may release (`bd unclaim`); prefer `bd reclaim` for stale
leases, never `--force` on a live claim.

### 2. Push the baseline (local)

Jules branches from `origin/main`. Any unpushed local commit is invisible to
it, so push first:

```bash
git status --short                        # must be clean or committed
git push origin main
```

Record the HEAD sha -- the session prompt pins it so Jules works on the right
baseline.

### 3. Write the session prompt

Draft the prompt to a file first (reviewable, re-runnable). It must contain:

1. **Repo + baseline**: `In repo newscientist101/apple-turnover on branch
   main (HEAD includes commit <sha>)`.
2. **File scope**: exactly what to change (usually one or two files) and what
   NOT to touch (sibling tests, template/CSS are off-limits unless the bead
   says otherwise).
3. **Acceptance markers**: the literal strings/IDs the result must contain
   (e.g. `sample-toggle-btn`, `Samples: Off`,
   `github:tidalcycles/dirt-samples`).
4. **Repo invariants**: tests use `httptest` only (no network, DB, real
   ports); every wait bounded (a hang must fail, never pass); follow the RED
   discipline from AGENTS.md (show the defect failing before the fix);
   run `gofmt -l .`, `go vet ./...`, `go build ./...`,
   `go test ./... -race -count=1` -- all green.
5. **Handoff**: leave changes in the session for `jules remote pull` review.
   Jules sessions never open pull requests and never run `bd` or touch
   `.beads/` — that is local-only by design, not something to re-instruct
   per session.

Example (bead `strudel-agent-3vo.4.4.1`):

```text
In repo newscientist101/apple-turnover on branch main (HEAD includes
commit 8278f59), strengthen TestPerformanceUIShellStructure in
srv/server_test.go:

GAP 1 (important): MISSING END-OF-DOCUMENT ASSERTION ... following the
TestRootRendersToCompletion pattern (body contains "</main>", ENDS with
"</html>", </main> ordered before </html>). Do NOT weaken
TestRootRendersToCompletion.

GAP 2: narrow the forbidden-term substring check to actual control
elements (e.g. type="range", <input, <select), keeping the no-faders/knobs
intent while eliminating substring false positives.

CONSTRAINTS: keep all required-element + sample-toggle assertions; do not
touch srv/templates/welcome.html or srv/static/style.css; httptest only;
bounded waits; RED discipline; full gate green; leave changes in the session
for `jules remote pull` review.
```

### 4. Spawn the session

```bash
jules remote new --repo newscientist101/apple-turnover --session "$(cat /tmp/jules-task-<bead>.txt)"
```

Record the session ID + URL from the output. `bd sync` to publish the claim.

### 5. Poll

```bash
jules remote list --session               # blank Status = running; Completed = ready
```

While waiting, keep the lease alive: `bd heartbeat <bead-id>`.
Check the diff without applying:

```bash
jules remote pull --session <id>          # review only
```

### 6. Pull + apply (local)

```bash
jules remote pull --session <id> --apply
git status --short && git diff --stat
```

### 7. Local fix-up (expected, not exceptional)

Jules patches land close but need a local pass. Both proven runs did:

- `7393488358784355191`: added `TestPerformanceUIShellStructure` toggle
  markers locally (Jules shipped template/CSS only).
- `17542198629625995985`: narrowed Jules' overbroad `(?i)<input\b` /
  `(?i)<select\b` patterns to `<input ... type=range` + fader/knob/slider
  role markup -- a bare-input match would re-add the trip-wire the bead
  removed (verify: prose "full range of patterns" must NOT trip).

Review every hunk against the bead's scope. Fix locally, never round-trip
trivia through a new session.

### 8. Verify, commit, close (local)

```bash
make verify                               # gofmt, vet, build, go test -race -- all green
git add <files> && git commit -m "... (jules <session-id}) ..."
git push origin main
bd close <bead-id> --reason "Jules session <id> pulled+applied as <sha>: <what landed>; <local fix-up>; make verify green, pushed."
bd sync                                   # exit 0; tree clean
```

Verify the close on `bd show <bead-id>` state, never on `bd sync`'s exit
code alone. Per repo rules: commit each finished piece, never leave finished
work uncommitted, never close a bead on uncommitted work.

## Anti-patterns

- Letting Jules run `bd` claim/close or touch `.beads/` -- task tracking is
  local-only; the Dolt lease contract does not extend into a Jules VM.
- Accepting a Jules diff unreviewed -- both proven runs needed a local pass.
- Spawning before `git push origin main` -- Jules will branch from a stale
  baseline and the patch will not apply.
- Closing the bead before `make verify` + push -- unpushed work exists only
  on this machine.
