# Everything also builds inside Docker (`docker compose build`); these targets are
# for local development with Go and Node installed.

VERSION ?= dev
LDFLAGS := -s -w -X github.com/Jackolix/Lotse/internal/version.Version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all web hub agents release test check dev-hub dev-web docker clean

all: web hub agents

web:
	cd web && npm ci && npm run build

hub:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/hub ./cmd/hub

# Cross-compiled agents land in dist/agents, where a locally run hub serves them.
agents:
	@mkdir -p dist/agents
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "agent $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/agents/lotse-agent-$$os-$$arch$$ext ./cmd/agent || exit 1; \
	done

# Release assets: agents for every platform, the hub for Linux, and checksums.
# Needs the web UI built first (make web), since the hub embeds it.
release:
	@rm -rf dist/release && mkdir -p dist/release
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "agent $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/release/lotse-agent-$$os-$$arch$$ext ./cmd/agent || exit 1; \
	done
	@for arch in amd64 arm64; do \
		echo "hub linux/$$arch"; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/release/lotse-hub-linux-$$arch ./cmd/hub || exit 1; \
	done
	cd dist/release && sha256sum * > sha256sums.txt

test:
	go test -race ./...

check: test
	go vet ./...
	cd web && npm run check

# Hub on :8090 with data in ./data; pair with dev-web for hot reload on :5173.
dev-hub:
	go run ./cmd/hub

dev-web:
	cd web && npm run dev

docker:
	docker compose build --build-arg VERSION=$(VERSION)

clean:
	rm -rf dist web/dist/assets web/dist/index.html web/dist/favicon.svg
