GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
MODULE  := github.com/runix/runix

LDFLAGS := -s -w \
	-X $(MODULE)/internal/platform/version.version=$(VERSION) \
	-X $(MODULE)/internal/platform/version.commit=$(COMMIT) \
	-X $(MODULE)/internal/platform/version.date=$(DATE)

BINARIES  := runix-server runix-agent
PLATFORMS := linux/amd64 linux/arm64

WEBUI_DIST := internal/webui/dist

.PHONY: all build test vet lint tidy release release-binaries web-embed clean \
        web-install web-build web-dev

all: build

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run

tidy:
	$(GO) mod tidy

# Export the UI and stage it where internal/webui embeds it from. The
# control plane is one binary, so this has to happen before `go build`;
# a checkout without it still compiles, against a placeholder page.
web-embed: web-build
	rm -rf $(WEBUI_DIST)
	cp -r web/out $(WEBUI_DIST)

# Everything a release needs: UI embedded, then static builds.
release: web-embed release-binaries

# Static, CGO-free release builds for every supported platform.
release-binaries:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		for b in $(BINARIES); do \
			echo "building dist/$${os}_$${arch}/$$b"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath \
				-ldflags '$(LDFLAGS)' -o dist/$${os}_$${arch}/$$b ./cmd/$$b || exit 1; \
		done; \
	done

clean:
	rm -rf bin dist web/.next

web-install:
	cd web && npm ci

web-build:
	cd web && npm run build

web-dev:
	cd web && npm run dev
