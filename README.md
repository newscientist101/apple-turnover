# Go Shelley Template

This app is a live, multi-user algorithmic music performance. A Go server acts
as the conductor holding the single live performance state, and an external AI
agent drives it by pushing strudel code over HTTP; browsers subscribe over
WebSocket and hot-swap the audio.

## Building and Running

Build with `make build`, then run `./srv/srv`. (Because `srv` is a directory, `go build -o srv` places the binary inside it, at `srv/srv`.) The server listens on port 8000 by default.

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
- `srv/templates`: Go HTML templates
