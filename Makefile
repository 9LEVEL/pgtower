BINARY  := pgtui
PKG     := github.com/9level/pg-tui
GO      := go
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

# Plataformas para build-all (utilitário compilado, sem Docker).
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 darwin/amd64

.DEFAULT_GOAL := build

## build: compila o binário ./pgtui para a plataforma atual
.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .

## run: compila e executa (lê ./.env)
.PHONY: run
run:
	$(GO) run .

## install: instala em /usr/local/bin (sudo)
.PHONY: install
install: build
	install -m 0755 $(BINARY) /usr/local/bin/$(BINARY)

## build-all: cross-compila binários para dist/ (uma tag = vários binários)
.PHONY: build-all
build-all:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=dist/$(BINARY)-$(VERSION)-$$os-$$arch; \
		echo "-> $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out . || exit 1; \
	done
	@echo "binários em dist/"

## test: unit + smoke de integração (usa DATABASE_URL se definido)
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

## clean: remove artefatos
.PHONY: clean
clean:
	rm -f $(BINARY)
	rm -rf dist

## release: valida semver + working tree limpa, cria tag anotada e faz push
##          uso: make release VERSION=v0.3.0
.PHONY: release
release:
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "VERSION inválido (esperado vX.Y.Z): '$(VERSION)'"; exit 1; }
	@git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null && { echo "tag $(VERSION) já existe"; exit 1; } || true
	@test -z "$$(git status --porcelain)" || { echo "working tree suja — commite ou stashe antes de release"; exit 1; }
	@git tag -a "$(VERSION)" -m "release $(VERSION)"
	@git push origin "$(VERSION)"
	@echo "tag $(VERSION) publicada — o CI vai buildar e anexar os binários ao release"

## help: lista os alvos
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
