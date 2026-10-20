# syntax=docker/dockerfile:1.7
# ---------- 1. build the web UI ----------
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 2. build the Go binary ----------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/pocket-core ./third_party/pocket-core
RUN cd third_party/pocket-core && sha256sum -c SHA256SUMS
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pocket-subtitles ./cmd/pocket-subtitles

# ---------- 3. alass (subtitle-to-subtitle aligner, Rust) ----------
FROM rust:1-slim-bookworm AS alass
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    cargo install --locked alass-cli --version 2.0.0 --root /out

# ---------- 4. Python engine dependencies ----------
FROM python:3.12-slim-bookworm AS pydeps
COPY engine/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir --prefix=/install -r /tmp/requirements.txt

# ---------- 5. runtime ----------
FROM python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg curl ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 1000 app && useradd -u 1000 -g app -d /data -M app
COPY --from=pydeps /install /usr/local
COPY --from=alass /out/bin/alass-cli /usr/local/bin/alass-cli
COPY --from=build /out/pocket-subtitles /usr/local/bin/pocket-subtitles
COPY engine/pocket_sync.py /app/engine/pocket_sync.py
ENV DATA_DIR=/data PORT=8080 HOST=0.0.0.0 MEDIA_DIRS=/media \
    ENGINE_PYTHON=python3 ENGINE_SCRIPT=/app/engine/pocket_sync.py ALASS_BIN=/usr/local/bin/alass-cli \
    PYTHONUNBUFFERED=1 PYTHONDONTWRITEBYTECODE=1
VOLUME ["/data"]
EXPOSE 8080
USER app
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD curl -fsS http://localhost:8080/healthz || exit 1
LABEL org.opencontainers.image.title="Pocket Subtitles" \
      org.opencontainers.image.description="Keeps a media library's subtitles in sync with the audio: re-times every Bazarr download, sweeps the library nightly, nudge any file by hand." \
      org.opencontainers.image.source="https://github.com/abjelosevic88/pocket-homelab-subtitles" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["pocket-subtitles"]
