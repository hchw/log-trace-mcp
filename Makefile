# log-trace-mcp Makefile
#
# Common targets: make build | test | run | tools | keygen | validate | clean

BINARY      := log-trace-mcp
PKG         := ./cmd/log-trace-mcp
BIN_DIR     := bin
CHANGE      := unified-log-query-mcp
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.appVersion=$(VERSION)

# Go module proxy (override with `make GOPROXY=...`).
export GOPROXY ?= https://goproxy.cn,direct

.DEFAULT_GOAL := build

## build: compile the single binary into bin/
.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

## run: build and start the server + admin page (pass ARGS=--stdio for stdio MCP)
.PHONY: run
run: build
	./$(BIN_DIR)/$(BINARY) serve $(ARGS)

## test: run the unit test suite
.PHONY: test
test:
	go test ./...

## test-race: run tests with the race detector
.PHONY: test-race
test-race:
	go test -race ./...

## coverage: run tests and print a coverage summary
.PHONY: coverage
coverage:
	go test -cover ./...

## vet: run go vet
.PHONY: vet
vet:
	go vet ./...

## fmt: format all Go sources
.PHONY: fmt
fmt:
	go fmt ./...

## tidy: sync go.mod/go.sum
.PHONY: tidy
tidy:
	go mod tidy

## check: fmt, vet and test
.PHONY: check
check: fmt vet test

## tools: list the registered MCP tools
.PHONY: tools
tools: build
	./$(BIN_DIR)/$(BINARY) tools

## version: print the built version
.PHONY: version
version: build
	./$(BIN_DIR)/$(BINARY) version

## keygen: generate a base64 32-byte master key for LOG_TRACE_MASTER_KEY
.PHONY: keygen
keygen:
	@head -c 32 /dev/urandom | base64

## config: generate config.json from config.example.json with a fresh master key
.PHONY: config
config:
	@if [ -f config.json ]; then echo "config.json already exists; refusing to overwrite"; exit 1; fi
	@key=$$(head -c 32 /dev/urandom | base64); \
	  sed "s#<MASTER_KEY>#$$key#" config.example.json > config.json; \
	  echo "wrote config.json — edit admin_token before running"

## rekey: re-encrypt stored credentials with a new key (NEW_KEY=... or --key)
.PHONY: rekey
rekey: build
	./$(BIN_DIR)/$(BINARY) rekey $(ARGS)

## validate: validate the OpenSpec change
.PHONY: validate
validate:
	openspec-cn validate "$(CHANGE)"

## cross: build release binaries for linux/darwin on amd64/arm64 into dist/
.PHONY: cross
cross:
	@mkdir -p dist
	@for os in linux darwin; do \
	  for arch in amd64 arm64; do \
	    out=dist/$(BINARY)-$$os-$$arch; \
	    echo "building $$out"; \
	    GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $$out $(PKG) || exit 1; \
	  done; \
	done

## clean: remove build artifacts
.PHONY: clean
clean:
	rm -rf $(BIN_DIR) dist

## help: list available targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'
