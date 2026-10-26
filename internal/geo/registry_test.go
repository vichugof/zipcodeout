package geo

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/vichugof/zipcodeout/internal/fakeapi"
)

func TestDefaultOrder(t *testing.T) {
	t.Parallel()
	want := []string{"ipwhois", "freeipapi", "ipapico"}
	if got := Names(); !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if !slices.Equal(fakeapi.Providers, want) {
		t.Errorf("fakeapi.Providers = %v, want %v", fakeapi.Providers, want)
	}
}

func TestParseNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want []string
		err  string
	}{
		{in: "ipwhois,freeipapi,ipapico", want: []string{"ipwhois", "freeipapi", "ipapico"}},
		{in: "ipapico,ipwhois", want: []string{"ipapico", "ipwhois"}},
		{in: " IPWhois , freeipapi ", want: []string{"ipwhois", "freeipapi"}},
		{in: "", err: "empty provider name"},
		{in: "ipwhois,", err: "empty provider name"},
		{in: "ip-api", err: `unknown provider "ip-api" (choose from ipwhois, freeipapi, ipapico)`},
		{in: "ipwhois,ipwhois", err: `provider "ipwhois" is listed twice`},
	}
	for _, tt := range tests {
		got, err := ParseNames(tt.in)
		switch {
		case tt.err != "":
			if err == nil || err.Error() != tt.err {
				t.Errorf("ParseNames(%q) error = %v, want %q", tt.in, err, tt.err)
			}
		case err != nil:
			t.Errorf("ParseNames(%q): %v", tt.in, err)
		case !slices.Equal(got, tt.want):
			t.Errorf("ParseNames(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestNewRejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	if _, err := New([]string{"nope"}, Options{}); err == nil {
		t.Error("New accepted an unknown provider")
	}
}

func TestBaseURLMovesEveryProvider(t *testing.T) {
	t.Parallel()
	srv := fakeapi.New(t) // every provider answers 503
	providers, err := New(Names(), Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range providers {
		if _, err := p.Locate(t.Context()); err == nil {
			t.Errorf("%s: Locate succeeded against a 503", p.Name())
		}
	}
	var paths []string
	for _, h := range srv.Hits() {
		paths = append(paths, h.Path)
	}
	want := []string{"/ipwhois/", "/freeipapi/api/v1/json", "/ipapico/json/"}
	if !slices.Equal(paths, want) {
		t.Errorf("requested paths = %v, want %v", paths, want)
	}
}

func TestRebase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		base, name, endpoint, want string
	}{
		{"http://127.0.0.1:8080", "ipwhois", "https://ipwho.is/", "http://127.0.0.1:8080/ipwhois/"},
		{"http://127.0.0.1:8080/", "freeipapi", "https://free.freeipapi.com/api/v1/json", "http://127.0.0.1:8080/freeipapi/api/v1/json"},
		{"https://proxy.example.com/geo/", "ipapico", "https://ipapi.co/json/", "https://proxy.example.com/geo/ipapico/json/"},
		{"https://proxy.example.com", "x", "https://api.example.com/json?fields=zip,city", "https://proxy.example.com/x/json?fields=zip,city"},
	}
	for _, tt := range tests {
		base, err := url.Parse(tt.base)
		if err != nil {
			t.Fatal(err)
		}
		got, err := rebase(base, tt.name, tt.endpoint)
		if err != nil || got != tt.want {
			t.Errorf("rebase(%s, %s, %s) = %q, %v; want %q", tt.base, tt.name, tt.endpoint, got, err, tt.want)
		}
	}
}

func TestParseBaseURL(t *testing.T) {
	t.Parallel()
	valid := []string{
		"https://geo.example.com",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
		"http://localhost:8080",
	}
	for _, raw := range valid {
		if _, err := parseBaseURL(raw); err != nil {
			t.Errorf("parseBaseURL(%q): %v", raw, err)
		}
	}
	invalid := map[string]string{
		"http://geo.example.com":       "use https",
		"http://127.0.0.1.example.com": "use https",
		"ftp://127.0.0.1":              "use https",
		"127.0.0.1:8080":               "invalid base URL",
		"https://":                     "no host",
	}
	for raw, msg := range invalid {
		_, err := parseBaseURL(raw)
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("parseBaseURL(%q) error = %v, want it to mention %q", raw, err, msg)
		}
	}
}
