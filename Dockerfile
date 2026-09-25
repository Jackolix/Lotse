# syntax=docker/dockerfile:1

# All build stages run on the build machine's platform and cross-compile, so a
# multi-arch image needs no emulation.

# ---- web UI ----
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- Go sources ----
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS src
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ENV CGO_ENABLED=0 LDFLAGS="-s -w -X github.com/Jackolix/Lotse/internal/version.Version=${VERSION}"

# ---- agents for every platform (independent of the image platform, built once) ----
FROM src AS agents
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build <<EOF
set -e
mkdir -p /out/agents
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}; arch=${target#*/}; ext=""
  [ "$os" = windows ] && ext=".exe"
  GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o /out/agents/lotse-agent-$os-$arch$ext ./cmd/agent
done
# The hub serves these gzipped (or unpacks on the fly), keeping the image small.
gzip -9 /out/agents/*
EOF

# ---- hub for the image platform ----
FROM src AS hub
COPY --from=web /src/web/dist ./web/dist
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "$LDFLAGS" -o /out/hub ./cmd/hub && \
    mkdir -p /out/data

# ---- runtime: static binary on distroless, no shell ----
# Root variant: the hub starts as root and drops privileges itself (see below).
FROM gcr.io/distroless/static-debian13
LABEL org.opencontainers.image.title="Lotse" \
      org.opencontainers.image.description="Self-hosted monitoring hub with remote shell and Wake-on-LAN" \
      org.opencontainers.image.source="https://github.com/Jackolix/Lotse"
COPY --from=hub /out/hub /app/hub
COPY --from=agents /out/agents /app/agents
COPY --from=hub --chown=65532:65532 /out/data /data
ENV HUB_ADDR=:8090 HUB_DATA_DIR=/data HUB_AGENT_DIR=/app/agents
EXPOSE 8090
VOLUME /data
# The hub starts as root only to take ownership of /data (bind mounts are often
# root-owned), then switches to PUID:PGID, by default 65532 ("nonroot").
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/app/hub", "healthcheck"]
ENTRYPOINT ["/app/hub"]
