package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

// maxBodySize caps how much of a response is read; real ones are about 1 KiB.
const maxBodySize = 64 << 10

var errInsecureRedirect = errors.New("refusing to follow a redirect from HTTPS to plain HTTP")

// jsonProvider is a Provider for a web service that answers a GET with the
// caller's location as a JSON document of type T.
type jsonProvider[T any] struct {
	name      string
	url       string
	userAgent string
	client    *http.Client
	// convert maps the decoded document to a Location, or reports the
	// failure the document describes.
	convert func(*T) (Location, *ProviderError)
}

func (p *jsonProvider[T]) Name() string { return p.name }

func (p *jsonProvider[T]) Locate(ctx context.Context) (Location, error) {
	loc, perr := p.locate(ctx)
	if perr != nil {
		perr.Provider = p.name
		return Location{}, perr
	}
	loc.Provider = p.name
	return loc, nil
}

func (p *jsonProvider[T]) locate(ctx context.Context) (Location, *ProviderError) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return Location{}, failure(KindUnavailable, "building request: %w", err)
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return Location{}, transportFailure(err)
	}
	defer resp.Body.Close()

	if perr := statusFailure(resp); perr != nil {
		return Location{}, perr
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return Location{}, transportFailure(err)
	}
	if len(body) > maxBodySize {
		return Location{}, failure(KindBadResponse, "response is larger than %d bytes", maxBodySize)
	}

	var doc T
	if err := json.Unmarshal(body, &doc); err != nil {
		return Location{}, failure(KindBadResponse, "invalid JSON: %w", err)
	}
	loc, perr := p.convert(&doc)
	if perr != nil {
		return Location{}, perr
	}
	loc = normalize(loc)
	if loc.Country == "" && loc.CountryCode == "" {
		return Location{}, failure(KindBadResponse, "response has no location data")
	}
	return loc, nil
}

// statusFailure classifies a response that is not a 2xx; it returns nil for
// one that is.
func statusFailure(resp *http.Response) *ProviderError {
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusTooManyRequests:
		return failure(KindRateLimited, "HTTP %s", resp.Status)
	case code >= 500:
		return failure(KindUnavailable, "HTTP %s", resp.Status)
	default:
		return failure(KindRejected, "HTTP %s", resp.Status)
	}
}

// transportFailure classifies an error from sending a request or reading its
// response.
func transportFailure(err error) *ProviderError {
	var netErr net.Error
	switch {
	case errors.Is(err, errInsecureRedirect):
		return failure(KindBadResponse, "%w", err)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return failure(KindTimeout, "%w", err)
	default:
		return failure(KindUnavailable, "%w", err)
	}
}

// newHTTPClient returns a client that never downgrades from HTTPS to HTTP on
// a redirect. A nil transport means http.DefaultTransport, which honors the
// HTTPS_PROXY and NO_PROXY variables. The client has no timeout of its own:
// requests are bounded by their context.
func newHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errInsecureRedirect
			}
			return nil
		},
	}
}

// flexString decodes a JSON string, number or null into a string. Providers
// are not consistent about the JSON type of postal codes.
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("expected a string or a number, got %s", data)
	}
	*f = flexString(n.String())
	return nil
}
