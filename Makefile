GO ?= go
BIN := bin
CHECKEDCOV := $(BIN)/checkedcov
EDGECOV := $(BIN)/edgecov

# PKG accepts a Go import path (e.g. strings, net/url, encoding/json) OR a
# filesystem directory. It is resolved to a directory with `go list`.
PKG ?=
DIR = $(shell if [ -d "$(PKG)" ]; then echo "$(PKG)"; else $(GO) list -f '{{.Dir}}' "$(PKG)"; fi)

.PHONY: build check edge analyze test clean

## build: compile both analyzers into ./bin
build: $(CHECKEDCOV) $(EDGECOV)

$(CHECKEDCOV): $(shell find cmd/checkedcov internal/checkedcovssa -name '*.go')
	@mkdir -p $(BIN)
	$(GO) build -o $@ ./cmd/checkedcov

$(EDGECOV): $(shell find cmd/edgecov internal/edgecovssa internal/checkedcovssa -name '*.go')
	@mkdir -p $(BIN)
	$(GO) build -o $@ ./cmd/edgecov

## check: checked-coverage (covered-but-unchecked lines) for PKG
##   make check PKG=strings
check: $(CHECKEDCOV)
	@test -n "$(PKG)" || { echo "usage: make check PKG=<import-path-or-dir>"; exit 2; }
	$(CHECKEDCOV) "$(DIR)"

## edge: checked-edge coverage (edges/branches/effects) for PKG
##   make edge PKG=net/url
edge: $(EDGECOV)
	@test -n "$(PKG)" || { echo "usage: make edge PKG=<import-path-or-dir>"; exit 2; }
	$(EDGECOV) "$(DIR)"

## analyze: run both analyzers on PKG (delegates to scripts/analyze.sh)
##   make analyze PKG=strconv
analyze: build
	@test -n "$(PKG)" || { echo "usage: make analyze PKG=<import-path-or-dir>"; exit 2; }
	@CHECKEDCOV=$(CHECKEDCOV) EDGECOV=$(EDGECOV) scripts/analyze.sh "$(PKG)"

## test: run the analyzers' own unit tests
test:
	$(GO) test ./internal/checkedcovssa/ ./internal/edgecovssa/

clean:
	rm -rf $(BIN)
