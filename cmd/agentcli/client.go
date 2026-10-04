package main

// The wire layer: everything that talks to the server, and the two error kinds
// the exit-code contract is built from.
//
// Two error types, and the distinction is the whole exit-code contract:
//
//   - usageError    the user asked for something impossible (no base URL, no
//     document, unknown subcommand). Nothing was sent; exit 2.
//   - serverError   the server was reached and refused, or could not be reached
//     at all. Exit 1, with the server's own message printed verbatim.
//
// The server's rejection text is never reworded, summarised or swallowed: an
// agent that pushed a blank document needs to read the server's exact reason,
// and a CLI that paraphrased it would be a second, drifting contract.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxDocBytes bounds a document read from a file or stdin. The server's own cap
// is 64 KiB (APIMaxBodyBytes) and this is deliberately LOOSER: the CLI's job is
// to hand the document over and report the server's verdict, not to pre-empt it.
// The bound here exists only so a `cat /dev/zero | agentcli push` cannot exhaust
// memory before the request is ever made.
const maxDocBytes = 1 << 20

// maxResponseBytes bounds a response body read. Every response the server
// produces is a small JSON object, so a megabyte is already pathological; the
// bound is what keeps a wrong-but-not-hanging endpoint from being read forever.
const maxResponseBytes = 1 << 20

// usageError is a caller mistake: exit 2, nothing sent.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// serverError is a rejection or a transport failure: exit 1. When the server
// answered, Body holds its verbatim JSON error string.
type serverError struct {
	Status int
	Body   string
	Err    error
}

func (e *serverError) Error() string {
	switch {
	case e.Err != nil:
		return e.Err.Error()
	case e.Body != "":
		return fmt.Sprintf("server rejected the request (HTTP %d): %s", e.Status, e.Body)
	default:
		return fmt.Sprintf("server returned HTTP %d with no JSON error body", e.Status)
	}
}

func (e *serverError) Unwrap() error { return e.Err }

// snapshot mirrors the server's Snapshot. It is declared field-by-field rather
// than reused so that a json tag renamed in srv/conductor.go shows up here as a
// zero value, which the tests assert on rather than silently tolerating.
type snapshot struct {
	Version          int64       `json:"version"`
	Code             string      `json:"code"`
	LastAgentMessage string      `json:"lastAgentMessage"`
	Anchor           anchor      `json:"anchor"`
	History          []version   `json:"history"`
	Playing          bool        `json:"playing"`
	ListenerCount    int         `json:"listenerCount"`
	LastEvalResult   *evalResult `json:"lastEvalResult"`

	// LastSync is the newest drift observation a listener reported, or nil when
	// none ever has. The POINTER is the whole point, exactly as SamplesResolved
	// is: nil means UNKNOWN (nothing has been observed yet) and must never be
	// rendered as a drift of zero, or `state` would print a confident healthy
	// row over a system nobody has measured.
	LastSync *syncObservation `json:"lastSync"`
}

// syncObservation mirrors the server's SyncObservation. DriftMS is a pointer for
// the same reason: a measured zero and an absent measurement are different
// claims, and unscheduled means no bar line existed at all.
type syncObservation struct {
	Version     int64  `json:"version"`
	DriftMS     *int64 `json:"driftMs,omitempty"`
	Unscheduled bool   `json:"unscheduled,omitempty"`
	TargetMS    int64  `json:"targetMs,omitempty"`
	ActualMS    int64  `json:"actualMs,omitempty"`
	EpochMS     int64  `json:"epochMs"`
}

type anchor struct {
	EpochMS int64   `json:"epochMs"`
	CPS     float64 `json:"cps"`
}

type version struct {
	Version int64  `json:"version"`
	Code    string `json:"code"`
	Message string `json:"message"`
	EpochMS int64  `json:"epochMs"`
}

// evalResult mirrors the stored verdict. Error and Stats are omitempty on the
// wire, so both are optional here.
type evalResult struct {
	Version int64           `json:"version"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Stats   json.RawMessage `json:"stats,omitempty"`
	EpochMS int64           `json:"epochMs"`

	// SamplesResolved is a POINTER for the same reason the server's is: nil means
	// UNKNOWN — no registry was available, so nothing was checked. A plain bool
	// here would make "I could not look" read as "I looked and it failed", and
	// `state` would print a confident row over a check that never ran.
	SamplesResolved *bool `json:"samplesResolved,omitempty"`
}

// evalAck mirrors the one response that is not a snapshot. `accepted` means the
// report was well-formed — NOT that it was stored — which is exactly why the
// CLI has to compare against the state it read beforehand.
type evalAck struct {
	Accepted bool  `json:"accepted"`
	Version  int64 `json:"version"`
}

// apiError is the documented failure shape: exactly one field.
type apiError struct {
	Error string `json:"error"`
}

// client is one base URL plus the bounded HTTP plumbing. Every method takes a
// context that already carries a deadline, so no call here can block forever.
type client struct {
	base string
	http *http.Client
	// jsonOutput selects machine-readable output. It is carried on the client
	// rather than passed to every call so that no command can print prose when
	// the caller asked for JSON.
	jsonOutput bool
}

// newClient validates the base URL and builds the bounded transport.
//
// The timeouts are per-transport and aggressive on purpose. The server answers
// every /api request in memory; anything slower than the caller's own -timeout
// is a hang, and ResponseHeaderTimeout is what turns that into an error instead
// of an endless wait.
func newClient(base string, timeout time.Duration, jsonOutput bool) (*client, error) {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return nil, usagef("no base URL: pass -base http://host:port or set STRUDEL_AGENT_URL")
	}
	// A bare "localhost:8000" is what people actually type, so accept it rather
	// than making them remember the scheme.
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, usagef("invalid base URL %q: %v", base, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, usagef("base URL %q must be http or https", base)
	}
	if u.Host == "" {
		return nil, usagef("base URL %q has no host", base)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")

	return &client{
		base:       u.String(),
		jsonOutput: jsonOutput,
		http: &http.Client{
			// No client-level Timeout: the per-request context owns the budget,
			// so one deadline covers dial, write, headers and body together.
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: timeout}).DialContext,
				ResponseHeaderTimeout: timeout,
				TLSHandshakeTimeout:   timeout,
				DisableKeepAlives:     true,
			},
		},
	}, nil
}

// get reads a snapshot from a GET endpoint.
func (c *client) get(ctx context.Context, path string) (*snapshot, error) {
	var snap snapshot
	if err := c.do(ctx, http.MethodGet, path, nil, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// post sends a JSON body and decodes the response into out (which may be nil
// when the caller only cares whether the request failed).
func (c *client) post(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

// do performs one request and maps every failure mode onto the two error kinds.
//
// It distinguishes "the server said no" from "the server never answered",
// because only the first carries a message worth showing verbatim, and because
// a caller must never report a transport failure as a rejection.
func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return usagef("cannot encode the request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return usagef("cannot build the request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// A cancelled/deadline context is a timeout, and saying so is more
		// useful than echoing the transport's url.Error text.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &serverError{Err: fmt.Errorf("no response from %s%s within the timeout; is the server running?",
				c.base, path)}
		}
		return &serverError{Err: fmt.Errorf("cannot reach %s: %v", c.base, err)}
	}
	defer func() {
		// Drain a bounded amount so the body can be closed promptly; the read
		// itself is already capped by the caller below.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return &serverError{Status: resp.StatusCode,
			Err: fmt.Errorf("cannot read the response body: %v", err)}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &serverError{Status: resp.StatusCode, Body: extractError(raw)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		// A 2xx whose body is not the documented JSON is a contract break, and
		// saying so is far more useful than decoding it into a zero value.
		return &serverError{Status: resp.StatusCode,
			Err: fmt.Errorf("server returned HTTP %d with an undecodable body: %v (first 200 bytes: %q)",
				resp.StatusCode, err, snippet(raw))}
	}
	return nil
}

// extractError pulls the documented {"error": "..."} string out of a failure
// body. A body that is not that shape is reported by what it actually is rather
// than being replaced with a generic message, so a proxy's HTML or a plain-text
// net/http error is still legible to the user.
func extractError(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	var probe apiError
	if err := json.Unmarshal(trimmed, &probe); err == nil && probe.Error != "" {
		return probe.Error
	}
	return snippet(trimmed)
}

// snippet renders a bounded, single-line excerpt of a body for an error message.
func snippet(raw []byte) string {
	const limit = 200
	s := strings.TrimSpace(string(raw))
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
