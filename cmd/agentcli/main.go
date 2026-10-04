package main

// agentcli drives the live performance from a shell or a script, so the harness
// never has to hand-roll curl for the things it does every iteration:
// read the state, push code, narrate, re-anchor, hush and play.
//
// It is a CLIENT of the documented HTTP contract in AGENT_API.md and adds no
// behaviour of its own to the server. Every subcommand maps one-to-one onto one
// endpoint — that one-to-one property is the reason `anchor` exists at all
// (issue strudel-agent-3vo.8.4): a wrapper that covers six of the seven
// documented endpoints is a wrapper whose one gap gets hand-rolled curl back.
// Every value it prints comes from a server response.
//
// Two properties are worth stating up front, because both are easy to get
// quietly wrong in a convenience wrapper:
//
//  1. A rejection is reported VERBATIM. The server's {"error": "..."} string is
//     printed as-is and the exit code is non-zero, so a scripted caller can tell
//     a refusal from a success without parsing prose.
//
//  2. "Accepted" is not "applied". The server deliberately answers
//     accepted:true for a report it understood and then discarded as stale, and
//     hush/play are idempotent. Wherever those two cases are possible, the CLI
//     reads the state first, compares, and says plainly which one happened — an
//     "ok" printed over an unchanged performance is how an agent concludes its
//     push landed when it did not.
//
// Exit codes: 0 success, 1 the server refused, could not be reached, or did not
// give the caller what it asked for (see `state -require-current`), 2 the command
// line was wrong (nothing was sent).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// baseURLEnv is the environment variable read when -base is not given, so a
// harness can point every invocation at one server without repeating a flag.
const baseURLEnv = "STRUDEL_AGENT_URL"

// Exit codes. Named so the tests assert on the contract rather than on literals.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is main's whole body, parameterised so the tests can drive it in-process
// and assert on both the output and the exit code. Nothing here touches the
// network directly; every command goes through the bounded client.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentcli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", envOr(baseURLEnv, "http://localhost:8000"),
		"base URL of the strudel server (env "+baseURLEnv+")")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request timeout")
	asJSON := fs.Bool("json", false, "print the raw server JSON instead of prose")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: agentcli [flags] <command> [args]\n\n")
		fmt.Fprintf(stderr, "commands:\n")
		fmt.Fprintf(stderr, "  state [-require-current]   print the current snapshot, and whether its verdict is current\n")
		fmt.Fprintf(stderr, "  push [-f file] [-m text]  publish a new document (default: stdin)\n")
		fmt.Fprintf(stderr, "  message <text>            set the agent narration\n")
		fmt.Fprintf(stderr, "  hush                      stop the performance\n")
		fmt.Fprintf(stderr, "  play                      resume the performance\n")
		fmt.Fprintf(stderr, "  eval-result [flags]       report how the last version evaluated\n")
		fmt.Fprintf(stderr, "  anchor [flags]            re-anchor the shared timeline (-epoch-ms, -cps)\n")
		fmt.Fprintf(stderr, "\nflags:\n")
		fs.PrintDefaults()
	}

	// flag.ContinueOnError returns flag.ErrHelp for -h/--help AFTER printing the
	// usage text to stderr, so it is a successful request for help rather than a
	// bad command line. Every other parse error is a usage mistake.
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return exitUsage
	}

	command, cmdArgs := rest[0], rest[1:]

	// A deadline is established ONCE and shared by the pre-read and the write,
	// so a command that reads state and then posts cannot spend twice the
	// budget the user asked for.
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c, err := newClient(*base, *timeout, *asJSON)
	if err != nil {
		return report(stderr, err)
	}

	switch command {
	case "state":
		err = cmdState(ctx, c, cmdArgs, stdout, stderr)
	case "push":
		err = cmdPush(ctx, c, cmdArgs, stdin, stdout, stderr)
	case "message":
		err = cmdMessage(ctx, c, cmdArgs, stdout, stderr)
	case "hush":
		err = cmdTransport(ctx, c, "hush", false, cmdArgs, stdout, stderr)
	case "play":
		err = cmdTransport(ctx, c, "play", true, cmdArgs, stdout, stderr)
	case "eval-result":
		err = cmdEvalResult(ctx, c, cmdArgs, stdout, stderr)
	case "anchor":
		err = cmdAnchor(ctx, c, cmdArgs, stdout, stderr)
	case "help", "-h", "--help":
		fs.Usage()
		return exitOK
	default:
		fmt.Fprintf(stderr, "agentcli: unknown command %q\n\n", command)
		fs.Usage()
		return exitUsage
	}
	return report(stderr, err)
}

// report maps an error onto an exit code and prints it. The mapping is the
// contract: a usage mistake is 2 and a server refusal is 1, so a script can tell
// "you called me wrong" from "the server said no" without reading the text.
//
// errHelpRequested is checked FIRST and is the one error that prints nothing: the
// usage text was already written by parseFlags (or printFlaglessUsage) before the
// sentinel was returned, so printing it again here would duplicate it. It exits 0
// rather than 2 because asking a subcommand for help is a request that SUCCEEDED —
// the same distinction the top-level flagset draws when fs.Parse returns
// flag.ErrHelp. Treating help as a mistake is what made the per-subcommand flags
// undiscoverable in the first place (strudel-agent-uvj.12).
func report(stderr io.Writer, err error) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, errHelpRequested) {
		return exitOK
	}
	var usage *usageError
	if errors.As(err, &usage) {
		fmt.Fprintf(stderr, "agentcli: %v\n", usage.msg)
		return exitUsage
	}
	fmt.Fprintf(stderr, "agentcli: %v\n", err)
	return exitError
}

// envOr reads an environment variable, falling back when it is unset or empty.
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
