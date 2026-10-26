BINARY    := zipcode
PKG       := ./cmd/zipcode
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: build cross test test-unit test-e2e test-live cover vet lint fmt fmt-check check clean

## build: build bin/zipcode for this machine
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

## cross: build static binaries for Linux and Windows into dist/
cross:
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		out=dist/$(BINARY)-$$os-$$arch$$ext; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out $(PKG) || exit 1; \
	done

## test: run the unit and end-to-end tests
test:
	go test -count=1 ./...

## test-unit: run the unit tests with the race detector
test-unit:
	go test -race -count=1 ./internal/...

## test-e2e: run the end-to-end tests against the compiled binary
test-e2e:
	go test -count=1 ./cmd/...

## test-live: query the real providers (sends your public IP address to them)
test-live:
	ZIPCODE_LIVE_TEST=1 go test -count=1 -run TestLive -v $(PKG)

## cover: report unit test coverage
cover:
	go test -count=1 -coverprofile=coverage.out ./internal/geo ./internal/cli
	go tool cover -func=coverage.out

## vet: vet the code for this machine and for Windows
vet:
	go vet ./...
	GOOS=windows go vet ./...

## lint: run golangci-lint
lint:
	golangci-lint run

## fmt: format the code
fmt:
	golangci-lint fmt

## fmt-check: fail if any file needs formatting
fmt-check:
	@diff=$$(golangci-lint fmt --diff); \
	if [ -n "$$diff" ]; then echo "$$diff"; echo "run 'make fmt'"; exit 1; fi

## check: everything CI checks
check: fmt-check vet lint test

## clean: remove build output
clean:
	rm -rf bin dist coverage.out
