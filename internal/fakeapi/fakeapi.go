// Package fakeapi is a local stand-in for the geolocation providers, so tests
// never reach the internet. Point the code under test at Server.URL (through
// geo.Options.BaseURL or the ZIPCODE_API_BASE_URL variable); each provider is
// then served under /<provider name>/.
package fakeapi

import (
	"embed"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Providers lists the provider names the server answers for. It must match
// geo.Names(); a test in the geo package checks that it does.
var Providers = []string{"ipwhois", "freeipapi", "ipapico"}

//go:embed testdata/*.json
var fixtures embed.FS

// Fixture returns the contents of testdata/<name>.
func Fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("fakeapi: fixture %s: %v", name, err)
	}
	return b
}

// Hit is one request received by a Server.
type Hit struct {
	Provider string
	Method   string
	Path     string
	Header   http.Header
	Time     time.Time
}

// Server is a fake geolocation API. A provider without a handler answers
// 503, and a request for an unknown provider fails the test.
type Server struct {
	URL string

	t    testing.TB
	done chan struct{}

	mu       sync.Mutex
	handlers map[string]http.Handler
	hits     []Hit
}

// New starts a Server on 127.0.0.1 and stops it when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{t: t, done: make(chan struct{}), handlers: make(map[string]http.Handler)}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	// One request per connection. Otherwise a client whose reused connection
	// was closed by Drop would retry the request and count as two hits.
	srv.Config.SetKeepAlivesEnabled(false)
	srv.Start()
	s.URL = srv.URL
	t.Cleanup(srv.Close)
	// Registered after srv.Close so it runs first: Close waits for running
	// handlers, and Hang handlers return once done is closed.
	t.Cleanup(func() { close(s.done) })
	return s
}

// Handle makes the server answer requests for provider with h.
func (s *Server) Handle(provider string, h http.Handler) {
	s.t.Helper()
	if !slices.Contains(Providers, provider) {
		s.t.Fatalf("fakeapi: unknown provider %q", provider)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[provider] = h
}

// Hits returns the requests received so far, in arrival order.
func (s *Server) Hits() []Hit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.hits)
}

// Order returns the provider of each request received so far, in arrival order.
func (s *Server) Order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	order := make([]string, len(s.hits))
	for i, h := range s.hits {
		order[i] = h.Provider
	}
	return order
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	provider, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	s.mu.Lock()
	s.hits = append(s.hits, Hit{
		Provider: provider,
		Method:   r.Method,
		Path:     r.URL.Path,
		Header:   r.Header.Clone(),
		Time:     time.Now(),
	})
	h, ok := s.handlers[provider]
	s.mu.Unlock()

	switch {
	case ok:
		h.ServeHTTP(w, r)
	case slices.Contains(Providers, provider):
		http.Error(w, "fakeapi: no handler for "+provider, http.StatusServiceUnavailable)
	default:
		s.t.Errorf("fakeapi: request for unknown path %s", r.URL.Path)
		http.NotFound(w, r)
	}
}

// Hang returns a handler that never answers. It returns when the client
// gives up or when the test ends.
func (s *Server) Hang() http.Handler {
	return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-s.done:
		}
	})
}

// Respond returns a handler that answers with status, content type and body.
func Respond(status int, contentType, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// JSON returns a handler that answers 200 with body as application/json.
func JSON(body []byte) http.Handler {
	return Respond(http.StatusOK, "application/json", string(body))
}

// Status returns a handler that answers with code and its standard text.
func Status(code int) http.Handler {
	return Respond(code, "text/plain; charset=utf-8", http.StatusText(code))
}

// HTML403 returns a handler that answers like a CDN blocking the request.
func HTML403() http.Handler {
	return Respond(http.StatusForbidden, "text/html; charset=utf-8",
		"<html><head><title>403 Forbidden</title></head><body><h1>Access denied</h1></body></html>")
}

// Drop returns a handler that closes the connection without answering. Unlike
// a closed port, which takes about two seconds to refuse a connection on
// Windows, it fails fast everywhere.
func Drop() http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// The server closes the connection without writing a response.
		panic(http.ErrAbortHandler)
	})
}
