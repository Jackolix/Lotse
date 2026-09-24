# syntax=docker/dockerfile:1

# ---- web UI ----
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- hub + agents for every platform ----
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build <<EOF
set -e
LDFLAGS="-s -w -X github.com/Jackolix/Lotse/internal/version.Version=${VERSION}"
GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "$LDFLAGS" -o /out/hub ./cmd/hub
mkdir -p /out/agents /out/data
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}; arch=${target#*/}; ext=""
  [ "$os" = windows ] && ext=".exe"
  GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o /out/agents/lotse-agent-$os-$arch$ext ./cmd/agent
done
# The hub serves these gzipped (or unpacks on the fly), keeping the image small.
gzip -9 /out/agents/*
EOF

# ---- runtime: static binary on distroless, no shell, non-root ----
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=go /out/hub /app/hub
COPY --from=go /out/agents /app/agents
COPY --from=go --chown=nonroot:nonroot /out/data /data
ENV HUB_ADDR=:8090 HUB_DATA_DIR=/data HUB_AGENT_DIR=/app/agents
EXPOSE 8090
VOLUME /data
USER nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/app/hub", "healthcheck"]
ENTRYPOINT ["/app/hub"]
