package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vichugof/zipcodeout/internal/geo"
)

// TestLive asks the real providers where this machine is. It is skipped
// unless ZIPCODE_LIVE_TEST=1, because it sends the machine's public IP
// address to each of them. It fails only when a provider's response no
// longer has the shape the tool expects; rate limits, outages and missing
// zip codes are reported as skips.
func TestLive(t *testing.T) {
	if os.Getenv("ZIPCODE_LIVE_TEST") != "1" {
		t.Skip("set ZIPCODE_LIVE_TEST=1 to query the real providers")
	}
	for _, name := range geo.Names() {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), binPath, "-json", "-provider", name, "-timeout", "20s")
			cmd.Env = liveEnv()
			cmd.WaitDelay = 5 * time.Second
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			switch code := exitCode(t, cmd.Run()); code {
			case 0:
				loc := decodeLocation(t, stdout.String())
				if loc.Provider != name || loc.ZipCode == "" || (loc.Country == "" && loc.CountryCode == "") {
					t.Errorf("incomplete location: %+v", loc)
				}
				t.Logf("%s: zip code %s, %s, %s (%s)", name, loc.ZipCode, loc.City, loc.Country, loc.CountryCode)
			case 3:
				t.Skipf("%s located this machine but has no zip code for it: %s", name, strings.TrimSpace(stdout.String()))
			case 1:
				var doc failureDoc
				if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
					t.Fatalf("stdout is not a JSON document: %v\n%s", err, stdout.String())
				}
				for _, a := range doc.Attempts {
					if a.Kind == string(geo.KindBadResponse) {
						t.Fatalf("%s's response no longer matches what the tool expects: %s", name, a.Message)
					}
				}
				t.Skipf("%s is not usable right now: %s", name, strings.TrimSpace(stderr.String()))
			default:
				t.Fatalf("exit code %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
			}
		})
	}
}

// liveEnv is the test's environment without ZIPCODE_ variables. It keeps any
// proxy settings the machine needs to reach the internet.
func liveEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(strings.ToUpper(kv), "ZIPCODE_")
	})
}
