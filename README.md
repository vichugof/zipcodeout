# zipcode

`zipcode` is a small command-line tool for Linux and Windows. It prints the zip
code, city, region and country of the place where you are, estimated from your
public IP address.

```console
$ zipcode
Zip code: 10001
City:     New York
Region:   New York
Country:  United States (US)
```

It is a single static binary with no dependencies beyond the Go standard
library.

## How it works

The tool asks free geolocation services, which need no API key, where your
public IP address is. It tries them in order and stops at the first one that
knows your zip code:

| Name | Service | Free tier |
|---|---|---|
| `ipwhois` | [ipwho.is](https://ipwho.is/) | 1,000 requests per day per IP address |
| `freeipapi` | [freeipapi.com](https://freeipapi.com/) | 60 requests per minute |
| `ipapico` | [ipapi.co](https://ipapi.co/) | quota not published |

A provider that is rate limited, down, slow or returns something unusable is
skipped, and the next one is asked. Results are never mixed: the city and
country always come from the same provider as the zip code.

### Accuracy and privacy

- **The location is approximate.** It is where your internet connection meets
  the wider network, which is usually your ISP's nearby equipment. With a VPN,
  a mobile network or a corporate proxy, it can be a different city or
  country.
- **Your public IP address is sent to each provider the tool asks.**

## Install

You need [Go](https://go.dev/dl/) 1.25 or later.

```sh
# Linux
make build              # writes bin/zipcode
# or
go build -o zipcode ./cmd/zipcode
```

```powershell
# Windows (PowerShell or cmd)
go build -o zipcode.exe ./cmd/zipcode
```

`make cross` builds static binaries for Linux and Windows (amd64 and arm64)
into `dist/`. The CI workflow publishes the same set as an artifact.

## Usage

```
zipcode [-json] [-provider list] [-timeout duration] [-verbose] [-version]
```

| Flag | Meaning |
|---|---|
| `-json` | Print the result as one JSON object |
| `-provider list` | Comma-separated providers to try, in order (default `ipwhois,freeipapi,ipapico`) |
| `-timeout duration` | Total time allowed for the lookup (default `15s`) |
| `-verbose` | Report every provider attempt on stderr |
| `-version` | Print version information and exit |
| `-h`, `-help` | Show help |

The time budget is shared between the providers. Each attempt gets an equal
share of the time left, so a provider that fails fast leaves more time for the
next.

### JSON output

```console
$ zipcode -json
{"zip_code":"10001","city":"New York","region":"New York","country":"United States","country_code":"US","ip":"203.0.113.7","provider":"ipwhois"}
```

With `-json`, stdout always holds exactly one JSON object. A failure looks like
this, and a human-readable summary also goes to stderr:

```json
{"error":"could not determine your location","attempts":[{"provider":"ipwhois","kind":"rate_limited","message":"HTTP 429 Too Many Requests"},{"provider":"freeipapi","kind":"timeout","message":"..."},{"provider":"ipapico","kind":"unavailable","message":"HTTP 503 Service Unavailable"}]}
```

The attempt kinds are `rate_limited`, `rejected`, `unavailable`, `timeout`,
`bad_response` and `no_zip_code`.

```powershell
# PowerShell
$location = zipcode -json | ConvertFrom-Json
$location.zip_code
```

### Exit status

| Code | Meaning |
|---|---|
| 0 | Zip code found |
| 1 | Lookup failed or timed out |
| 2 | Invalid usage |
| 3 | Location found, but no provider knows its zip code. The rest of the location is still printed, with `unknown` (or `""` in JSON) for the zip code. |
| 130 | Interrupted (Ctrl+C) |

### Notes for Windows

- **Proxies:** Go does not read the Windows proxy settings. If you need a proxy,
  set `HTTPS_PROXY`, for example `$env:HTTPS_PROXY = "http://proxy.example.com:8080"`.
- **Certificates:** Go uses the Windows certificate store, so corporate root
  certificates work.
- **Non-ASCII names in PowerShell:** Windows PowerShell 5.1 decodes captured
  output with the console's legacy code page, so names such as "São Paulo" come
  out garbled in `$x = zipcode`. Run this first:

  ```powershell
  [Console]::OutputEncoding = [Text.Encoding]::UTF8
  ```

  Printing straight to the console is not affected.

## Development

```
cmd/zipcode/       main package, end-to-end and live tests
internal/geo/      providers, the fallback chain, provider registry
internal/cli/      flags, output and exit codes
internal/fakeapi/  local fake of the providers, used only by tests
```

| Make target | Plain Go equivalent | What it runs |
|---|---|---|
| `make test` | `go test -count=1 ./...` | All unit and end-to-end tests |
| `make test-unit` | `go test -race -count=1 ./internal/...` | Unit tests, with the race detector |
| `make test-e2e` | `go test -count=1 ./cmd/...` | End-to-end tests of the compiled binary |
| `make test-live` | `ZIPCODE_LIVE_TEST=1 go test -count=1 -run TestLive -v ./cmd/zipcode` | Asks the real providers (sends your IP to them) |
| `make cover` | | Unit test coverage |
| `make vet` | `go vet ./...` and `GOOS=windows go vet ./...` | Vet for Linux and Windows |
| `make lint` | `golangci-lint run` | Linters and format check |
| `make check` | | Everything CI checks |

How the tests are built:

- **No test reaches the internet**, except the opt-in live test. Setting
  `ZIPCODE_API_BASE_URL` serves every provider from one base URL, for example
  `http://127.0.0.1:8080/ipwhois/`. The tests point it at `internal/fakeapi`,
  which can answer with recorded responses, errors, dropped connections or no
  answer at all. Plain `http` is only accepted for loopback addresses.
- **The end-to-end tests run the real binary.** `TestMain` builds it the same
  way a release is built, then the tests run it as a separate process and check
  stdout, stderr, exit codes and the requests it sent.
- **The live test is a contract check.** It fails only when a provider's
  response no longer has the shape the tool expects. Rate limits, outages and
  missing zip codes make it skip instead.

CI runs on Ubuntu and Windows: lint, vet, and the unit and end-to-end tests,
with the race detector on Linux. It also builds the release binaries. The live
test runs weekly and on manual dispatch.
