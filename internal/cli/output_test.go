package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vichugof/zipcodeout/internal/geo"
)

func TestWriteLocationText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		loc  geo.Location
		want string
	}{
		{
			name: "full",
			loc:  geo.Location{ZipCode: "10001", City: "New York", Region: "New York", Country: "United States", CountryCode: "US"},
			want: "Zip code: 10001\nCity:     New York\nRegion:   New York\nCountry:  United States (US)\n",
		},
		{
			name: "unknown fields",
			loc:  geo.Location{ZipCode: "75001", Country: "France", CountryCode: "FR"},
			want: "Zip code: 75001\nCity:     unknown\nRegion:   unknown\nCountry:  France (FR)\n",
		},
		{
			name: "country code only",
			loc:  geo.Location{ZipCode: "10115", City: "Berlin", CountryCode: "DE"},
			want: "Zip code: 10115\nCity:     Berlin\nRegion:   unknown\nCountry:  DE\n",
		},
		{
			name: "country name only",
			loc:  geo.Location{City: "Lyon", Country: "France"},
			want: "Zip code: unknown\nCity:     Lyon\nRegion:   unknown\nCountry:  France\n",
		},
		{
			name: "non-ASCII",
			loc:  geo.Location{ZipCode: "01310-100", City: "São Paulo", Region: "São Paulo", Country: "Brazil", CountryCode: "BR"},
			want: "Zip code: 01310-100\nCity:     São Paulo\nRegion:   São Paulo\nCountry:  Brazil (BR)\n",
		},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		writeLocation(&buf, tt.loc, false)
		if got := buf.String(); got != tt.want {
			t.Errorf("%s:\ngot  %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestWriteLocationJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writeLocation(&buf, geo.Location{City: "Town & Country", Region: "<North>", Country: "United States", CountryCode: "US", Provider: "ipwhois"}, true)
	want := `{"zip_code":"","city":"Town & Country","region":"<North>","country":"United States","country_code":"US","provider":"ipwhois"}` + "\n"
	if got := buf.String(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestPlace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		loc  geo.Location
		want string
	}{
		{geo.Location{City: "Paris", Country: "France", CountryCode: "FR"}, "Paris, France (FR)"},
		{geo.Location{Country: "Hong Kong"}, "Hong Kong"},
		{geo.Location{City: "Lyon"}, "Lyon"},
		{geo.Location{}, "your location"},
	}
	for _, tt := range tests {
		if got := place(tt.loc); got != tt.want {
			t.Errorf("place(%+v) = %q, want %q", tt.loc, got, tt.want)
		}
	}
}

func TestWriteAttempts(t *testing.T) {
	t.Parallel()
	attempts := []geo.Attempt{
		{Provider: "ipwhois", Kind: geo.KindRateLimited, Err: errors.New("HTTP 429 Too Many Requests"), Duration: 12400 * time.Microsecond},
		{Provider: "freeipapi", Duration: 3 * time.Millisecond},
	}
	var buf bytes.Buffer
	writeAttempts(&buf, "  ", attempts)
	want := "  ipwhois: rate_limited (12ms): HTTP 429 Too Many Requests\n  freeipapi: ok (3ms)\n"
	if got := buf.String(); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestWriteJSONErrorIsOneLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writeJSONError(&buf, "could not determine your location", []geo.Attempt{
		{Provider: "ipwhois", Kind: geo.KindUnavailable, Err: errors.New("HTTP 503 Service Unavailable")},
	})
	want := `{"error":"could not determine your location","attempts":[{"provider":"ipwhois","kind":"unavailable","message":"HTTP 503 Service Unavailable"}]}` + "\n"
	if got := buf.String(); got != want || strings.Count(got, "\n") != 1 {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
