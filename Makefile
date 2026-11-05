# Bunker build tasks. `make help` lists them.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PKG     := github.com/kucukkanat/bunker
LDFLAGS := -s -w -X $(PKG)/internal/core.Version=$(VERSION)
IMAGE   ?= ghcr.io/kucukkanat/bunker
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

export CGO_ENABLED := 0

.PHONY: help build dev test vet docker docker-run release-local clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  \033[1m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build ./bunker for this machine
	go build -trimpath -ldflags "$(LDFLAGS)" -o bunker ./cmd/bunker

dev: ## Run locally in dev mode (single local admin, no sign-in)
	go run ./cmd/bunker dev

test: ## Run go vet and the tests
	go vet ./...
	go test ./...

vet: ## Run go vet
	go vet ./...

docker: ## Build the Docker image ($(IMAGE):$(VERSION))
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

docker-run: docker ## Build and run the Docker image on :8080
	docker run --rm -p 8080:8080 -v bunker-data:/data $(IMAGE):$(VERSION)

release-local: ## Cross-compile release binaries and checksums.txt into dist/
	@rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		out=dist/bunker-$$os-$$arch$$ext; \
		echo "  build $$out"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out ./cmd/bunker || exit 1; \
	done
	@cd dist && (sha256sum bunker-* 2>/dev/null || shasum -a 256 bunker-*) > checksums.txt
	@cat dist/checksums.txt

clean: ## Remove build output
	rm -rf bunker bunker.exe dist
