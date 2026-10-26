package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vichugof/zipcodeout/internal/geo"
)

// writeLocation prints loc as text, or as one JSON object when asJSON is set.
func writeLocation(w io.Writer, loc geo.Location, asJSON bool) {
	if asJSON {
		writeJSON(w, loc)
		return
	}
	fmt.Fprintf(w, "Zip code: %s\nCity:     %s\nRegion:   %s\nCountry:  %s\n",
		orUnknown(loc.ZipCode), orUnknown(loc.City), orUnknown(loc.Region), orUnknown(countryLabel(loc)))
}

// countryLabel formats the country as "United States (US)", or whichever of
// the name and code is known.
func countryLabel(loc geo.Location) string {
	if loc.Country != "" && loc.CountryCode != "" {
		return loc.Country + " (" + loc.CountryCode + ")"
	}
	return loc.Country + loc.CountryCode
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// place describes loc for a message, such as "Paris, France".
func place(loc geo.Location) string {
	var parts []string
	for _, s := range []string{loc.City, countryLabel(loc)} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "your location"
	}
	return strings.Join(parts, ", ")
}

type jsonError struct {
	Error    string        `json:"error"`
	Attempts []jsonAttempt `json:"attempts"`
}

type jsonAttempt struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
}

// writeJSONError prints the failure as one JSON object, so a script reading
// stdout always receives a document.
func writeJSONError(w io.Writer, msg string, attempts []geo.Attempt) {
	doc := jsonError{Error: msg, Attempts: make([]jsonAttempt, 0, len(attempts))}
	for _, a := range attempts {
		ja := jsonAttempt{Provider: a.Provider, Kind: string(a.Kind)}
		if a.Err != nil {
			ja.Message = a.Err.Error()
		}
		doc.Attempts = append(doc.Attempts, ja)
	}
	writeJSON(w, doc)
}

// writeJSON prints v compactly on one line, leaving characters such as & and
// < unescaped.
func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // v is always encodable; write errors have nowhere to go
}

// writeAttempts prints one line per attempt, such as
// "ipwhois: rate_limited (12ms): HTTP 429 Too Many Requests".
func writeAttempts(w io.Writer, prefix string, attempts []geo.Attempt) {
	for _, a := range attempts {
		outcome := "ok"
		if a.Kind != "" {
			outcome = string(a.Kind)
		}
		line := fmt.Sprintf("%s%s: %s (%s)", prefix, a.Provider, outcome, a.Duration.Round(time.Millisecond))
		if a.Err != nil {
			line += ": " + a.Err.Error()
		}
		fmt.Fprintln(w, line)
	}
}
