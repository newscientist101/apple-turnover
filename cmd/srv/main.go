package main

import (
	"flag"
	"fmt"
	"os"

	"strudelagent/srv"
)

var flagListenAddr = flag.String("listen", ":8000", "address to listen on")

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()
	return srv.New().Serve(*flagListenAddr)
}
