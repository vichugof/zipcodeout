# Plan: `zipcode`, a cross-platform Go CLI that prints your zip code, city and country

## Context
The "Zip code tool" requirement doc asks for a shell tool, built the way an expert Go engineer would build it. It prints the
user's **zip code** for their current position, plus the **city and country**. It must run on **Windows and Linux** and
have **unit and end-to-end tests**. `/home/vichugof/public_html/sudo/ai_training/zipcodeout` is empty: a new project, not
yet a git repo. Available tools: Go 1.26.0, gcc, golangci-lint 2.11.1, make, and gh (logged in as `vichugof`). There is
no Wine, so Windows is checked by vetting the Windows build locally and running the tests on GitHub Actions `windows-latest`.

Decisions confirmed with you:
- **Position comes from the public IP address.** Three free HTTPS providers that need no API key are tried in order:
  `ipwho.is`, then `freeipapi.com`, then `ipapi.co`.
  - Rejected: ip-api.com (free tier is HTTP-only and non-commercial) and ipinfo.io (free tier is country-only).
  - Results are approximate because they reflect the ISP's or VPN's exit point. The `-h` text and the README say so, and
    say that the user's IP is sent to these providers.
- **Windows verification:** run `GOOS=windows go vet` and `go build` locally, and run every test in CI on both
  `ubuntu-latest` and `windows-latest`.
- **Standard library only**, no third-party dependencies. Release builds use `CGO_ENABLED=0 -trimpath`, which produces
  static binaries.

## Layout (module `github.com/vichugof/zipcodeout`)
```
cmd/zipcode/main.go          signal.NotifyContext(os.Interrupt, syscall.SIGTERM) → cli.Run → os.Exit; var version (ldflags)
cmd/zipcode/e2e_test.go      e2e tests of the compiled binary (package main, so the go test cache resets when any source changes)
cmd/zipcode/live_test.go     real-network smoke test; skipped unless ZIPCODE_LIVE_TEST=1
internal/geo/
  geo.go                     Location, Provider, ProviderError + Kind
  http.go                    jsonProvider: shared request / status / decode / normalize logic
  ipwhois.go freeipapi.go ipapico.go   one response struct + toLocation each (~25 lines)
  registry.go                names, default order, default URLs, ParseNames, base-URL override
  chain.go                   tries providers in order within one time budget, records each attempt
internal/cli/                cli.go (flags, wiring, exit codes), output.go (text and JSON)
internal/fakeapi/            used only by tests: httptest server routed by provider, handler helpers, embedded JSON fixtures
.gitattributes (* text=auto eol=lf)  .gitignore  .golangci.yml  Makefile  .github/workflows/ci.yml  README.md
```

## Core design (`internal/geo`)
```go
type Location struct {
    ZipCode     string `json:"zip_code"`      // "" when unknown, never null
    City        string `json:"city"`
    Region      string `json:"region"`
    Country     string `json:"country"`
    CountryCode string `json:"country_code"`
    IP          string `json:"ip,omitempty"`
    Provider    string `json:"provider"`
}
type Provider interface {
    Name() string
    Locate(ctx context.Context) (Location, error) // errors are *ProviderError{Provider, Kind, Err}
}
type Kind string // rate_limited | rejected | unavailable | timeout | bad_response | no_zip_code
```
- **jsonProvider** is generic over each provider's response type and holds all the HTTP code in one place:
  - Builds a GET with `NewRequestWithContext`, `User-Agent: zipcode/<version>` and `Accept: application/json`.
  - Reads at most 64 KiB of body.
  - Uses an `http.Client` on the default transport, so `HTTPS_PROXY` is honored. Its `CheckRedirect` refuses HTTPS→HTTP
    downgrades. There is no `Client.Timeout`; the context controls timing.
  - Runs the provider's `toLocation`, then `normalize`: trims spaces, turns `"-"` into "", upper-cases the country code.
  - A response with no country at all is reported as `bad_response`.
- **Error classification** (no sleeping or retrying; on any error the chain moves to the next provider):

  | Kind | Triggered by |
  |---|---|
  | `rate_limited` | HTTP 429, or ipapi.co `reason:"RateLimited"` |
  | `rejected` | HTTP 200 with `success:false` (ipwho.is) or `error:true` (ipapi.co); 403, including CDN HTML pages; other 4xx |
  | `unavailable` | 5xx; DNS failure, connection reset, EOF |
  | `timeout` | the attempt's deadline expired |
  | `bad_response` | malformed, empty or oversized body; HTML on a 200; `{}` |

- **Field mapping:**

  | name | default URL | zip · city · region · country · code |
  |---|---|---|
  | `ipwhois` | `https://ipwho.is/` | `postal` `city` `region` `country` `country_code` |
  | `freeipapi` | `https://free.freeipapi.com/api/v1/json` | `zipCode` `cityName` `regionName` `countryName` `countryCode` |
  | `ipapico` | `https://ipapi.co/json/` | `postal` `city` `region` `country_name` `country_code` (if missing, `country`, which is the 2-letter code) |

  Postal fields decode through a small `flexString` type that accepts a string, number or null. A provider changing the
  field's type then doesn't break parsing.
- **Chain**: `Locate(ctx) (Location, []Attempt, error)`.
  - Each attempt's deadline is the remaining overall budget divided by the number of providers left. A provider that fails
    fast leaves its unused time to the later ones.
  - The first location that has a zip wins.
  - A location without a zip is recorded as `no_zip_code`. The first such partial result is kept and the chain moves on.
    Fields from different providers are never mixed.
  - If the parent context is canceled, the chain stops immediately.
  - It ends with the location, `ErrNoZipCode` (plus the partial result), or `ErrLookupFailed`.
  - Every `Attempt{Provider, Kind, Err, Duration}` feeds `-verbose` and the JSON error document.
- **Test switch**: the `ZIPCODE_API_BASE_URL` env var, read through an injected `getenv`, moves *every* provider under one
  base URL: `<base>/ipwhois/`, `<base>/freeipapi/api/v1/json`, `<base>/ipapico/json/`.
  - It is all-or-nothing, so a test can never reach the internet by accident.
  - Plain `http://` is accepted only for loopback hosts.
  - The binary that is tested is the binary that ships; there are no build tags.

## CLI (`internal/cli`, stdlib `flag`)
Entry point: `cli.Run(ctx, args, cli.Env{Stdout, Stderr, Getenv, Version}) int`
```
zipcode [-json] [-provider ipwhois,freeipapi,ipapico] [-timeout 15s] [-verbose] [-version]
```
- **Flags:**
  - `-provider`: comma-separated list of which providers to use, in order. Unknown, duplicate or empty names exit 2.
  - `-timeout`: total time budget for the lookup; must be greater than 0.
  - `-verbose`: one stderr line per attempt (provider, kind, duration).
  - `-version`: prints `zipcode v1.2.3 (go1.26.0 windows/amd64)`. The version comes from `-X main.version` if set,
    otherwise `debug.ReadBuildInfo`, otherwise `(devel)`.
  - `-h`: usage on **stdout**, exit 0, including the accuracy and privacy note. The usage text names `zipcode` directly
    rather than `os.Args[0]`, which on Windows is a full `C:\…\zipcode.exe` path.
  - Positional arguments exit 2.
- **Text output:** LF line endings only, printed only after the lookup finishes; missing values show `unknown`.
  ```
  Zip code: 10001
  City:     New York
  Region:   New York
  Country:  United States (US)
  ```
- **`-json` output:** always exactly one compact JSON object on stdout, written with `SetEscapeHTML(false)`.
  - On success or a partial result, it is the `Location`.
  - On lookup failure, it is `{"error":"…","attempts":[{"provider","kind","message"}]}`, so `jq` and `ConvertFrom-Json`
    never receive empty input.
  - A human-readable summary still goes to stderr.
- **Exit codes:**
  - **0**: zip found.
  - **1**: lookup failed (all providers failed, or the time budget ran out).
  - **2**: usage or configuration error, including a bad `ZIPCODE_API_BASE_URL`.
  - **3**: location found but no zip. The partial result is printed, with a warning on stderr.
  - **130**: interrupted. `context.AfterFunc(ctx, stop)` restores default signal handling, so a second Ctrl+C exits
    immediately.

## Tests
All tests are offline, table-driven and use `t.Parallel()`. None uses `t.Setenv`; environment values come through the
injected `getenv`.

**`internal/fakeapi`** (shared by every test layer):
- An `httptest` server on 127.0.0.1, routed by the first path segment (for example `/ipwhois/…`).
- Records each hit: order, time, headers.
- An unknown path calls `t.Errorf`; a provider the test didn't set up returns 503.
- Handler helpers:
  - `JSON(fixture)`, `Status(code)`, `HTML403()`.
  - `Drop()`: takes over the connection and closes it. It fails fast on both OSes, unlike a closed port, which takes about
    2 s per connection attempt on Windows.
  - `Hang()`: waits on the request context or on a `done` channel. A cleanup registered *after* `srv.Close` closes that
    channel, so it runs before the server shuts down.
- JSON fixtures are embedded with `//go:embed testdata/*.json`.

**Unit: providers** (each provider):
- Full success. For ipapico, `country` is the code and `country_name` is the name.
- Postal empty, null or missing → empty zip, no error.
- `{}` or all-null → `bad_response`.
- Failure inside a 200 → `rejected`, keeping the provider's message.
- 429 and ipapico `RateLimited` → `rate_limited`.
- 403 HTML or 404 → `rejected`.
- 5xx or `Drop` → `unavailable`.
- `Hang` with a short context → `timeout`.
- Malformed, empty, oversized or HTML-on-200 body → `bad_response`.
- Request shape: GET, exact path, User-Agent, Accept.
- Numeric postal and leading-zero postal.
- Non-ASCII city.
- HTTPS→HTTP redirect refused.

**Unit: registry:**
- Default order.
- `ParseNames`: ordering; unknown, duplicate and empty names.
- The base-URL rewrite moves every provider.
- Base-URL validation: https accepted, http to loopback accepted, http to a remote host or garbage rejected.

**Unit: chain** (fake providers, plus `testing/synctest` for a virtual clock):
- A success stops the chain.
- Each kind moves on to the next provider, and attempts are recorded in order.
- The first partial result is kept and fields are never mixed.
- With a 15 s budget and every provider hanging, the deadlines land at 5, 10 and 15 s. A fast failure gives its time to
  later providers.
- A parent cancel stops at once, without calling the remaining providers.

**Unit: cli** (`Run` against fakeapi):
- Flag validation → 2.
- `-h` → stdout, exit 0.
- Expected text and JSON output: full, unknown region, Unicode, `&` not escaped.
- The JSON error document.
- Exit codes 0, 1, 2, 3 and 130 (130 by passing an already-canceled context).
- Different messages for timeout and interrupt.
- Version precedence.

**E2E: the compiled binary** (`cmd/zipcode/e2e_test.go`, run by plain `go test ./...`, a few seconds):
- `TestMain` builds the binary once with `CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=e2e" -o <tmp>/zipcode[.exe] .`
  - It adds `.exe` when `runtime.GOOS == "windows"`.
  - It runs `--version` once as a warm-up, which absorbs Windows Defender's first-scan delay.
  - It deletes the temp dir afterwards.
- The `runCLI` helper:
  - Uses `exec.CommandContext(t.Context(), …)` with `WaitDelay`.
  - Removes inherited `ZIPCODE_*` and proxy variables, matching names case-insensitively.
  - Sets `ZIPCODE_API_BASE_URL`, plus `HTTP(S)_PROXY=http://127.0.0.1:9`. Go skips the proxy for loopback, so the fakes
    still work, and any accidental real request fails.
  - Returns stdout, stderr and the exit code.
- Timing checks are upper bounds with some slack.

E2E scenarios:
1. `--version` prints `zipcode e2e (…)` with zero hits. `-h` exits 0. A bad flag, `--provider nope` or a positional
   argument exits 2 with zero hits.
2. Text happy path, byte-exact: only ipwhois is hit, with User-Agent `zipcode/e2e`.
3. `--json` happy path: one compact object with `provider:"ipwhois"`.
4. ipwhois returns 429, freeipapi returns malformed JSON, ipapico succeeds: exit 0, hit order checked, and `-verbose`
   shows both reasons.
5. ipwhois returns `success:false` in a 200: falls through to the next provider.
6. ipwhois has no postal: the output comes entirely from freeipapi, with no mixed fields.
7. No zip from anyone: exit 3, `Zip code: unknown` plus the first partial result; `--json` has `"zip_code":""`.
8. 503, `Drop` and 403 HTML: exit 1, stderr names all three, and `--json` prints the error document with the kinds.
9. ipwhois hangs with `--timeout 3s`: freeipapi is hit about 1 s later (measured on the server clock), exit 0, well under
   3 s.
10. All providers hang with `--timeout 1500ms`: exit 1 with "timed out", within the bound.
11. `--provider freeipapi` hits only freeipapi; `--provider ipapico,ipwhois` keeps that order.
12. "São Paulo" with zip "01310-100" comes back byte-exact, in text and JSON.
13. SIGINT while a provider hangs: exit 130. Skipped on Windows, where `Process.Signal(os.Interrupt)` isn't supported;
    the cli cancel unit test covers that case.

**Live smoke test** (`ZIPCODE_LIVE_TEST=1` / `make test-live`; also a weekly CI cron and manual dispatch):
- Runs `--provider X --json` for each provider.
- Fails only on `bad_response`, which means a provider changed its API.
- Skips on rate limit, outage, timeout or no zip.
- Never part of `make test`, because it sends your IP to the providers.

## Windows details
- `.gitattributes` with `eol=lf` is committed before any expected-output data, since Actions checks files out with CRLF
  by default. The tool writes `\n` only.
- Fakes bind 127.0.0.1, so there's no firewall prompt. Tests use `srv.URL`, never `localhost`.
- Child processes are always `Wait()`ed, because a running exe locks its temp dir.
- Signal tests use `t.Skip`, not a file-name suffix: `_unix_test.go` does not exclude a file on Windows.
- The README notes three things for Windows users:
  - Go ignores the Windows system proxy; set `HTTPS_PROXY` instead.
  - Go uses the Windows certificate store, so corporate root certificates work.
  - Windows PowerShell 5.1 needs `[Console]::OutputEncoding=[Text.Encoding]::UTF8` to capture non-ASCII output correctly.

## Build, lint, CI
- **go.mod**: `go mod init github.com/vichugof/zipcodeout`, which writes `go 1.25.0` on Go 1.26.
  - Avoid Go 1.26-only APIs such as `errors.AsType` and `new(expr)`.
  - `synctest`, `t.Context` and `WaitDelay` are fine.
- **Makefile** (for Linux development; CI calls `go` directly):
  - `VERSION ?= $(git describe --tags --always --dirty || echo dev)`.
  - `build` → `bin/zipcode`.
  - `cross` → `dist/zipcode-{linux,windows}-{amd64,arm64}[.exe]`, built with
    `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X main.version=$(VERSION)"`.
  - `test` (`-count=1 ./...`), `test-unit` (`-race ./internal/...`), `test-e2e` (`./cmd/...`), `test-live`.
  - `cover`; `vet` (native and `GOOS=windows`); `lint`; `fmt-check` (`golangci-lint fmt --diff`).
  - `check` runs all of the above; `clean`.
  - The README lists the plain `go` commands for Windows users without make.
- **.golangci.yml** (v2 format): the default linters plus bodyclose, noctx, errorlint, usetesting, gosec (off for tests),
  misspell and nilerr. Formatters: gofumpt and goimports.
- **ci.yml**:
  - Triggers: push, PR, `workflow_dispatch` and a weekly `schedule`.
  - Settings: `permissions: contents: read`; superseded runs are canceled; `shell: bash`.
  - Actions: `actions/checkout@v7` and `actions/setup-go@v7` with `go-version: 1.26.x`.
  - Jobs:
    - `lint` (ubuntu): format check, vet (native and Windows), `golangci/golangci-lint-action@v9` pinned to `v2.11.1`.
    - `test` (ubuntu and windows, fail-fast off): `go test -count=1 ./...`, adding `-race` on ubuntu. Race on Windows
      needs cgo and MinGW and has been flaky before, so it is skipped.
    - `cross` (ubuntu): `make cross`, then `actions/upload-artifact@v7`.
    - `live` (schedule and manual dispatch only, both OSes): `ZIPCODE_LIVE_TEST=1 go test -run Live ./cmd/zipcode`.
- `git init` locally. No commit, push or repo creation unless you ask.

## Implementation order
1. `go mod init`, `git init`, `.gitattributes`, `.gitignore`, `.golangci.yml`.
2. `internal/fakeapi`; `internal/geo` types, jsonProvider, the 3 providers and the registry, with their tests.
3. The chain, with synctest tests.
4. `internal/cli` with its tests; `cmd/zipcode/main.go`.
5. E2E and live tests, Makefile, CI workflow, README.

## Risks / follow-ups
- **Free-tier quotas are per public IP.** Office networks and CI runners share them. The fallback chain absorbs this; a
  short-lived cache under `os.UserCacheDir` is a possible v2.
- **Free APIs change shape** (ipinfo already did). The weekly live job acts as the contract test, and replacing a provider
  means changing one file.
- **freeipapi's missing-value marker is undocumented.** The `"-"` → "" handling is a guess, to confirm on the first live
  run.

## Verification
1. `make check`: fmt-check, `go vet` for Linux and Windows, golangci-lint, and all tests pass.
2. `make test-unit` passes with the race detector; `make cover` aims for at least 85% on `internal/geo` and `internal/cli`.
3. `make test-e2e` passes. Edit a provider file and run it again to confirm the result isn't a stale `(cached)` pass.
4. `make cross`, then `file dist/*`: ELF for the Linux builds, PE32+ for the `.exe` files.
5. Manual smoke test (real network, so it sends your IP to the providers): `./bin/zipcode`,
   `./bin/zipcode -json -verbose`, and `make test-live`.
6. After you push to GitHub: the `ci` workflow passes on `ubuntu-latest` and `windows-latest`.
