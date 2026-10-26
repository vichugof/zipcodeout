package geo

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

// spec describes a supported provider.
type spec struct {
	name       string
	defaultURL string
	build      func(name, endpoint, userAgent string, client *http.Client) Provider
}

// specs lists the supported providers in their default order.
var specs = []spec{
	{name: "ipwhois", defaultURL: "https://ipwho.is/", build: jsonProviderFor((*ipwhoisResponse).toLocation)},
	{name: "freeipapi", defaultURL: "https://free.freeipapi.com/api/v1/json", build: jsonProviderFor((*freeipapiResponse).toLocation)},
	{name: "ipapico", defaultURL: "https://ipapi.co/json/", build: jsonProviderFor((*ipapicoResponse).toLocation)},
}

func jsonProviderFor[T any](convert func(*T) (Location, *ProviderError)) func(name, endpoint, userAgent string, client *http.Client) Provider {
	return func(name, endpoint, userAgent string, client *http.Client) Provider {
		return &jsonProvider[T]{name: name, url: endpoint, userAgent: userAgent, client: client, convert: convert}
	}
}

// Names returns the supported provider names in their default order.
func Names() []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.name
	}
	return names
}

// ParseNames parses a comma-separated list of provider names, such as
// "ipapico,ipwhois", keeping its order.
func ParseNames(list string) ([]string, error) {
	var names []string
	for name := range strings.SplitSeq(list, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		switch {
		case name == "":
			return nil, errors.New("empty provider name")
		case !slices.Contains(Names(), name):
			return nil, fmt.Errorf("unknown provider %q (choose from %s)", name, strings.Join(Names(), ", "))
		case slices.Contains(names, name):
			return nil, fmt.Errorf("provider %q is listed twice", name)
		}
		names = append(names, name)
	}
	return names, nil
}

// Options configures the providers returned by New.
type Options struct {
	// UserAgent is sent with every request.
	UserAgent string
	// BaseURL, when set, moves every provider off its real host: a
	// provider's path is served under BaseURL/<name>, so ipwhois becomes
	// BaseURL/ipwhois/. Tests use it to talk to a local fake. It must be
	// HTTPS, or plain HTTP on a loopback address.
	BaseURL string
	// Transport replaces http.DefaultTransport.
	Transport http.RoundTripper
}

// New returns the named providers, in the order given.
func New(names []string, opts Options) ([]Provider, error) {
	var base *url.URL
	if opts.BaseURL != "" {
		var err error
		if base, err = parseBaseURL(opts.BaseURL); err != nil {
			return nil, err
		}
	}
	client := newHTTPClient(opts.Transport)
	providers := make([]Provider, 0, len(names))
	for _, name := range names {
		i := slices.IndexFunc(specs, func(s spec) bool { return s.name == name })
		if i < 0 {
			return nil, fmt.Errorf("unknown provider %q", name)
		}
		endpoint := specs[i].defaultURL
		if base != nil {
			var err error
			if endpoint, err = rebase(base, name, endpoint); err != nil {
				return nil, err
			}
		}
		providers = append(providers, specs[i].build(name, endpoint, opts.UserAgent, client))
	}
	return providers, nil
}

// parseBaseURL validates the BaseURL option.
func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	switch {
	case u.Host == "":
		return nil, fmt.Errorf("invalid base URL %q: no host", raw)
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return nil, fmt.Errorf("invalid base URL %q: use https, or http on a loopback address", raw)
	}
	return u, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// rebase moves endpoint under base/<name>, keeping endpoint's path and query.
func rebase(base *url.URL, name, endpoint string) (string, error) {
	orig, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("provider %s: %w", name, err)
	}
	u := *base
	u.Path = strings.TrimSuffix(base.Path, "/") + "/" + name + orig.Path
	u.RawPath = ""
	u.RawQuery = orig.RawQuery
	u.Fragment = ""
	return u.String(), nil
}
