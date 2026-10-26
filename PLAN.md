# Plan: `zipcode`, a cross-platform Go CLI that prints your zip code, city and country

> **Status (2026-10-26): built and released.** The latest release is
> [v0.1.1](https://github.com/vichugof/zipcodeout/releases/tag/v0.1.1), and the repository is public under the MIT
> license. This document began as the implementation plan and now records what was actually built.
> [Changes from the original plan](#changes-from-the-original-plan) lists where the two differ.

## Context
The "Zip code tool" requirement doc asked for a shell tool, built the way an expert Go engineer would build it. It prints
the user's **zip code** for their current position, plus the **city and country**. It must run on **Windows and Linux**
and have **unit and end-to-end tests**.

The project started from an empty directory. Development used Go 1.26.0, gcc, golangci-lint 2.11.1, make and gh on
Linux. No Windows machine was available, so Windows is verified by GitHub Actions on `windows-latest`.

Decisions made with the user:
- **Position comes from the public IP address.** Three free HTTPS providers that need no API key are tried in order:
  `ipwho.is`, then `freeipapi.com`, then `ipapi.co`.
  - Rejected: ip-api.com (free tier is HTTP-only and non-commercial) and ipinfo.io (free tier is country-only).
  - Results are approximate because they reflect the ISP's or VPN's exit point. The `-h` text and the README say so,
    and say that the user's IP is sent to these providers.
- **Windows verification:** `GOOS=windows go vet` locally, and every test in CI on `ubuntu-latest` and
  `windows-latest`.
- **Standard library only**, no third-party dependencies. Release builds use `CGO_ENABLED=0 -trimpath`, which produces
  static binaries.
- **Releases** follow semantic versioning, starting at `v0.1.0`; the 0.x versions leave room to change flags and output.
- **Repository:** `github.com/vichugof/zipcodeout`. It was private until after the first release, then made public with
  the MIT license.

## Layout (module `github.com/vichugof/zipcodeout`)
```
cmd/zipcode/main.go          signal.NotifyContext(os.Interrupt, syscall.SIGTERM) → cli.Run → os.Exit; var version (ldflags)
cmd/zipcode/e2e_test.go      e2e tests of the compiled binary (package main, so the go test cache resets when the code changes)
cmd/zipcode/live_test.go     real-network test; skipped unless ZIPCODE_LIVE_TEST=1
internal/geo/
  geo.go                     Location, Provider, ProviderError + Kind, normalize
  http.go                    jsonProvider (shared request / status / decode logic), flexString, the HTTP client
  ipwhois.go freeipapi.go ipapico.go   one response struct + toLocation each (~25 lines)
  registry.go                names, default order, default URLs, ParseNames, base-URL override
  chain.go                   tries providers in order within one time budget, records each attempt
internal/cli/                cli.go (flags, wiring, exit codes), output.go (text and JSON)
internal/fakeapi/            used only by tests: httptest server routed by provider, handler helpers, embedded JSON fixtures
.github/workflows/ci.yml       lint, tests on Ubuntu and Windows, cross builds; for branch pushes and pull requests
.github/workflows/release.yml  tests on both systems, then publishes a GitHub release; for v* tags
.gitattributes  .gitignore  .golangci.yml  Makefile  README.md  RELEASING.md  LICENSE  PLAN.md
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
  - Uses an `http.Client` on the default transport, so `HTTPS_PROXY` is honored. Its `CheckRedirect` refuses
    HTTPS→HTTP downgrades. There is no `Client.Timeout`; the context controls timing.
  - Runs the provider's `toLocation`, then `normalize`: trims spaces, turns `"-"` into "", upper-cases the country code.
  - A response with no country at all is reported as `bad_response`.
- **Error classification** (no sleeping or retrying; on any error the chain moves to the next provider):

  | Kind | Triggered by |
  |---|---|
  | `rate_limited` | HTTP 429, or ipapi.co `reason:"RateLimited"` |
  | `rejected` | HTTP 200 with `success:false` (ipwho.is) or `error:true` (ipapi.co); 403, including CDN HTML pages; other 4xx |
  | `unavailable` | 5xx; DNS failure, connection reset, EOF |
  | `timeout` | the attempt's deadline expired |
  | `bad_response` | malformed, empty or oversized body; HTML on a 200; `{}`; a refused HTTPS→HTTP redirect |

- **Field mapping:**

  | name | default URL | zip · city · region · country · code |
  |---|---|---|
  | `ipwhois` | `https://ipwho.is/` | `postal` `city` `region` `country` `country_code` |
  | `freeipapi` | `https://free.freeipapi.com/api/v1/json` | `zipCode` `cityName` `regionName` `countryName` `countryCode` |
  | `ipapico` | `https://ipapi.co/json/` | `postal` `city` `region` `country_name` `country_code` (if missing, `country`, which is the 2-letter code) |

  - Postal fields decode through a small `flexString` type that accepts a string, number or null, so a provider
    changing the field's type doesn't break parsing.
  - freeipapi sends `"-"` for a missing value. The first live run confirmed this for its zip code field.
- **Chain**: `Locate(ctx) (Location, []Attempt, error)`.
  - Each attempt's deadline is the remaining overall budget divided by the number of providers left. A provider that
    fails fast leaves its unused time to the later ones.
  - The last attempt runs directly on the overall context instead of a timer of its own. A separate timer would fire at
    the same instant as the overall deadline and could make a timeout look like a plain failure.
  - The first location that has a zip wins.
  - A location without a zip is recorded as `no_zip_code`. The first such partial result is kept and the chain moves
    on. Fields from different providers are never mixed.
  - If the parent context is canceled, the chain stops immediately.
  - It ends with the location, `ErrNoZipCode` (plus the partial result), or `ErrLookupFailed`; if the overall context
    ended first, it returns that context's error.
  - Every `Attempt{Provider, Kind, Err, Duration}` feeds `-verbose` and the JSON error document.
- **Test switch**: the `ZIPCODE_API_BASE_URL` env var, read through an injected `getenv`, moves *every* provider under
  one base URL: `<base>/ipwhois/`, `<base>/freeipapi/api/v1/json`, `<base>/ipapico/json/`.
  - It is all-or-nothing, so a test can never reach the internet by accident.
  - Plain `http://` is accepted only for loopback hosts.
  - The binary that is tested is the binary that ships; there are no build tags.

## CLI (`internal/cli`, stdlib `flag`)
Entry point: `cli.Run(ctx, args, cli.Env{Stdout, Stderr, Getenv, Version}) int`
```
zipcode [-json] [-provider ipwhois,freeipapi,ipapico] [-timeout 15s] [-verbose] [-version]
```
- **Flags:**
  - `-provider`: comma-separated list of providers to use, in order. Unknown, duplicate or empty names exit 2.
  - `-timeout`: total time budget for the lookup; must be greater than 0.
  - `-verbose`: one stderr line per attempt, such as `zipcode: ipwhois: rate_limited (12ms): HTTP 429 Too Many Requests`.
  - `-version`: prints `zipcode v0.1.1 (go1.26.9 linux/amd64)`. The version comes from `-X main.version` if set,
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
  - On lookup failure or interruption, it is `{"error":"…","attempts":[{"provider","kind","message"}]}`, so `jq` and
    `ConvertFrom-Json` never receive empty input.
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

| Package | Tests (with subtests) | Coverage |
|---|---|---|
| `internal/geo` | 21 (75) | 94.7% |
| `internal/cli` | 20 (29) | 99.0% |
| `cmd/zipcode` (end-to-end) | 15 (20), plus the live test | not measured |

**`internal/fakeapi`** (shared by every test layer):
- An `httptest` server on 127.0.0.1, routed by the first path segment (for example `/ipwhois/…`).
- Keep-alives are off, so every request uses a new connection. Otherwise Go's client would retry a request whose
  reused connection `Drop` closed, and the test would count two hits.
- Records each hit: order, time, method, path, headers.
- An unknown path calls `t.Errorf`; a provider the test didn't set up returns 503.
- Handler helpers:
  - `Respond(status, contentType, body)`, `JSON(fixture)`, `Status(code)`, `HTML403()`.
  - `Drop()`: closes the connection without answering (`panic(http.ErrAbortHandler)`). It fails fast on both OSes,
    unlike a closed port, which takes about 2 s per connection attempt on Windows.
  - `Hang()`: waits on the request context or on a `done` channel. A cleanup registered *after* `srv.Close` closes that
    channel, so it runs before the server shuts down.
- Ten JSON fixtures are embedded with `//go:embed testdata/*.json`. They use documentation IP addresses
  (192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24).

**Unit: providers** (each provider):
- Full success, with exact request checks: GET, exact path, User-Agent, Accept.
- ipapico's `country` is read as the code and `country_name` as the name; `country` is the fallback for a missing
  `country_code`.
- Postal empty, null or missing → empty zip, no error. Numeric postal, leading-zero postal, wrong-typed postal.
- `{}`, all-null or all-dash bodies → `bad_response`.
- Failure inside a 200 → `rejected`, keeping the provider's message; ipapico `RateLimited` → `rate_limited`.
- Across all three providers: 429, 500, 503, 403 HTML, 404, `Drop`, malformed JSON, empty body, HTML with status 200,
  oversized body, and `Hang` past a short deadline.
- Spaces trimmed, country code upper-cased, non-ASCII names preserved.
- An HTTPS server redirecting to plain HTTP is refused, and the HTTP server is never contacted.

**Unit: registry:** default order (matching `fakeapi.Providers`); `ParseNames` ordering, case and errors; the base-URL
rewrite for every provider; base-URL validation.

**Unit: chain** (stub providers, plus `testing/synctest` for a virtual clock): a success stops the chain; each kind
moves on and is recorded in order; other errors count as `unavailable`; the first partial result is kept and fields are
never mixed; a 15 s budget gives deadlines at exactly 5, 10 and 15 s; a fast failure gives 7.5 s to the next provider; a
parent cancel stops at once; no deadline means no attempt deadline.

**Unit: cli** (`Run` against fakeapi): exact text and JSON output; fallback with `-verbose`; `-provider` selection and
order; seven usage errors → 2; `-h`/`-help`/`--help` → stdout and 0; `-version`; insecure base URL → 2; all providers
failing, in text and JSON; no zip → 3; timeout; interruption before and during the lookup → 130; output helpers.

**End-to-end: the compiled binary** (`cmd/zipcode/e2e_test.go`, run by plain `go test ./...`, a few seconds on Linux):
- `TestMain` builds the binary once with `CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=e2e"` into a temp
  directory, adding `.exe` on Windows, and runs `-version` once as a warm-up for Windows Defender's first scan.
- `runCLI` uses `exec.CommandContext(t.Context(), …)` with `WaitDelay`. It removes inherited `ZIPCODE_*` and proxy
  variables (matching names case-insensitively), then sets `ZIPCODE_API_BASE_URL` and points `HTTP(S)_PROXY` at a dead
  port. Go never proxies loopback, so the fakes still work and any accidental real request fails.
- Timing checks are upper bounds with some slack.

E2E scenarios, all passing:
1. `-version` prints `zipcode e2e (…)`; `-h` exits 0; a bad flag, `-provider nope` or a positional argument exits 2.
   None of them makes a request.
2. Text happy path, byte-exact: only ipwhois is hit, with User-Agent `zipcode/e2e`.
3. `-json` happy path: one compact object with `provider:"ipwhois"`.
4. ipwhois returns 429, freeipapi returns malformed JSON, ipapico succeeds: exit 0, hit order checked, `-verbose`
   shows both reasons.
5. ipwhois returns `success:false` in a 200: falls through to the next provider.
6. ipwhois has no postal: the output comes entirely from freeipapi, with no mixed fields.
7. No zip from anyone: exit 3, `Zip code: unknown` plus the first partial result; `-json` has `"zip_code":""`.
8. 503, `Drop` and 403 HTML: exit 1, stderr names all three, and `-json` prints the error document with the kinds.
9. ipwhois hangs with `-timeout 3s`: freeipapi is asked about 1 s later (server clock), exit 0.
10. All providers hang with `-timeout 1500ms`: exit 1 with "timed out after 1.5s", in about 1.5 s.
11. `-provider freeipapi` asks only freeipapi; `-provider ipapico,ipwhois` keeps that order.
12. "São Paulo" with zip "01310-100" comes back byte-exact, in text and JSON.
13. SIGINT while a provider hangs: exit 130. Skipped on Windows, where `Process.Signal(os.Interrupt)` isn't
    supported; the cli cancellation tests cover that case.

**Live test** (`ZIPCODE_LIVE_TEST=1` / `make test-live`; also a weekly CI schedule and manual runs):
- Runs `-provider X -json` for each provider against the real service.
- Fails only on `bad_response`, which means a provider changed its API.
- Skips on rate limit, outage, timeout or no zip.
- Never part of `make test`, because it sends the machine's IP to the providers.

## Windows details
- `.gitattributes` sets `eol=lf`, since Actions checks files out with CRLF by default. The tool writes `\n` only.
- Fakes bind 127.0.0.1, so there's no firewall prompt. Tests use `srv.URL`, never `localhost`.
- Child processes are always waited for, because a running exe locks its temp dir.
- Signal tests use `t.Skip`, not a file-name suffix: `_unix_test.go` does not exclude a file on Windows.
- The README notes three things for Windows users:
  - Go ignores the Windows system proxy; set `HTTPS_PROXY` instead.
  - Go uses the Windows certificate store, so corporate root certificates work.
  - Windows PowerShell 5.1 needs `[Console]::OutputEncoding=[Text.Encoding]::UTF8` to capture non-ASCII output
    correctly.

## Build, lint, CI
- **go.mod**: `go 1.25.0`, as written by `go mod init` on Go 1.26. No Go 1.26-only APIs are used; `synctest`,
  `t.Context`, `strings.SplitSeq` and `WaitDelay` are all available in 1.25.
- **Makefile** (for Linux development; CI calls `go` directly except for the release builds):
  - `VERSION ?= $(git describe --tags --always --dirty || echo dev)`.
  - `build` → `bin/zipcode`.
  - `cross` → `dist/zipcode-{linux,windows}-{amd64,arm64}[.exe]`, built with
    `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X main.version=$(VERSION)"`.
  - `package` → release archives and `SHA256SUMS` in `dist/release/` (see [Releases](#releases)).
  - `test` (`-count=1 ./...`), `test-unit` (`-race ./internal/...`), `test-e2e` (`./cmd/...`), `test-live`.
  - `cover`; `vet` (native and `GOOS=windows`); `lint`; `fmt`; `fmt-check` (`golangci-lint fmt --diff`).
  - `check` runs fmt-check, vet, lint and test; `clean`.
  - The README lists the plain `go` commands for Windows users without make.
- **.golangci.yml** (v2 format): the default linters plus bodyclose, noctx, errorlint, usetesting, gosec (off for tests
  and `internal/fakeapi`), misspell and nilerr. Formatters: gofumpt and goimports.
- **ci.yml**:
  - Triggers: pushes to branches (tags belong to `release.yml`), pull requests, manual runs and a weekly schedule.
  - Settings: `permissions: contents: read`; superseded runs are canceled; `shell: bash`.
  - Actions: `actions/checkout@v7` and `actions/setup-go@v7` with `go-version: 1.26.x` (Go 1.26.9 at the time) and
    `cache-dependency-path: go.mod`, because there is no `go.sum`.
  - Jobs:
    - `lint` (ubuntu): vet for Linux and Windows, then `golangci/golangci-lint-action@v9` pinned to `v2.11.1`. The
      linter also reports unformatted files, so there is no separate format step.
    - `test` (ubuntu and windows, fail-fast off): `go test -count=1 ./...`, adding `-race` on ubuntu. Race on Windows
      needs cgo and MinGW and has been flaky before, so it is skipped.
    - `cross` (ubuntu): `make cross`, then `actions/upload-artifact@v7`.
    - `live` (schedule and manual runs only, both OSes): `ZIPCODE_LIVE_TEST=1 go test -run TestLive ./cmd/zipcode`.

## Releases
Added after the original plan; [RELEASING.md](RELEASING.md) is the full guide.
- **Trigger:** pushing a tag that starts with `v` runs `.github/workflows/release.yml`.
- **`test` job:** unit and end-to-end tests of the tagged commit on `ubuntu-latest` and `windows-latest`. Nothing is
  published unless both pass.
- **`release` job** (ubuntu, the only job with `permissions: contents: write`):
  - `make package VERSION=<tag>` builds the four binaries and packs each with `README.md` and `LICENSE`: `.tar.gz` for
    Linux, which keeps the executable permission, and `.zip` for Windows. It also writes `SHA256SUMS`.
  - `gh release create <tag> dist/release/* --generate-notes --verify-tag` publishes the release, titled
    `zipcode <tag>`.
- **Published releases:**
  - `v0.1.0`: the first release.
  - `v0.1.1`: the same code, with `LICENSE` added to every archive. GitHub reports it as the latest release.
- **Downloads** need no GitHub account. The README and RELEASING.md fetch the latest release with `curl` or PowerShell,
  finding its tag through `https://api.github.com/repos/vichugof/zipcodeout/releases/latest`, and check `SHA256SUMS`.

## Repository setup
- Default branch `main`. Commits use an author identity set in this repository's own git config.
- The global git config on the development machine rewrites `https://github.com/` URLs to SSH, and that machine's SSH
  key belongs to a different GitHub account than the repository owner. Pushing over SSH therefore failed with
  "Repository not found". This repository's `.git/config` fixes that without touching global settings:
  - `origin` is `https://github.com/vichugof/zipcodeout.git`.
  - A more specific `url.https://github.com/vichugof/.insteadOf` rule exempts it from the SSH rewrite.
  - `gh auth git-credential` supplies the credentials of the active `gh` account.
- Pushing workflow files needs the `workflow` scope on the `gh` token (`gh auth refresh -h github.com -s workflow`).
- Before the repository was made public, its full history was checked for tokens, keys and personal data; none was
  found.

## Changes from the original plan
1. **Chain timing:** the last attempt runs on the overall context. The synctest tests showed that a separate timer at
   the same instant could report a timeout as "could not determine your location".
2. **Chain structure:** an `attempt()` helper creates and cancels each attempt's context, instead of a helper returning
   a cancel function. gosec flagged the original shape (G118), and the new one is simpler.
3. **Fake API:** keep-alives are off and `Drop` aborts the handler. Both came from how Go's client retries requests.
4. **CI triggers:** `ci.yml` runs only for branches, so a tag push runs only the release workflow, which has its own
   tests.
5. **CI details:** no separate format-check step (golangci-lint covers it), and setup-go caches using `go.mod`.
6. **Added:** the release workflow, `make package`, RELEASING.md, the MIT LICENSE, and no-login download
   instructions.

## Verification results
1. `make check` passes: no formatting changes needed, vet clean for Linux and Windows, golangci-lint reports 0 issues,
   and all tests pass.
2. The unit tests pass with the race detector. The chain tests passed 20 runs in a row after the timing fix.
3. Coverage: 94.7% in `internal/geo` and 99.0% in `internal/cli`, above the 85% target.
4. Test caching: changing a provider's code makes the end-to-end tests rerun instead of reporting `(cached)`. A
   comment appended at the end of a file doesn't change the binary, so that case correctly stays cached.
5. Cross builds: static ELF binaries for Linux and PE32+ console executables for Windows, on amd64 and arm64. Stripping
   with `-s -w` makes them 31% smaller (8.7 MB to 6.0 MB).
6. Live run from the development machine: ipwho.is and ipapi.co each returned a zip code for the same connection, but
   different ones, which shows how approximate IP geolocation is. freeipapi found the city but returned `"-"` for the
   zip code.
7. CI passed on `ubuntu-latest` and `windows-latest` for every pushed commit. On Windows, the end-to-end tests ran the
   compiled `zipcode.exe`.
8. Both releases were downloaded without a login: all checksums match, every archive contains the expected files, and
   the binaries report their release version (for example `zipcode v0.1.1 (go1.26.9 linux/amd64)`).

## Risks / follow-ups
- **The released `.exe` hasn't run on a real Windows machine.** CI runs a binary built from the same commit on
  Windows, but not the cross-compiled release file. Running `zipcode.exe -version` from a release zip would close this.
- **Free-tier quotas are per public IP.** Office networks and CI runners share them. The fallback chain absorbs this;
  a short-lived cache under `os.UserCacheDir` is a possible v2.
- **Free APIs change shape** (ipinfo already did). The weekly live job acts as the contract test, and replacing a
  provider means changing one file. GitHub disables scheduled workflows in public repositories after 60 days without
  activity, so that job can stop if the repository goes quiet.
- **The binaries aren't code-signed.** Windows SmartScreen can warn on the first run of a browser download; RELEASING.md
  explains how to get past it.
- **The `v0.1.0` archives contain a README that still calls the repository private.** Later releases have the
  corrected text.
