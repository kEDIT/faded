# faded — build, test, and cross-compile
#
# Common targets:
#   make            build the binary for your machine
#   make test       run the unit tests
#   make cross      build binaries for every platform in PLATFORMS -> dist/
#   make dist/faded_darwin_arm64   build one specific platform
#   make clean      remove the binary and dist/

BINARY  := faded
DIST    := dist
GO      ?= go
GOFLAGS ?= -mod=vendor
LDFLAGS ?= -s -w

# Platforms to cross-compile for. Override on the command line, e.g.:
#   make cross PLATFORMS="linux/amd64 windows/amd64"
PLATFORMS ?= \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

# Turn "linux/amd64" into the output path "dist/faded_linux_amd64".
BINARIES := $(foreach p,$(PLATFORMS),$(DIST)/$(BINARY)_$(subst /,_,$(p)))

.PHONY: all build test testv cover race vet fmt tidy cross clean help

all: build

## build: compile the binary for the current OS/ARCH
build:
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) .

## test: run unit tests
test:
	$(GO) test ./...

## testv: run unit tests, verbose
testv:
	$(GO) test -v ./...

## cover: run tests with a coverage summary
cover:
	$(GO) test -cover ./...

## race: run tests under the race detector
race:
	$(GO) test -race ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: gofmt the sources in place
fmt:
	$(GO) fmt ./...

## tidy: tidy go.mod / go.sum
tidy:
	$(GO) mod tidy

## cross: build binaries for every platform in PLATFORMS
cross: $(BINARIES)

# Pattern rule: dist/faded_<os>_<arch>[.exe]
# The stem ($*) is "<os>_<arch>"; split it back into GOOS/GOARCH.
$(DIST)/$(BINARY)_%:
	@mkdir -p $(DIST)
	$(eval GOOS  := $(word 1,$(subst _, ,$*)))
	$(eval GOARCH := $(word 2,$(subst _, ,$*)))
	@echo "building $(GOOS)/$(GOARCH) -> $@$(if $(filter windows,$(GOOS)),.exe,)"
	@GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' \
		-o $@$(if $(filter windows,$(GOOS)),.exe,) .

## clean: remove build artifacts
clean:
	rm -rf $(BINARY) $(DIST)

## help: list the documented targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
