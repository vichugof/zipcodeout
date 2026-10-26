package geo

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNoZipCode means providers located the caller but none of them knew
	// the zip code. Chain.Locate returns it with the first location found.
	ErrNoZipCode = errors.New("no zip code found for this location")
	// ErrLookupFailed means no provider returned a usable location.
	ErrLookupFailed = errors.New("could not determine the location")
)

// errMissingZip is recorded for an attempt that found a location without a
// zip code.
var errMissingZip = errors.New("response has no zip code")

// Attempt records the outcome of asking one provider.
type Attempt struct {
	Provider string
	Kind     Kind  // empty when the provider returned a zip code
	Err      error // nil when the provider returned a zip code
	Duration time.Duration
}

// Chain asks providers in order until one returns a location with a zip code.
type Chain struct {
	providers []Provider
}

// NewChain returns a Chain that tries providers in the order given.
func NewChain(providers ...Provider) *Chain {
	return &Chain{providers: providers}
}

// Locate returns the first location that has a zip code, along with a record
// of every attempt. If ctx has a deadline, each attempt gets an equal share
// of the time left, so a provider that fails fast leaves more time for the
// ones after it.
//
// If no provider knows the zip code, Locate returns the first location found
// and ErrNoZipCode; it never mixes fields from different providers. If no
// provider found a location, it returns ctx's error when ctx ended first, and
// ErrLookupFailed otherwise.
func (c *Chain) Locate(ctx context.Context) (Location, []Attempt, error) {
	var (
		attempts []Attempt
		partial  *Location
	)
	for i, p := range c.providers {
		if ctx.Err() != nil {
			break
		}
		start := time.Now()
		loc, err := attempt(ctx, p, len(c.providers)-i)
		a := Attempt{Provider: p.Name(), Duration: time.Since(start)}
		var pe *ProviderError
		switch {
		case errors.As(err, &pe):
			a.Kind, a.Err = pe.Kind, pe.Err
		case err != nil:
			a.Kind, a.Err = KindUnavailable, err
		case loc.ZipCode == "":
			a.Kind, a.Err = KindNoZipCode, errMissingZip
			if partial == nil {
				partial = &loc
			}
		}
		attempts = append(attempts, a)
		if a.Err == nil {
			return loc, attempts, nil
		}
	}
	switch {
	case partial != nil:
		return *partial, attempts, ErrNoZipCode
	case ctx.Err() != nil:
		return Location{}, attempts, ctx.Err()
	default:
		return Location{}, attempts, ErrLookupFailed
	}
}

// attempt asks p for the location. If ctx has a deadline, p gets an equal
// share of the time left among the remaining attempts, this one included.
func attempt(ctx context.Context, p Provider, remaining int) (Location, error) {
	// The last attempt gets all the time left. Ending it with ctx's own
	// deadline, rather than a timer of its own that fires at the same
	// instant, ensures that ctx reports the timeout once it returns.
	if deadline, ok := ctx.Deadline(); ok && remaining > 1 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Until(deadline)/time.Duration(remaining))
		defer cancel()
	}
	return p.Locate(ctx)
}
