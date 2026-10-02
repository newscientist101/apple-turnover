package main

import (
	"flag"
	"os"
	"testing"
)

func TestRunInvalidAddress(t *testing.T) {
	// Save original args and flag state
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	})

	// Reset flag.CommandLine for testing run()
	flag.CommandLine = flag.NewFlagSet("srv", flag.ContinueOnError)
	flagListenAddr = flag.CommandLine.String("listen", "invalid-address-string:99999", "address to listen on")

	os.Args = []string{"srv", "-listen", "invalid-address-string:99999"}

	err := run()
	if err == nil {
		t.Fatal("expected error from run() with invalid listen address, got nil")
	}
}
