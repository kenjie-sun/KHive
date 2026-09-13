BINARY_NAME ?= khive
GO ?= go
NPM ?= npm
NODE ?= node
GO_TAGS ?= with_utls nomsgpack
GOOS ?= linux
CGO_ENABLED ?= 0
VERSION ?= v1.0.0
VERSION_TAG = $(if $(filter v%,$(VERSION)),$(VERSION),v$(VERSION))
BUILD_TIME ?= $(shell date -u "+%Y-%m-%dT%H:%M:%SZ")
DIST_DIR ?= dist
MAIN_PACKAGE ?= ./cmd/vohive

LDFLAGS = -s -w -X 'github.com/1239t/vohive/internal/global.Version=$(VERSION)' -X 'github.com/1239t/vohive/internal/global.BuildTime=$(BUILD_TIME)'
GO_BUILD = $(GO) build -mod=readonly -trimpath -buildvcs=false -tags "$(GO_TAGS)" -ldflags "$(LDFLAGS)"

AMD64_OUT = $(DIST_DIR)/$(BINARY_NAME)_$(VERSION_TAG)_linux_amd64
ARM64_OUT = $(DIST_DIR)/$(BINARY_NAME)_$(VERSION_TAG)_linux_arm64
ARMV7_OUT = $(DIST_DIR)/$(BINARY_NAME)_$(VERSION_TAG)_linux_armv7
UPX ?= $(shell command -v upx || command -v upx-ucl)
UPX_FLAGS ?= --best --lzma

.PHONY: all build build-amd64 build-arm64 build-armv7 build-all frontend-dist clean

all: build

build: build-amd64

build-all: build-amd64 build-arm64 build-armv7

frontend-dist:
	$(NPM) run build --prefix web
	$(NODE) scripts/sync-web-dist.mjs

build-amd64: frontend-dist
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=amd64 $(GO_BUILD) -o $(AMD64_OUT) $(MAIN_PACKAGE)
	@if [ -n "$(UPX)" ]; then $(UPX) $(UPX_FLAGS) $(AMD64_OUT); fi

build-arm64: frontend-dist
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=arm64 $(GO_BUILD) -o $(ARM64_OUT) $(MAIN_PACKAGE)
	@if [ -n "$(UPX)" ]; then $(UPX) $(UPX_FLAGS) $(ARM64_OUT); fi

build-armv7: frontend-dist
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=arm GOARM=7 $(GO_BUILD) -o $(ARMV7_OUT) $(MAIN_PACKAGE)
	@if [ -n "$(UPX)" ]; then $(UPX) $(UPX_FLAGS) $(ARMV7_OUT); fi

clean:
	go clean
	rm -rf $(DIST_DIR)

.PHONY: deps test check-release
deps:
	$(GO) mod download
	$(GO) mod verify
	$(NPM) ci --prefix web --ignore-scripts --no-audit --no-fund

test:
	$(GO) test -tags "$(GO_TAGS)" ./...

check-release:
	python3 scripts/check-release.py
