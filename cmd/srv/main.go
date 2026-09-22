package main

import (
	"flag"
	"fmt"
	"os"

	"srv.exe.dev/srv"
)

var flagListenAddr = flag.String("listen", ":8000", "address to listen on")

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func run() error {
	flag.Parse()
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return srv.New(hostname).Serve(*flagListenAddr)
}
