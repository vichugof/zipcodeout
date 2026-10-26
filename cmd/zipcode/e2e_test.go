package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vichugof/zipcodeout/internal/fakeapi"
	"github.com/vichugof/zipcodeout/internal/geo"
)

// binPath is the zipcode binary that TestMain builds.
var binPath string

// TestMain builds the real binary once, the way a release is built, so the
// tests run exactly what ships. Because these tests live in package main,
// go test's cache notices a change to any package the binary is built from.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "zipcode-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return 1
	}
	defer os.RemoveAll(dir)

	binPath = filepath.Join(dir, "zipcode")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	ctx := context.Background()
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", "-X main.version=e2e", "-o", binPath, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: building zipcode:", err)
		return 1
	}
	// The first run of a new executable can be slow on Windows while
	// Defender scans it. Absorb that here rather than in a timed test.
	_ = exec.CommandContext(ctx, binPath, "-version").Run()
	return m.Run()
}

type result struct {
	stdout, stderr string
	code           int
	elapsed        time.Duration
}

// runCLI runs the binary with args against the fake API at baseURL and
// waits for it to exit.
func runCLI(t *testing.T, baseURL string, args ...string) result {
	t.Helper()
	cmd := command(t, baseURL, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	return result{
		stdout:  stdout.String(),
		stderr:  stderr.String(),
		code:    exitCode(t, err),
		elapsed: time.Since(start),
	}
}

// command prepares the binary to run against the fake API at baseURL, in an
// environment that cannot reach the real providers.
func command(t *testing.T, baseURL string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(t.Context(), binPath, args...)
	cmd.Env = hermeticEnv(baseURL)
	// Do not hang on the output pipes if the test ends while the binary is
	// still running. A binary left running would also lock its directory
	// on Windows and break the cleanup in TestMain.
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// hermeticEnv returns the test's environment minus anything that could
// change where the binary sends requests, plus the fake API's base URL. The
// proxy variables point at a port nobody listens on, so a request that
// somehow escaped the fake would fail instead of reaching the internet. Go
// never proxies loopback addresses, so requests to the fake are unaffected.
func hermeticEnv(baseURL string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		name = strings.ToUpper(name) // Windows variable names ignore case
		return strings.HasPrefix(name, "ZIPCODE_") || strings.HasSuffix(name, "_PROXY")
	})
	return append(env,
		"ZIPCODE_API_BASE_URL="+baseURL,
		"HTTP_PROXY=http://127.0.0.1:9",
		"HTTPS_PROXY=http://127.0.0.1:9",
		"NO_PROXY=",
	)
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	default:
		t.Fatalf("running zipcode: %v", err)
		return -1
	}
}

func wantExit(t *testing.T, res result, code int) {
	t.Helper()
	if res.code != code {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", res.code, code, res.stdout, res.stderr)
	}
}

func wantOrder(t *testing.T, srv *fakeapi.Server, want ...string) {
	t.Helper()
	if got := srv.Order(); !slices.Equal(got, want) {
		t.Errorf("providers asked = %v, want %v", got, want)
	}
}

func serve(t *testing.T, srv *fakeapi.Server, provider, fixture string) {
	t.Helper()
	srv.Handle(provider, fakeapi.JSON(fakeapi.Fixture(t, fixture)))
}

func decodeLocation(t *testing.T, stdout string) geo.Location {
	t.Helper()
	var loc geo.Location
	if err := json.Unmarshal([]byte(stdout), &loc); err != nil {
		t.Fatalf("stdout is not a JSON location: %v\n%s", err, stdout)
	}
	return loc
}

// failureDoc is the JSON document printed with -json when the lookup fails.
type failureDoc struct {
	Error    string `json:"error"`
	Attempts []struct {
		Provider string `json:"provider"`
		Kind     string `json:"kind"`
		Message  string `json:"message"`
	} `json:"attempts"`
}

const (
	newYorkText = "Zip code: 10001\nCity:     New York\nRegion:   New York\nCountry:  United States (US)\n"
	chicagoText = "Zip code: 60601\nCity:     Chicago\nRegion:   Illinois\nCountry:  United States (US)\n"
	parisText   = "Zip code: unknown\nCity:     Paris\nRegion:   Île-de-France\nCountry:  France (FR)\n"
)

func TestVersion(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	res := runCLI(t, srv.URL, "-version")
	wantExit(t, res, 0)
	want := "zipcode e2e (" + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + ")\n"
	if res.stdout != want || res.stderr != "" {
		t.Errorf("stdout = %q, stderr = %q; want %q on stdout only", res.stdout, res.stderr, want)
	}
	wantOrder(t, srv)
}

func TestHelp(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	res := runCLI(t, srv.URL, "-h")
	wantExit(t, res, 0)
	if !strings.HasPrefix(res.stdout, "Usage: zipcode [flags]\n") || res.stderr != "" {
		t.Errorf("stdout = %q, stderr = %q; want the usage on stdout only", res.stdout, res.stderr)
	}
	wantOrder(t, srv)
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		msg  string
	}{
		{"unknown flag", []string{"-bogus"}, "flag provided but not defined: -bogus"},
		{"unknown provider", []string{"-provider", "nope"}, `unknown provider "nope"`},
		{"positional argument", []string{"10001"}, `unexpected argument "10001"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := fakeapi.New(t)
			res := runCLI(t, srv.URL, tt.args...)
			wantExit(t, res, 2)
			if res.stdout != "" || !strings.Contains(res.stderr, tt.msg) {
				t.Errorf("stdout = %q, stderr = %q; want nothing on stdout and %q on stderr", res.stdout, res.stderr, tt.msg)
			}
			wantOrder(t, srv)
		})
	}
}

func TestTextOutput(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serve(t, srv, "ipwhois", "ipwhois_ok.json")

	res := runCLI(t, srv.URL)
	wantExit(t, res, 0)
	if res.stdout != newYorkText || res.stderr != "" {
		t.Errorf("stdout = %q, stderr = %q; want %q on stdout only", res.stdout, res.stderr, newYorkText)
	}
	hits := srv.Hits()
	if len(hits) != 1 || hits[0].Provider != "ipwhois" {
		t.Fatalf("providers asked = %v, want only ipwhois", srv.Order())
	}
	if ua := hits[0].Header.Get("User-Agent"); ua != "zipcode/e2e" {
		t.Errorf("User-Agent = %q, want zipcode/e2e", ua)
	}
}

func TestJSONOutput(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serve(t, srv, "ipwhois", "ipwhois_ok.json")

	res := runCLI(t, srv.URL, "-json")
	wantExit(t, res, 0)
	want := `{"zip_code":"10001","city":"New York","region":"New York","country":"United States","country_code":"US","ip":"203.0.113.7","provider":"ipwhois"}` + "\n"
	if res.stdout != want {
		t.Errorf("stdout = %s, want %s", res.stdout, want)
	}
}

func TestFallsBackPastFailingProviders(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	srv.Handle("ipwhois", fakeapi.Status(http.StatusTooManyRequests))
	srv.Handle("freeipapi", fakeapi.JSON([]byte(`{"zipCode":`)))
	serve(t, srv, "ipapico", "ipapico_ok.json")

	res := runCLI(t, srv.URL, "-json", "-verbose")
	wantExit(t, res, 0)
	if loc := decodeLocation(t, res.stdout); loc.Provider != "ipapico" || loc.ZipCode != "98101" {
		t.Errorf("location = %+v, want Seattle from ipapico", loc)
	}
	wantOrder(t, srv, "ipwhois", "freeipapi", "ipapico")
	for _, want := range []string{
		"zipcode: ipwhois: rate_limited (", "HTTP 429 Too Many Requests",
		"zipcode: freeipapi: bad_response (", "invalid JSON",
		"zipcode: ipapico: ok (",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", res.stderr, want)
		}
	}
}

func TestFailureReportedWithStatus200(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serve(t, srv, "ipwhois", "ipwhois_failure.json")
	serve(t, srv, "freeipapi", "freeipapi_ok.json")

	res := runCLI(t, srv.URL)
	wantExit(t, res, 0)
	if res.stdout != chicagoText {
		t.Errorf("stdout = %q, want %q", res.stdout, chicagoText)
	}
	wantOrder(t, srv, "ipwhois", "freeipapi")
}

func TestNeverMixesProviders(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serve(t, srv, "ipwhois", "ipwhois_nozip.json") // Paris, without a zip code
	serve(t, srv, "freeipapi", "freeipapi_ok.json")

	res := runCLI(t, srv.URL)
	wantExit(t, res, 0)
	if res.stdout != chicagoText {
		t.Errorf("stdout = %q, want only freeipapi's location %q", res.stdout, chicagoText)
	}
}

func TestNoZipCodeAnywhere(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serve(t, srv, "ipwhois", "ipwhois_nozip.json")
	serve(t, srv, "freeipapi", "freeipapi_nozip.json")
	serve(t, srv, "ipapico", "ipapico_nozip.json")

	res := runCLI(t, srv.URL)
	wantExit(t, res, 3)
	if res.stdout != parisText {
		t.Errorf("stdout = %q, want the first partial location %q", res.stdout, parisText)
	}
	if !strings.Contains(res.stderr, "warning: no provider knows the zip code for Paris, France (FR)") {
		t.Errorf("stderr = %q, want a warning", res.stderr)
	}

	res = runCLI(t, srv.URL, "-json")
	wantExit(t, res, 3)
	if loc := decodeLocation(t, res.stdout); loc.ZipCode != "" || loc.City != "Paris" || !strings.Contains(res.stdout, `"zip_code":""`) {
		t.Errorf("stdout = %s, want Paris with an empty zip_code", res.stdout)
	}
}

func TestAllProvidersFail(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	srv.Handle("ipwhois", fakeapi.Status(http.StatusServiceUnavailable))
	srv.Handle("freeipapi", fakeapi.Drop())
	srv.Handle("ipapico", fakeapi.HTML403())

	res := runCLI(t, srv.URL)
	wantExit(t, res, 1)
	if res.stdout != "" {
		t.Errorf("stdout = %q, want nothing", res.stdout)
	}
	for _, want := range []string{
		"zipcode: could not determine your location\n",
		"ipwhois: unavailable", "freeipapi: unavailable", "ipapico: rejected",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", res.stderr, want)
		}
	}

	res = runCLI(t, srv.URL, "-json")
	wantExit(t, res, 1)
	var doc failureDoc
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", err, res.stdout)
	}
	var got []string
	for _, a := range doc.Attempts {
		got = append(got, a.Provider+":"+a.Kind)
	}
	if want := []string{"ipwhois:unavailable", "freeipapi:unavailable", "ipapico:rejected"}; doc.Error == "" || !slices.Equal(got, want) {
		t.Errorf("document = %+v, want attempts %v", doc, want)
	}
}

func TestSlowProviderFallsBack(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	srv.Handle("ipwhois", srv.Hang())
	serve(t, srv, "freeipapi", "freeipapi_ok.json")

	res := runCLI(t, srv.URL, "-timeout", "3s")
	wantExit(t, res, 0)
	if res.stdout != chicagoText {
		t.Errorf("stdout = %q, want %q", res.stdout, chicagoText)
	}
	hits := srv.Hits()
	if len(hits) != 2 {
		t.Fatalf("providers asked = %v, want ipwhois then freeipapi", srv.Order())
	}
	// ipwhois gets a third of the 3s budget before the tool moves on.
	if gap := hits[1].Time.Sub(hits[0].Time); gap < 900*time.Millisecond || gap > 2*time.Second {
		t.Errorf("freeipapi was asked %v after ipwhois, want about 1s", gap)
	}
	if res.elapsed >= 3*time.Second {
		t.Errorf("run took %v, want well under the 3s budget", res.elapsed)
	}
}

func TestAllProvidersHang(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	for _, p := range fakeapi.Providers {
		srv.Handle(p, srv.Hang())
	}

	res := runCLI(t, srv.URL, "-timeout", "1500ms")
	wantExit(t, res, 1)
	if !strings.HasPrefix(res.stderr, "zipcode: timed out after 1.5s\n") {
		t.Errorf("stderr = %q, want a timeout message", res.stderr)
	}
	if res.elapsed < 1400*time.Millisecond || res.elapsed > 5*time.Second {
		t.Errorf("run took %v, want about 1.5s", res.elapsed)
	}
	wantOrder(t, srv, "ipwhois", "freeipapi", "ipapico")
}

func TestProviderFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		list string
		want []string
	}{
		{"freeipapi", []string{"freeipapi"}},
		{"ipapico,ipwhois", []string{"ipapico", "ipwhois"}}, // ipapico answers 503
	}
	for _, tt := range tests {
		t.Run(tt.list, func(t *testing.T) {
			t.Parallel()
			srv := fakeapi.New(t)
			serve(t, srv, "ipwhois", "ipwhois_ok.json")
			serve(t, srv, "freeipapi", "freeipapi_ok.json")

			res := runCLI(t, srv.URL, "-provider", tt.list)
			wantExit(t, res, 0)
			wantOrder(t, srv, tt.want...)
		})
	}
}

func TestNonASCIIOutput(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serve(t, srv, "ipwhois", "ipwhois_unicode.json")

	res := runCLI(t, srv.URL)
	wantExit(t, res, 0)
	want := "Zip code: 01310-100\nCity:     São Paulo\nRegion:   São Paulo\nCountry:  Brazil (BR)\n"
	if res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}

	res = runCLI(t, srv.URL, "-json")
	wantExit(t, res, 0)
	if !strings.Contains(res.stdout, `"zip_code":"01310-100","city":"São Paulo","region":"São Paulo","country":"Brazil"`) {
		t.Errorf("stdout = %s, want the names unescaped", res.stdout)
	}
}

func TestInterrupt(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot send os.Interrupt to another process; the cli package tests cover cancellation")
	}
	srv := fakeapi.New(t)
	asked := make(chan struct{}, 1)
	srv.Handle("ipwhois", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case asked <- struct{}{}:
		default:
		}
		srv.Hang().ServeHTTP(w, r)
	}))
	serve(t, srv, "freeipapi", "freeipapi_ok.json")

	cmd := command(t, srv.URL)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-asked:
	case <-time.After(10 * time.Second):
		t.Fatal("zipcode never contacted the fake API")
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if code := exitCode(t, cmd.Wait()); code != 130 {
		t.Errorf("exit code = %d, want 130\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "zipcode: interrupted") {
		t.Errorf("stderr = %q, want an interruption message", stderr.String())
	}
	wantOrder(t, srv, "ipwhois")
}
