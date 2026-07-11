BINARY      := pgtui
PKG         := github.com/9level/pg-tui
IMAGE       := ghcr.io/9level/pg-tui
GO          := go
LDFLAGS     := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := build

## build: compila o binário ./pgtui
.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) .

## run: compila e executa (lê ./.env)
.PHONY: run
run:
	$(GO) run .

## install: instala em /usr/local/bin (sudo)
.PHONY: install
install: build
	install -m 0755 $(BINARY) /usr/local/bin/$(BINARY)

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

## release: valida semver + working tree limpa, cria tag anotada e faz push
##          uso: make release VERSION=v0.1.0
.PHONY: release
release:
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "VERSION inválido (esperado vX.Y.Z): '$(VERSION)'"; exit 1; }
	@git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null && { echo "tag $(VERSION) já existe"; exit 1; } || true
	@test -z "$$(git status --porcelain)" || { echo "working tree suja — commite ou stashe antes de release"; exit 1; }
	@git tag -a "$(VERSION)" -m "release $(VERSION)"
	@git push origin "$(VERSION)"
	@echo "tag $(VERSION) publicada — o CI vai buildar e publicar $(IMAGE):$(VERSION)"

## docker-build: build local da imagem distroless
.PHONY: docker-build
docker-build:
	docker build -f deploy/Dockerfile -t $(IMAGE):$(if $(VERSION),$(VERSION),dev) .

## docker-push: push da imagem (requer login no GHCR)
.PHONY: docker-push
docker-push: docker-build
	docker push $(IMAGE):$(if $(VERSION),$(VERSION),dev)

## help: lista os alvos
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
