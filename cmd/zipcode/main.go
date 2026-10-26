// Command zipcode prints the zip code, city, region and country of the
// machine it runs on, estimated from its public IP address.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/vichugof/zipcodeout/internal/cli"
)

// version is stamped at build time with -ldflags "-X main.version=v1.2.3".
var version string

func main() {
	// Ctrl+C (and, on Windows, closing the console) cancels the lookup.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// After the first signal, restore the default handling so that a second
	// Ctrl+C ends the process at once.
	context.AfterFunc(ctx, stop)

	code := cli.Run(ctx, os.Args[1:], cli.Env{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Getenv:  os.Getenv,
		Version: version,
	})
	stop()
	os.Exit(code)
}
