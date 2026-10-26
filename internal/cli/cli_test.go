package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/vichugof/zipcodeout/internal/fakeapi"
)

type result struct {
	stdout, stderr string
	code           int
}

// run calls Run with args and an environment whose only variable points the
// providers at srv.
func run(ctx context.Context, srv *fakeapi.Server, args ...string) result {
	return runWithBaseURL(ctx, srv.URL, args...)
}

func runWithBaseURL(ctx context.Context, baseURL string, args ...string) result {
	var stdout, stderr bytes.Buffer
	code := Run(ctx, args, Env{
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(key string) string {
			if key == BaseURLEnv {
				return baseURL
			}
			return ""
		},
		Version: "v1.2.3",
	})
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func serveFixture(t *testing.T, srv *fakeapi.Server, provider, fixture string) {
	t.Helper()
	srv.Handle(provider, fakeapi.JSON(fakeapi.Fixture(t, fixture)))
}

func wantCode(t *testing.T, res result, code int) {
	t.Helper()
	if res.code != code {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", res.code, code, res.stdout, res.stderr)
	}
}

func TestRunPrintsLocation(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serveFixture(t, srv, "ipwhois", "ipwhois_ok.json")

	res := run(t.Context(), srv)
	wantCode(t, res, ExitOK)
	want := "Zip code: 10001\nCity:     New York\nRegion:   New York\nCountry:  United States (US)\n"
	if res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want nothing", res.stderr)
	}
	hits := srv.Hits()
	if len(hits) != 1 || hits[0].Provider != "ipwhois" {
		t.Fatalf("requests = %v, want one to ipwhois", srv.Order())
	}
	if ua := hits[0].Header.Get("User-Agent"); ua != "zipcode/v1.2.3" {
		t.Errorf("User-Agent = %q, want zipcode/v1.2.3", ua)
	}
}

func TestRunPrintsJSON(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serveFixture(t, srv, "ipwhois", "ipwhois_ok.json")

	res := run(t.Context(), srv, "-json")
	wantCode(t, res, ExitOK)
	want := `{"zip_code":"10001","city":"New York","region":"New York","country":"United States","country_code":"US","ip":"203.0.113.7","provider":"ipwhois"}` + "\n"
	if res.stdout != want {
		t.Errorf("stdout = %s, want %s", res.stdout, want)
	}
}

func TestRunFallsBackAndReportsAttempts(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	srv.Handle("ipwhois", fakeapi.Status(http.StatusTooManyRequests))
	serveFixture(t, srv, "freeipapi", "freeipapi_ok.json")

	res := run(t.Context(), srv, "-verbose")
	wantCode(t, res, ExitOK)
	if !strings.HasPrefix(res.stdout, "Zip code: 60601\nCity:     Chicago\n") {
		t.Errorf("stdout = %q, want the freeipapi location", res.stdout)
	}
	for _, want := range []string{
		"zipcode: ipwhois: rate_limited (",
		"): HTTP 429 Too Many Requests\n",
		"zipcode: freeipapi: ok (",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", res.stderr, want)
		}
	}
}

func TestRunProviderFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		list string
		want []string // providers asked, in order
	}{
		{"freeipapi", []string{"freeipapi"}},
		{"ipapico,ipwhois", []string{"ipapico", "ipwhois"}},
	}
	for _, tt := range tests {
		t.Run(tt.list, func(t *testing.T) {
			t.Parallel()
			srv := fakeapi.New(t) // ipapico answers 503
			serveFixture(t, srv, "ipwhois", "ipwhois_ok.json")
			serveFixture(t, srv, "freeipapi", "freeipapi_ok.json")

			res := run(t.Context(), srv, "-provider", tt.list)
			wantCode(t, res, ExitOK)
			if got := srv.Order(); !slices.Equal(got, tt.want) {
				t.Errorf("providers asked = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args []string
		msg  string
	}{
		{[]string{"-bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"-provider", "nope"}, `unknown provider "nope"`},
		{[]string{"-provider", "ipwhois,ipwhois"}, `provider "ipwhois" is listed twice`},
		{[]string{"-provider", ""}, "empty provider name"},
		{[]string{"-timeout", "0s"}, "invalid -timeout 0s: must be greater than zero"},
		{[]string{"-timeout", "soon"}, `invalid value "soon" for flag -timeout`},
		{[]string{"extra"}, `unexpected argument "extra"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			t.Parallel()
			srv := fakeapi.New(t)
			res := run(t.Context(), srv, tt.args...)
			wantCode(t, res, ExitUsage)
			if !strings.Contains(res.stderr, tt.msg) || !strings.HasSuffix(res.stderr, "Run 'zipcode -h' for usage.\n") {
				t.Errorf("stderr = %q, want it to contain %q and point to -h", res.stderr, tt.msg)
			}
			if res.stdout != "" {
				t.Errorf("stdout = %q, want nothing", res.stdout)
			}
			if hits := srv.Hits(); len(hits) != 0 {
				t.Errorf("made %d requests, want 0", len(hits))
			}
		})
	}
}

func TestRunHelp(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-h", "-help", "--help"} {
		srv := fakeapi.New(t)
		res := run(t.Context(), srv, flag)
		wantCode(t, res, ExitOK)
		if res.stdout != usage || res.stderr != "" {
			t.Errorf("%s: stdout = %q, stderr = %q; want the usage on stdout only", flag, res.stdout, res.stderr)
		}
	}
	for _, flag := range []string{"-json", "-provider", "-timeout", "-verbose", "-version", "approximate"} {
		if !strings.Contains(usage, flag) {
			t.Errorf("usage does not mention %s", flag)
		}
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	res := run(t.Context(), srv, "-version")
	wantCode(t, res, ExitOK)
	want := "zipcode v1.2.3 (" + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + ")\n"
	if res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
}

func TestResolveVersion(t *testing.T) {
	t.Parallel()
	if got := resolveVersion("v2.0.0"); got != "v2.0.0" {
		t.Errorf("resolveVersion(v2.0.0) = %q", got)
	}
	if got := resolveVersion(""); got == "" {
		t.Error("resolveVersion(\"\") is empty, want the build info version or (devel)")
	}
}

func TestRunRejectsInsecureBaseURL(t *testing.T) {
	t.Parallel()
	res := runWithBaseURL(t.Context(), "http://geo.example.com")
	wantCode(t, res, ExitUsage)
	if !strings.Contains(res.stderr, "zipcode: ZIPCODE_API_BASE_URL: invalid base URL") {
		t.Errorf("stderr = %q, want it to name ZIPCODE_API_BASE_URL", res.stderr)
	}
}

// failingServer answers 503, drops the connection, and blocks with an HTML
// page, for ipwhois, freeipapi and ipapico respectively.
func failingServer(t *testing.T) *fakeapi.Server {
	srv := fakeapi.New(t)
	srv.Handle("ipwhois", fakeapi.Status(http.StatusServiceUnavailable))
	srv.Handle("freeipapi", fakeapi.Drop())
	srv.Handle("ipapico", fakeapi.HTML403())
	return srv
}

func TestRunAllProvidersFail(t *testing.T) {
	t.Parallel()
	res := run(t.Context(), failingServer(t))
	wantCode(t, res, ExitFailure)
	if res.stdout != "" {
		t.Errorf("stdout = %q, want nothing", res.stdout)
	}
	for _, want := range []string{
		"zipcode: could not determine your location\n",
		"  ipwhois: unavailable (",
		"): HTTP 503 Service Unavailable\n",
		"  freeipapi: unavailable (",
		"  ipapico: rejected (",
		"): HTTP 403 Forbidden\n",
	} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", res.stderr, want)
		}
	}
}

func TestRunAllProvidersFailJSON(t *testing.T) {
	t.Parallel()
	res := run(t.Context(), failingServer(t), "-json")
	wantCode(t, res, ExitFailure)

	var doc jsonError
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, res.stdout)
	}
	if doc.Error != "could not determine your location" {
		t.Errorf("error = %q", doc.Error)
	}
	var got []string
	for _, a := range doc.Attempts {
		got = append(got, a.Provider+":"+a.Kind)
		if a.Message == "" {
			t.Errorf("attempt %s has no message", a.Provider)
		}
	}
	if want := []string{"ipwhois:unavailable", "freeipapi:unavailable", "ipapico:rejected"}; !slices.Equal(got, want) {
		t.Errorf("attempts = %v, want %v", got, want)
	}
	if !strings.Contains(res.stderr, "could not determine your location") {
		t.Errorf("stderr = %q, want a summary as well", res.stderr)
	}
}

func TestRunNoZipCode(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	serveFixture(t, srv, "ipwhois", "ipwhois_nozip.json")
	serveFixture(t, srv, "freeipapi", "freeipapi_nozip.json")
	serveFixture(t, srv, "ipapico", "ipapico_nozip.json")

	res := run(t.Context(), srv)
	wantCode(t, res, ExitNoZipCode)
	want := "Zip code: unknown\nCity:     Paris\nRegion:   Île-de-France\nCountry:  France (FR)\n"
	if res.stdout != want {
		t.Errorf("stdout = %q, want the first partial location %q", res.stdout, want)
	}
	if !strings.Contains(res.stderr, "zipcode: warning: no provider knows the zip code for Paris, France (FR)") {
		t.Errorf("stderr = %q, want a warning", res.stderr)
	}

	res = run(t.Context(), srv, "-json")
	wantCode(t, res, ExitNoZipCode)
	if !strings.HasPrefix(res.stdout, `{"zip_code":"","city":"Paris",`) {
		t.Errorf("stdout = %s, want the partial location with an empty zip_code", res.stdout)
	}
}

func TestRunTimeout(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	for _, p := range fakeapi.Providers {
		srv.Handle(p, srv.Hang())
	}

	res := run(t.Context(), srv, "-timeout", "300ms", "-json")
	wantCode(t, res, ExitFailure)
	if !strings.HasPrefix(res.stderr, "zipcode: timed out after 300ms\n") {
		t.Errorf("stderr = %q, want a timeout message", res.stderr)
	}
	var doc jsonError
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, res.stdout)
	}
	if doc.Error != "timed out after 300ms" || len(doc.Attempts) != 3 {
		t.Errorf("document = %+v, want a timeout with 3 attempts", doc)
	}
	for _, a := range doc.Attempts {
		if a.Kind != "timeout" {
			t.Errorf("attempt %s: kind = %s, want timeout", a.Provider, a.Kind)
		}
	}
}

func TestRunInterruptedBeforeLookup(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res := run(ctx, srv, "-json")
	wantCode(t, res, ExitInterrupted)
	if res.stderr != "zipcode: interrupted\n" {
		t.Errorf("stderr = %q, want only the interruption", res.stderr)
	}
	if want := `{"error":"interrupted","attempts":[]}` + "\n"; res.stdout != want {
		t.Errorf("stdout = %s, want %s", res.stdout, want)
	}
	if hits := srv.Hits(); len(hits) != 0 {
		t.Errorf("made %d requests, want 0", len(hits))
	}
}

func TestRunInterruptedDuringLookup(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv.Handle("ipwhois", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel() // as if the user pressed Ctrl+C while waiting for ipwhois
		srv.Hang().ServeHTTP(w, r)
	}))
	serveFixture(t, srv, "freeipapi", "freeipapi_ok.json")

	res := run(ctx, srv)
	wantCode(t, res, ExitInterrupted)
	if got := srv.Order(); !slices.Equal(got, []string{"ipwhois"}) {
		t.Errorf("providers asked = %v, want only ipwhois", got)
	}
	if !strings.HasPrefix(res.stderr, "zipcode: interrupted\n") {
		t.Errorf("stderr = %q, want an interruption message", res.stderr)
	}
}
