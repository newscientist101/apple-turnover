package main

// The subcommands. Each one maps onto exactly one documented endpoint, prints
// only values that came back in a server response, and returns an error rather
// than calling os.Exit so that run() owns the exit-code contract.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// cmdState prints the snapshot: the document, the version, the playing flag, the
// last agent message and the listener count, which is the whole read path an
// agent needs before deciding what to push next.
func cmdState(ctx context.Context, c *client, args []string, out io.Writer) error {
	if len(args) > 0 {
		return usagef("state takes no arguments, got %q", strings.Join(args, " "))
	}
	snap, err := c.get(ctx, "/api/state")
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSONLine(out, snap)
	}
	fmt.Fprintf(out, "version:        %d\n", snap.Version)
	fmt.Fprintf(out, "playing:        %t\n", snap.Playing)
	fmt.Fprintf(out, "listeners:      %d\n", snap.ListenerCount)
	fmt.Fprintf(out, "last message:   %s\n", orNone(snap.LastAgentMessage))
	fmt.Fprintf(out, "anchor:         epochMs=%d cps=%g\n", snap.Anchor.EpochMS, snap.Anchor.CPS)
	fmt.Fprintf(out, "code:\n%s\n", orNone(snap.Code))
	if v := snap.LastEvalResult; v != nil {
		fmt.Fprintf(out, "last eval:      version=%d ok=%t", v.Version, v.OK)
		if v.Error != "" {
			fmt.Fprintf(out, " error=%q", v.Error)
		}
		fmt.Fprintln(out)
	} else {
		fmt.Fprintf(out, "last eval:      none yet\n")
	}
	return nil
}

// codeRequest is the documented POST /api/code body. Message is omitempty so an
// omitted narration leaves the previous one in place, exactly as the contract
// says — sending an empty string would be a different request.
type codeRequest struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// cmdPush publishes a new document, read from -f or from stdin.
//
// It reports the NEW VERSION rather than the whole snapshot, because the version
// is the one thing a caller must read back to close the loop: it is what a later
// eval-result has to name.
func cmdPush(ctx context.Context, c *client, args []string, stdin io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("f", "", "read the document from this file (\"-\" or omitted means stdin)")
	message := fs.String("m", "", "narration shown to listeners beside the code")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: err.Error()}
	}
	if fs.NArg() > 0 {
		return usagef("push takes no positional arguments (use -f for a file, or pipe on stdin), got %q",
			strings.Join(fs.Args(), " "))
	}

	doc, err := readDocument(*file, stdin)
	if err != nil {
		return err
	}

	var snap snapshot
	if err := c.post(ctx, "/api/code", codeRequest{Code: doc, Message: *message}, &snap); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSONLine(out, snap)
	}
	fmt.Fprintf(out, "pushed version %d\n", snap.Version)
	return nil
}

// messageRequest is the documented POST /api/message body. The CLI does not
// pre-validate emptiness: the server rejects a blank narration with its own
// message, and duplicating that rule here would be a second contract to drift.
type messageRequest struct {
	Message string `json:"message"`
}

// cmdMessage records narration without touching the document or the version.
func cmdMessage(ctx context.Context, c *client, args []string, out io.Writer) error {
	if len(args) == 0 {
		return usagef("message needs the text to say: agentcli message \"four on the floor\"")
	}
	// The args are joined so an unquoted multi-word narration works from a
	// shell script without the caller having to remember the quoting.
	text := strings.Join(args, " ")
	var snap snapshot
	if err := c.post(ctx, "/api/message", messageRequest{Message: text}, &snap); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSONLine(out, snap)
	}
	// The version is echoed deliberately: narration must NOT bump it, so the
	// unchanged version is the confirmation that only the words moved.
	fmt.Fprintf(out, "message set at version %d (version unchanged)\n", snap.Version)
	return nil
}

// cmdTransport implements hush and play.
//
// These endpoints take no body and the server sends NO body back for them — an
// empty JSON object would be fine, but sending nothing at all is what the
// contract describes, and a client that posted {"playing": false} would be
// rejected with a 400 for smuggling a field through an argument-free endpoint.
//
// It is also where "accepted" and "applied" come apart. The endpoints are
// idempotent: hushing an already-hushed performance succeeds, changes nothing,
// and still broadcasts a transport frame. So the state is read FIRST and
// compared, and a no-op is reported as a no-op rather than as a fresh change.
//
// The comparison is a report, not a guarantee — another agent could hush in
// between — so the wording describes what was observed ("was already hushed at
// version 3") rather than promising what the server holds now. A no-op is still
// SUCCESS (the request was accepted), so the exit code is 0; the distinct
// wording is what stops a caller reading it as a change.
func cmdTransport(ctx context.Context, c *client, name string, playing bool, args []string, out io.Writer) error {
	if len(args) > 0 {
		return usagef("%s takes no arguments, got %q", name, strings.Join(args, " "))
	}
	before, err := c.get(ctx, "/api/state")
	if err != nil {
		return err
	}
	// A nil body means no body is sent: http.NewRequestWithContext gets a nil
	// reader and the request carries ContentLength 0. Anything else here would
	// be rejected with a 400 for smuggling a field through an argument-free
	// endpoint.
	var snap snapshot
	if err := c.post(ctx, "/api/"+name, nil, &snap); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSONLine(out, snap)
	}
	if before.Playing == playing {
		fmt.Fprintf(out, "%s: accepted, no change (the performance was already %s at version %d)\n",
			name, playingWord(playing), snap.Version)
		return nil
	}
	fmt.Fprintf(out, "%s: %s (version %d)\n", name, playingWord(playing), snap.Version)
	return nil
}

// playingWord names the state a transport command asked for.
func playingWord(playing bool) string {
	if playing {
		return "playing"
	}
	return "hushed"
}

// evalResultRequest is the documented POST /api/eval-result body. Stats are
// opaque client-supplied JSON and are forwarded verbatim.
type evalResultRequest struct {
	Version int64           `json:"version"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Stats   json.RawMessage `json:"stats,omitempty"`
}

// cmdEvalResult reports how a published version evaluated, and is the one command
// where the server's own answer is genuinely insufficient.
//
// The endpoint replies {"accepted": true} for a report it UNDERSTOOD, including
// one it then deliberately discarded as stale — accepted means well-formed, not
// stored, and the wire carries no marker telling the two apart. Printing "ok"
// off the back of accepted:true would tell an agent its verdict is now the
// server's verdict when it is not.
//
// So the state is read first and the stored verdict compared. The command is for
// a browser-shaped report relayed by an operator, and the pre-read is what makes
// its output trustworthy; the alternative was printing a success the response
// does not support.
func cmdEvalResult(ctx context.Context, c *client, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("eval-result", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	ver := fs.Int64("version", 0, "the published version this verdict is about (required)")
	ok := fs.Bool("ok", true, "report whether the version evaluated")
	errText := fs.String("error", "", "the error text, when the evaluation threw")
	stats := fs.String("stats", "", "opaque stats JSON, e.g. '{\"haps\":64}'")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: err.Error()}
	}
	if fs.NArg() > 0 {
		return usagef("eval-result takes no positional arguments, got %q", strings.Join(fs.Args(), " "))
	}
	if *ver <= 0 {
		return usagef("eval-result needs -version (a published version is 1 or greater)")
	}
	req := evalResultRequest{Version: *ver, OK: *ok, Error: *errText}
	if *stats != "" {
		if !json.Valid([]byte(*stats)) {
			return usagef("-stats must be valid JSON, got %q", *stats)
		}
		req.Stats = json.RawMessage(*stats)
	}

	before, err := c.get(ctx, "/api/state")
	if err != nil {
		return err
	}

	var ack evalAck
	if err := c.post(ctx, "/api/eval-result", req, &ack); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSONLine(out, ack)
	}

	stale := before.LastEvalResult != nil && *ver < before.LastEvalResult.Version
	if stale {
		fmt.Fprintf(out, "eval-result: accepted but IGNORED for version %d (stale: version %d is already the stored verdict)\n",
			*ver, before.LastEvalResult.Version)
		return nil
	}
	fmt.Fprintf(out, "eval-result: stored for version %d (ok=%t)\n", ack.Version, *ok)
	return nil
}

// anchorRequest is the documented POST /api/anchor body. Both fields are
// required by the contract — a re-anchor REPLACES the whole timeline rather
// than patching one half of it — so this type sends both unconditionally,
// even when the caller only wanted to change one of them.
type anchorRequest struct {
	EpochMS int64   `json:"epochMs"`
	CPS     float64 `json:"cps"`
}

// cmdAnchor republishes the shared timeline every listener schedules against.
//
// It is here because the CLI is one-to-one with the documented endpoints: POST
// /api/anchor was the last route reachable only by hand-rolled curl, and a
// wrapper that covers six of seven endpoints is a wrapper whose gaps get
// hand-rolled curl back.
//
// The two load-bearing properties apply, and the second one is real here rather
// than theoretical: a re-anchor with unchanged values is idempotent — the server
// accepts it, stores the same anchor and broadcasts it — so the wire carries no
// marker distinguishing "moved the shared bar grid" from "said it again". The
// state is therefore read FIRST and compared, and a re-send is reported as
// "no change" rather than as a fresh re-anchor.
//
// A REFUSED anchor needs no such care to be safe, and this is deliberate: the
// server answers 400 with its own reason, the client returns that error
// verbatim, and run() maps it to exit 1. There is no accepted-but-ignored case
// to paper over here — the uncertainty is the other way round, in the
// comparison, which is a report of what was OBSERVED rather than a guarantee
// about what the server holds now (another agent could re-anchor in between).
func cmdAnchor(ctx context.Context, c *client, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("anchor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	epoch := fs.Int64("epoch-ms", 0, "the shared timeline's epoch, in Unix milliseconds (default: the current epoch)")
	cps := fs.Float64("cps", 0, "cycles per second, greater than 0 and at most 1000 (default: the current rate)")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: err.Error()}
	}
	if fs.NArg() > 0 {
		return usagef("anchor takes no positional arguments, got %q", strings.Join(fs.Args(), " "))
	}
	// Visit rather than a sentinel default: epoch 0 and cps 0 are both invalid
	// to the server, so a default value cannot double as "was it supplied".
	supplied := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { supplied[f.Name] = true })
	if !supplied["epoch-ms"] && !supplied["cps"] {
		return usagef("anchor needs -epoch-ms and/or -cps (got neither; read the current anchor with 'agentcli state')")
	}

	before, err := c.get(ctx, "/api/state")
	if err != nil {
		return err
	}

	// The half the caller did not name is carried over from the state just
	// read, which is exactly what the contract tells an agent to do by hand
	// ("resend the current epochMs") — so the CLI cannot half-anchor the way a
	// hand-written body easily could.
	req := anchorRequest{EpochMS: before.Anchor.EpochMS, CPS: before.Anchor.CPS}
	if supplied["epoch-ms"] {
		req.EpochMS = *epoch
	}
	if supplied["cps"] {
		req.CPS = *cps
	}

	var snap snapshot
	if err := c.post(ctx, "/api/anchor", req, &snap); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSONLine(out, snap)
	}

	if req.EpochMS == before.Anchor.EpochMS && req.CPS == before.Anchor.CPS {
		fmt.Fprintf(out, "anchor: accepted, no change (the timeline was already epochMs=%d cps=%g)\n",
			snap.Anchor.EpochMS, snap.Anchor.CPS)
		return nil
	}
	// The version is echoed for the same reason `message` echoes it: a re-anchor
	// must NOT bump it, so the unchanged number is the confirmation that only
	// the timeline moved.
	fmt.Fprintf(out, "anchor: epochMs=%d cps=%g (version %d unchanged)\n",
		snap.Anchor.EpochMS, snap.Anchor.CPS, snap.Version)
	return nil
}

// readDocument loads the document to push, from a file or from stdin.
//
// "-" and an omitted -f both mean stdin. The size bound is deliberately looser
// than the server's 64 KiB cap: this exists to stop an unbounded read, not to
// pre-empt the server's rejection, which is reported with its own message.
func readDocument(file string, stdin io.Reader) (string, error) {
	if file != "" && file != "-" {
		src, err := os.Open(file)
		if err != nil {
			return "", usagef("cannot read the document: %v", err)
		}
		defer func() { _ = src.Close() }()
		return readAllBounded(src, "file "+file)
	}
	return readAllBounded(stdin, "stdin")
}

// readAllBounded reads at most maxDocBytes and reports an over-long input as the
// usage error it is, rather than sending a body the server will only reject.
func readAllBounded(r io.Reader, what string) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxDocBytes+1))
	if err != nil {
		return "", usagef("cannot read the document from %s: %v", what, err)
	}
	if len(raw) > maxDocBytes {
		return "", usagef("the document read from %s is larger than %d bytes", what, maxDocBytes)
	}
	return string(raw), nil
}

// writeJSONLine prints a value as one line of JSON, for the -json mode a script
// pipes into jq.
func writeJSONLine(out io.Writer, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cannot encode the response for output: %w", err)
	}
	_, err = fmt.Fprintf(out, "%s\n", encoded)
	return err
}

// orNone renders an empty string as something a human can read as "unset"
// rather than as a blank that looks like a formatting bug.
func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
