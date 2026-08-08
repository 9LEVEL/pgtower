BINARY  := pgtui
PKG     := github.com/9level/pgtui
GO      := go
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

# Platforms for build-all (compiled utility, no Docker).
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 darwin/amd64

.DEFAULT_GOAL := build

## build: compiles the ./pgtui binary for the current platform
.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .

## run: compiles and runs (reads ./config.yml or env vars)
.PHONY: run
run:
	$(GO) run .

## install: installs to /usr/local/bin (sudo)
.PHONY: install
install: build
	install -m 0755 $(BINARY) /usr/local/bin/$(BINARY)

## build-all: cross-compiles binaries to dist/ (one tag = several binaries)
.PHONY: build-all
build-all:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=dist/$(BINARY)-$(VERSION)-$$os-$$arch; \
		echo "-> $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out . || exit 1; \
	done
	@cd dist && { command -v sha256sum >/dev/null 2>&1 && sha256sum $(BINARY)-$(VERSION)-* || shasum -a 256 $(BINARY)-$(VERSION)-*; } > SHA256SUMS
	@echo "binaries + SHA256SUMS in dist/"

## test: unit + integration smoke (uses DATABASE_URL if set)
.PHONY: test
test:
	$(GO) test ./... $(if $(VERBOSE),-v,)

## vet: go vet
.PHONY: vet
vet:
	$(GO) vet ./...

## tidy: go mod tidy
.PHONY: tidy
tidy:
	$(GO) mod tidy

## clean: removes artifacts
.PHONY: clean
clean:
	rm -f $(BINARY)
	rm -rf dist

## release: validates semver + clean working tree, creates annotated tag and pushes
##          usage: make release VERSION=v0.3.0
.PHONY: release
release:
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "invalid VERSION (expected vX.Y.Z): '$(VERSION)'"; exit 1; }
	@git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null && { echo "tag $(VERSION) already exists"; exit 1; } || true
	@test -z "$$(git status --porcelain)" || { echo "working tree dirty — commit or stash before release"; exit 1; }
	@git tag -a "$(VERSION)" -m "release $(VERSION)"
	@git push origin "$(VERSION)"
	@echo "tag $(VERSION) pushed — CI will build and attach the binaries to the release"

## help: lists the targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
