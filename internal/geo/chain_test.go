package geo

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// stub is a Provider whose behavior is set by the test.
type stub struct {
	name   string
	calls  int
	locate func(ctx context.Context) (Location, error)
}

func (s *stub) Name() string { return s.name }

func (s *stub) Locate(ctx context.Context) (Location, error) {
	s.calls++
	return s.locate(ctx)
}

func found(loc Location) func(context.Context) (Location, error) {
	return func(context.Context) (Location, error) { return loc, nil }
}

func fails(kind Kind) func(context.Context) (Location, error) {
	return func(context.Context) (Location, error) {
		return Location{}, &ProviderError{Kind: kind, Err: errors.New(string(kind))}
	}
}

// hangs waits until the attempt runs out of time, like a provider that never
// answers.
func hangs(ctx context.Context) (Location, error) {
	<-ctx.Done()
	return Location{}, &ProviderError{Kind: KindTimeout, Err: ctx.Err()}
}

var (
	newYork = Location{ZipCode: "10001", City: "New York", Region: "New York", Country: "United States", CountryCode: "US", Provider: "b"}
	paris   = Location{City: "Paris", Region: "Île-de-France", Country: "France", CountryCode: "FR", Provider: "a"}
	lyon    = Location{City: "Lyon", Country: "France", CountryCode: "FR", Provider: "b"}
)

func kinds(attempts []Attempt) []Kind {
	ks := make([]Kind, len(attempts))
	for i, a := range attempts {
		ks[i] = a.Kind
	}
	return ks
}

func TestChainFirstSuccessWins(t *testing.T) {
	t.Parallel()
	a := &stub{name: "a", locate: found(newYork)}
	b := &stub{name: "b", locate: found(paris)}

	loc, attempts, err := NewChain(a, b).Locate(t.Context())
	if err != nil || loc != newYork {
		t.Fatalf("Locate = %+v, %v; want New York", loc, err)
	}
	if b.calls != 0 {
		t.Errorf("second provider called %d times, want 0", b.calls)
	}
	if len(attempts) != 1 || attempts[0].Provider != "a" || attempts[0].Kind != "" || attempts[0].Err != nil {
		t.Errorf("attempts = %+v, want one successful attempt by a", attempts)
	}
}

func TestChainFallsThroughEveryFailure(t *testing.T) {
	t.Parallel()
	for _, kind := range []Kind{KindRateLimited, KindRejected, KindUnavailable, KindTimeout, KindBadResponse} {
		a := &stub{name: "a", locate: fails(kind)}
		b := &stub{name: "b", locate: found(newYork)}

		loc, attempts, err := NewChain(a, b).Locate(t.Context())
		if err != nil || loc != newYork {
			t.Errorf("%s: Locate = %+v, %v; want New York", kind, loc, err)
			continue
		}
		if got := kinds(attempts); !slices.Equal(got, []Kind{kind, ""}) {
			t.Errorf("%s: attempt kinds = %q", kind, got)
		}
		if attempts[0].Provider != "a" || attempts[0].Err == nil || attempts[0].Err.Error() != string(kind) {
			t.Errorf("%s: first attempt = %+v", kind, attempts[0])
		}
	}
}

func TestChainTreatsOtherErrorsAsUnavailable(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	a := &stub{name: "a", locate: func(context.Context) (Location, error) { return Location{}, boom }}
	b := &stub{name: "b", locate: found(newYork)}

	_, attempts, err := NewChain(a, b).Locate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if attempts[0].Kind != KindUnavailable || !errors.Is(attempts[0].Err, boom) {
		t.Errorf("first attempt = %+v, want unavailable: boom", attempts[0])
	}
}

func TestChainKeepsFirstPartialLocation(t *testing.T) {
	t.Parallel()
	a := &stub{name: "a", locate: found(paris)}
	b := &stub{name: "b", locate: found(lyon)}
	c := &stub{name: "c", locate: fails(KindRejected)}

	loc, attempts, err := NewChain(a, b, c).Locate(t.Context())
	if !errors.Is(err, ErrNoZipCode) {
		t.Fatalf("error = %v, want ErrNoZipCode", err)
	}
	if loc != paris {
		t.Errorf("location = %+v, want the first partial one, %+v", loc, paris)
	}
	if got, want := kinds(attempts), []Kind{KindNoZipCode, KindNoZipCode, KindRejected}; !slices.Equal(got, want) {
		t.Errorf("attempt kinds = %q, want %q", got, want)
	}
	if !errors.Is(attempts[0].Err, errMissingZip) {
		t.Errorf("first attempt error = %v, want errMissingZip", attempts[0].Err)
	}
}

func TestChainNeverMergesLocations(t *testing.T) {
	t.Parallel()
	// The second provider knows the zip code but not the region; the region
	// from the first provider's partial answer must not leak into the result.
	noRegion := Location{ZipCode: "10001", City: "New York", Country: "United States", CountryCode: "US", Provider: "b"}
	a := &stub{name: "a", locate: found(paris)}
	b := &stub{name: "b", locate: found(noRegion)}

	loc, _, err := NewChain(a, b).Locate(t.Context())
	if err != nil || loc != noRegion {
		t.Errorf("Locate = %+v, %v; want exactly %+v", loc, err, noRegion)
	}
}

func TestChainReportsLookupFailed(t *testing.T) {
	t.Parallel()
	a := &stub{name: "a", locate: fails(KindUnavailable)}
	b := &stub{name: "b", locate: fails(KindRateLimited)}

	loc, attempts, err := NewChain(a, b).Locate(t.Context())
	if !errors.Is(err, ErrLookupFailed) {
		t.Fatalf("error = %v, want ErrLookupFailed", err)
	}
	if loc != (Location{}) {
		t.Errorf("location = %+v, want none", loc)
	}
	if got, want := kinds(attempts), []Kind{KindUnavailable, KindRateLimited}; !slices.Equal(got, want) {
		t.Errorf("attempt kinds = %q, want %q", got, want)
	}
}

func TestChainSplitsTheTimeBudget(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		start := time.Now()
		var deadlines []time.Duration
		hangsRecording := func(ctx context.Context) (Location, error) {
			deadline, _ := ctx.Deadline()
			deadlines = append(deadlines, deadline.Sub(start))
			return hangs(ctx)
		}
		a := &stub{name: "a", locate: hangsRecording}
		b := &stub{name: "b", locate: hangsRecording}
		c := &stub{name: "c", locate: hangsRecording}

		_, attempts, err := NewChain(a, b, c).Locate(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want context.DeadlineExceeded", err)
		}
		if want := []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second}; !slices.Equal(deadlines, want) {
			t.Errorf("attempt deadlines = %v, want %v", deadlines, want)
		}
		for _, a := range attempts {
			if a.Kind != KindTimeout || a.Duration != 5*time.Second {
				t.Errorf("attempt = %+v, want a 5s timeout", a)
			}
		}
	})
}

func TestChainFastFailureLeavesTimeForOthers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		start := time.Now()
		var deadlines []time.Duration
		record := func(next func(context.Context) (Location, error)) func(context.Context) (Location, error) {
			return func(ctx context.Context) (Location, error) {
				deadline, _ := ctx.Deadline()
				deadlines = append(deadlines, deadline.Sub(start))
				return next(ctx)
			}
		}
		a := &stub{name: "a", locate: record(fails(KindUnavailable))}
		b := &stub{name: "b", locate: record(hangs)}
		c := &stub{name: "c", locate: record(found(newYork))}

		loc, attempts, err := NewChain(a, b, c).Locate(ctx)
		if err != nil || loc != newYork {
			t.Fatalf("Locate = %+v, %v; want New York", loc, err)
		}
		// a fails at once, so b gets half of the full 15s instead of a third.
		if want := []time.Duration{5 * time.Second, 7500 * time.Millisecond, 15 * time.Second}; !slices.Equal(deadlines, want) {
			t.Errorf("attempt deadlines = %v, want %v", deadlines, want)
		}
		if got := attempts[1].Duration; got != 7500*time.Millisecond {
			t.Errorf("second attempt took %v, want 7.5s", got)
		}
	})
}

func TestChainStopsWhenCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := &stub{name: "a", locate: func(ctx context.Context) (Location, error) {
		cancel() // as if the user pressed Ctrl+C during the first attempt
		return hangs(ctx)
	}}
	b := &stub{name: "b", locate: found(newYork)}

	_, attempts, err := NewChain(a, b).Locate(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if b.calls != 0 {
		t.Errorf("second provider called %d times after cancellation, want 0", b.calls)
	}
	if len(attempts) != 1 {
		t.Errorf("got %d attempts, want 1", len(attempts))
	}
}

func TestChainWithoutDeadline(t *testing.T) {
	t.Parallel()
	a := &stub{name: "a", locate: func(ctx context.Context) (Location, error) {
		if _, ok := ctx.Deadline(); ok {
			return Location{}, errors.New("attempt got a deadline its parent did not have")
		}
		return newYork, nil
	}}
	if _, _, err := NewChain(a).Locate(t.Context()); err != nil {
		t.Error(err)
	}
}
