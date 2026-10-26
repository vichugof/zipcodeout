package geo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vichugof/zipcodeout/internal/fakeapi"
)

const testUserAgent = "zipcode/test"

// newTestProvider returns the named provider, talking to srv.
func newTestProvider(t *testing.T, srv *fakeapi.Server, name string) Provider {
	t.Helper()
	providers, err := New([]string{name}, Options{BaseURL: srv.URL, UserAgent: testUserAgent})
	if err != nil {
		t.Fatalf("New(%s): %v", name, err)
	}
	return providers[0]
}

// wantFailure checks that err is a *ProviderError from provider, of the given
// kind, whose message contains msg.
func wantFailure(t *testing.T, err error, provider string, kind Kind, msg string) {
	t.Helper()
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v (%T), want a *ProviderError", err, err)
	}
	if pe.Provider != provider || pe.Kind != kind {
		t.Errorf("error = %v, want provider %s and kind %s", err, provider, kind)
	}
	if !strings.Contains(pe.Err.Error(), msg) {
		t.Errorf("error message = %q, want it to contain %q", pe.Err, msg)
	}
}

// static adapts a handler that does not need the server.
func static(h http.Handler) func(*fakeapi.Server) http.Handler {
	return func(*fakeapi.Server) http.Handler { return h }
}

func TestProviderErrorFormat(t *testing.T) {
	t.Parallel()
	inner := errors.New("HTTP 429 Too Many Requests")
	err := &ProviderError{Provider: "ipwhois", Kind: KindRateLimited, Err: inner}
	if got, want := err.Error(), "ipwhois: rate_limited: HTTP 429 Too Many Requests"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, inner) {
		t.Error("errors.Is does not see the wrapped error")
	}
}

func TestProvidersDecodeFullResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		provider, fixture, path string
		want                    Location
	}{
		{
			provider: "ipwhois", fixture: "ipwhois_ok.json", path: "/ipwhois/",
			want: Location{ZipCode: "10001", City: "New York", Region: "New York", Country: "United States", CountryCode: "US", IP: "203.0.113.7", Provider: "ipwhois"},
		},
		{
			provider: "freeipapi", fixture: "freeipapi_ok.json", path: "/freeipapi/api/v1/json",
			want: Location{ZipCode: "60601", City: "Chicago", Region: "Illinois", Country: "United States", CountryCode: "US", IP: "203.0.113.7", Provider: "freeipapi"},
		},
		{
			provider: "ipapico", fixture: "ipapico_ok.json", path: "/ipapico/json/",
			want: Location{ZipCode: "98101", City: "Seattle", Region: "Washington", Country: "United States", CountryCode: "US", IP: "203.0.113.7", Provider: "ipapico"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			t.Parallel()
			srv := fakeapi.New(t)
			srv.Handle(tt.provider, fakeapi.JSON(fakeapi.Fixture(t, tt.fixture)))

			got, err := newTestProvider(t, srv, tt.provider).Locate(t.Context())
			if err != nil {
				t.Fatalf("Locate: %v", err)
			}
			if got != tt.want {
				t.Errorf("Locate = %+v\nwant     %+v", got, tt.want)
			}

			hits := srv.Hits()
			if len(hits) != 1 {
				t.Fatalf("got %d requests, want 1", len(hits))
			}
			h := hits[0]
			if h.Method != http.MethodGet || h.Path != tt.path {
				t.Errorf("request = %s %s, want GET %s", h.Method, h.Path, tt.path)
			}
			if ua := h.Header.Get("User-Agent"); ua != testUserAgent {
				t.Errorf("User-Agent = %q, want %q", ua, testUserAgent)
			}
			if accept := h.Header.Get("Accept"); accept != "application/json" {
				t.Errorf("Accept = %q, want application/json", accept)
			}
		})
	}
}

func TestProvidersDecodeBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider string
		fixture  string // read from fakeapi/testdata when set
		body     string // used when fixture is empty
		want     Location
		kind     Kind // when set, the failure expected instead of want
		msg      string
	}{
		{
			name: "ipwhois failure", provider: "ipwhois", fixture: "ipwhois_failure.json",
			kind: KindRejected, msg: "Reserved range",
		},
		{
			name: "ipwhois failure without a message", provider: "ipwhois", body: `{"success":false}`,
			kind: KindRejected, msg: "provider reported an error",
		},
		{
			name: "ipwhois without a zip code", provider: "ipwhois", fixture: "ipwhois_nozip.json",
			want: Location{City: "Paris", Region: "Île-de-France", Country: "France", CountryCode: "FR", IP: "198.51.100.23"},
		},
		{
			name: "non-ASCII names", provider: "ipwhois", fixture: "ipwhois_unicode.json",
			want: Location{ZipCode: "01310-100", City: "São Paulo", Region: "São Paulo", Country: "Brazil", CountryCode: "BR", IP: "192.0.2.44"},
		},
		{
			name: "spaces trimmed and country code upper-cased", provider: "ipwhois",
			body: `{"success":true,"country":" Brazil ","country_code":"br","city":" Recife ","postal":" 50030-230 "}`,
			want: Location{ZipCode: "50030-230", City: "Recife", Country: "Brazil", CountryCode: "BR"},
		},
		{
			name: "numeric postal code", provider: "ipwhois",
			body: `{"success":true,"country":"United States","country_code":"US","postal":60601}`,
			want: Location{ZipCode: "60601", Country: "United States", CountryCode: "US"},
		},
		{
			name: "leading zero kept", provider: "ipwhois",
			body: `{"success":true,"country":"United States","country_code":"US","postal":"02134"}`,
			want: Location{ZipCode: "02134", Country: "United States", CountryCode: "US"},
		},
		{
			name: "null postal code", provider: "ipwhois",
			body: `{"success":true,"country":"United States","country_code":"US","postal":null}`,
			want: Location{Country: "United States", CountryCode: "US"},
		},
		{
			name: "missing postal code", provider: "ipwhois",
			body: `{"success":true,"country":"United States","country_code":"US"}`,
			want: Location{Country: "United States", CountryCode: "US"},
		},
		{
			name: "postal code of the wrong type", provider: "ipwhois",
			body: `{"success":true,"country":"United States","postal":true}`,
			kind: KindBadResponse, msg: "expected a string or a number",
		},
		{
			name: "empty object", provider: "ipwhois", body: `{}`,
			kind: KindBadResponse, msg: "no location data",
		},
		{
			name: "freeipapi dash for unknown values", provider: "freeipapi", fixture: "freeipapi_nozip.json",
			want: Location{City: "Lyon", Region: "Auvergne-Rhône-Alpes", Country: "France", CountryCode: "FR", IP: "198.51.100.23"},
		},
		{
			name: "freeipapi nothing but dashes", provider: "freeipapi",
			body: `{"countryName":"-","countryCode":"-","cityName":"-","zipCode":"-"}`,
			kind: KindBadResponse, msg: "no location data",
		},
		{
			name: "ipapico rate limited with status 200", provider: "ipapico", fixture: "ipapico_ratelimited.json",
			kind: KindRateLimited, msg: "RateLimited: Visit https://ipapi.co/ratelimited/ for details",
		},
		{
			name: "ipapico error", provider: "ipapico", fixture: "ipapico_error.json",
			kind: KindRejected, msg: "Reserved IP Address",
		},
		{
			name: "ipapico null postal code", provider: "ipapico", fixture: "ipapico_nozip.json",
			want: Location{City: "Hong Kong", Region: "Central and Western", Country: "Hong Kong", CountryCode: "HK", IP: "192.0.2.80"},
		},
		{
			name: "ipapico all null", provider: "ipapico",
			body: `{"ip":"192.0.2.1","city":null,"region":null,"country":null,"country_name":null,"country_code":null,"postal":null}`,
			kind: KindBadResponse, msg: "no location data",
		},
		{
			name: "ipapico country code from the country field", provider: "ipapico",
			body: `{"country":"us","country_name":"United States","postal":"10001"}`,
			want: Location{ZipCode: "10001", Country: "United States", CountryCode: "US"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(tt.body)
			if tt.fixture != "" {
				body = fakeapi.Fixture(t, tt.fixture)
			}
			srv := fakeapi.New(t)
			srv.Handle(tt.provider, fakeapi.JSON(body))

			got, err := newTestProvider(t, srv, tt.provider).Locate(t.Context())
			if tt.kind != "" {
				wantFailure(t, err, tt.provider, tt.kind, tt.msg)
				return
			}
			if err != nil {
				t.Fatalf("Locate: %v", err)
			}
			tt.want.Provider = tt.provider
			if got != tt.want {
				t.Errorf("Locate = %+v\nwant     %+v", got, tt.want)
			}
		})
	}
}

func TestProvidersClassifyHTTPFailures(t *testing.T) {
	t.Parallel()
	oversized := `{"padding":"` + strings.Repeat("x", maxBodySize) + `"}`
	tests := []struct {
		name    string
		handler func(*fakeapi.Server) http.Handler
		kind    Kind
		msg     string
	}{
		{"rate limited", static(fakeapi.Status(http.StatusTooManyRequests)), KindRateLimited, "HTTP 429 Too Many Requests"},
		{"server error", static(fakeapi.Status(http.StatusInternalServerError)), KindUnavailable, "HTTP 500 Internal Server Error"},
		{"service unavailable", static(fakeapi.Status(http.StatusServiceUnavailable)), KindUnavailable, "HTTP 503 Service Unavailable"},
		{"CDN block page", static(fakeapi.HTML403()), KindRejected, "HTTP 403 Forbidden"},
		{"not found", static(fakeapi.Status(http.StatusNotFound)), KindRejected, "HTTP 404 Not Found"},
		{"connection dropped", static(fakeapi.Drop()), KindUnavailable, ""},
		{"malformed JSON", static(fakeapi.JSON([]byte(`{"country":`))), KindBadResponse, "invalid JSON"},
		{"empty body", static(fakeapi.JSON(nil)), KindBadResponse, "invalid JSON"},
		{"HTML with status 200", static(fakeapi.Respond(http.StatusOK, "text/html", "<html><body>Hi</body></html>")), KindBadResponse, "invalid JSON"},
		{"oversized body", static(fakeapi.JSON([]byte(oversized))), KindBadResponse, "larger than"},
		{"no answer before the deadline", (*fakeapi.Server).Hang, KindTimeout, "context deadline exceeded"},
	}
	for _, provider := range Names() {
		for _, tt := range tests {
			t.Run(provider+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				srv := fakeapi.New(t)
				srv.Handle(provider, tt.handler(srv))
				ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
				defer cancel()

				_, err := newTestProvider(t, srv, provider).Locate(ctx)
				wantFailure(t, err, provider, tt.kind, tt.msg)
			})
		}
	}
}

func TestRedirectToPlainHTTPIsRefused(t *testing.T) {
	t.Parallel()
	plain := fakeapi.New(t)
	plain.Handle("ipwhois", fakeapi.JSON(fakeapi.Fixture(t, "ipwhois_ok.json")))
	secure := httptest.NewTLSServer(http.RedirectHandler(plain.URL+"/ipwhois/", http.StatusFound))
	t.Cleanup(secure.Close)

	providers, err := New([]string{"ipwhois"}, Options{BaseURL: secure.URL, Transport: secure.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	_, err = providers[0].Locate(t.Context())
	wantFailure(t, err, "ipwhois", KindBadResponse, "refusing to follow a redirect")
	if !errors.Is(err, errInsecureRedirect) {
		t.Errorf("error = %v, want errInsecureRedirect", err)
	}
	if hits := plain.Hits(); len(hits) != 0 {
		t.Errorf("plain HTTP server got %d requests, want 0", len(hits))
	}
}
