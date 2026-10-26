// Package cli implements the zipcode command.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/vichugof/zipcodeout/internal/geo"
)

// Exit codes returned by Run.
const (
	ExitOK          = 0
	ExitFailure     = 1   // the lookup failed or timed out
	ExitUsage       = 2   // invalid flags or configuration
	ExitNoZipCode   = 3   // a location was found, but without a zip code
	ExitInterrupted = 130 // the context passed to Run was canceled
)

// BaseURLEnv names the environment variable that serves every provider from
// another base URL, such as a local fake API in tests.
const BaseURLEnv = "ZIPCODE_API_BASE_URL"

const defaultTimeout = 15 * time.Second

// Env is the outside world as Run sees it.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// Version is the version stamped at build time, if any.
	Version string
}

// options holds the parsed command line.
type options struct {
	json      bool
	providers []string
	timeout   time.Duration
	verbose   bool
	version   bool
}

// Run executes the zipcode command with args (excluding the program name)
// and returns the process exit code. Canceling ctx interrupts the lookup.
func Run(ctx context.Context, args []string, env Env) int {
	opts, err := parseArgs(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(env.Stdout, usage)
		return ExitOK
	case err != nil:
		fmt.Fprintf(env.Stderr, "zipcode: %v\nRun 'zipcode -h' for usage.\n", err)
		return ExitUsage
	}

	version := resolveVersion(env.Version)
	if opts.version {
		fmt.Fprintf(env.Stdout, "zipcode %s (%s %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return ExitOK
	}

	providers, err := geo.New(opts.providers, geo.Options{
		BaseURL:   env.Getenv(BaseURLEnv),
		UserAgent: "zipcode/" + strings.Trim(version, "()"),
	})
	if err != nil {
		fmt.Fprintf(env.Stderr, "zipcode: %s: %v\n", BaseURLEnv, err)
		return ExitUsage
	}

	lookupCtx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	loc, attempts, err := geo.NewChain(providers...).Locate(lookupCtx)
	if opts.verbose {
		writeAttempts(env.Stderr, "zipcode: ", attempts)
	}

	var msg string
	switch {
	case ctx.Err() != nil:
		msg = "interrupted"
	case err == nil:
		writeLocation(env.Stdout, loc, opts.json)
		return ExitOK
	case errors.Is(err, geo.ErrNoZipCode):
		writeLocation(env.Stdout, loc, opts.json)
		fmt.Fprintf(env.Stderr, "zipcode: warning: no provider knows the zip code for %s\n", place(loc))
		return ExitNoZipCode
	case errors.Is(err, context.DeadlineExceeded):
		msg = "timed out after " + opts.timeout.String()
	default:
		msg = "could not determine your location"
	}

	fmt.Fprintf(env.Stderr, "zipcode: %s\n", msg)
	if !opts.verbose {
		writeAttempts(env.Stderr, "  ", attempts)
	}
	if opts.json {
		writeJSONError(env.Stdout, msg, attempts)
	}
	if ctx.Err() != nil {
		return ExitInterrupted
	}
	return ExitFailure
}

func parseArgs(args []string) (options, error) {
	var (
		opts      options
		providers string
	)
	fs := flag.NewFlagSet("zipcode", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Run prints errors and usage itself
	fs.BoolVar(&opts.json, "json", false, "")
	fs.StringVar(&providers, "provider", strings.Join(geo.Names(), ","), "")
	fs.DurationVar(&opts.timeout, "timeout", defaultTimeout, "")
	fs.BoolVar(&opts.verbose, "verbose", false, "")
	fs.BoolVar(&opts.version, "version", false, "")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if opts.timeout <= 0 {
		return options{}, fmt.Errorf("invalid -timeout %s: must be greater than zero", opts.timeout)
	}
	names, err := geo.ParseNames(providers)
	if err != nil {
		return options{}, fmt.Errorf("invalid -provider: %w", err)
	}
	opts.providers = names
	return opts, nil
}

// resolveVersion prefers the version stamped with -ldflags, then the module
// version recorded by the go command, as for go install ...@v1.2.3.
func resolveVersion(stamped string) string {
	if stamped != "" {
		return stamped
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

// usage names the program "zipcode" rather than using os.Args[0], which is a
// full path such as C:\tools\zipcode.exe on Windows.
const usage = `Usage: zipcode [flags]

Print the zip code, city, region and country of your current location.

The location is estimated from your public IP address, so it is approximate:
VPNs, mobile networks and corporate proxies can place you somewhere else.
Your IP address is sent to each geolocation provider that is asked.

Flags:
  -json              print the result as a single JSON object
  -provider list     comma-separated providers to try, in order
                     (default "ipwhois,freeipapi,ipapico")
  -timeout duration  total time allowed for the lookup (default 15s)
  -verbose           report every provider attempt on stderr
  -version           print version information and exit
  -h, -help          show this help

Exit status:
  0    zip code found
  1    lookup failed or timed out
  2    invalid usage
  3    location found, but without a zip code
  130  interrupted
`
