// Package geo estimates where the caller is (zip code, city, region and
// country) from their public IP address, using free geolocation web services.
package geo

import (
	"context"
	"fmt"
	"strings"
)

// Location is an approximate position derived from a public IP address.
type Location struct {
	ZipCode     string `json:"zip_code"`
	City        string `json:"city"`
	Region      string `json:"region"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	IP          string `json:"ip,omitempty"`
	Provider    string `json:"provider"`
}

// Provider looks up the location of the machine that sends the request.
type Provider interface {
	Name() string
	// Locate returns the caller's location; a failure is a *ProviderError.
	// A location without a zip code is not an error at this level.
	Locate(ctx context.Context) (Location, error)
}

// Kind classifies why an attempt did not produce a zip code.
type Kind string

// The kinds of attempt failure.
const (
	KindRateLimited Kind = "rate_limited" // the provider's quota is used up
	KindRejected    Kind = "rejected"     // the provider refused or reported an error
	KindUnavailable Kind = "unavailable"  // network failure or server error
	KindTimeout     Kind = "timeout"      // the attempt ran out of time
	KindBadResponse Kind = "bad_response" // the response held no usable location
	KindNoZipCode   Kind = "no_zip_code"  // a location without a zip code
)

// ProviderError reports why a provider failed.
type ProviderError struct {
	Provider string
	Kind     Kind
	Err      error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Provider, e.Kind, e.Err)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// failure returns a ProviderError without the provider name, which the
// provider fills in.
func failure(kind Kind, format string, args ...any) *ProviderError {
	return &ProviderError{Kind: kind, Err: fmt.Errorf(format, args...)}
}

// normalize trims every field of loc, treats "-" (freeipapi's marker for an
// unknown value, such as a missing zip code) as empty, and upper-cases the
// country code.
func normalize(loc Location) Location {
	for _, f := range []*string{&loc.ZipCode, &loc.City, &loc.Region, &loc.Country, &loc.CountryCode, &loc.IP} {
		*f = strings.TrimSpace(*f)
		if *f == "-" {
			*f = ""
		}
	}
	loc.CountryCode = strings.ToUpper(loc.CountryCode)
	return loc
}
