# Multi-stage build for PassCtrl

# Build stage
FROM golang:1.25-bookworm AS builder

WORKDIR /src

# Cache go modules first
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy source
COPY . .

# Build static-ish binary (CGO disabled for smaller runtime)
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -o /out/passctrl ./cmd


# Runtime stage
FROM debian:bookworm-slim

ENV TZ=UTC \
    PUID=1000 \
    PGID=1000 \
    PASSCTRL_SERVER__LISTEN__ADDRESS=0.0.0.0 \
    PASSCTRL_SERVER__LISTEN__PORT=8080

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata gosu passwd \
    && rm -rf /var/lib/apt/lists/*

# App directories that may be mounted from host
WORKDIR /app
RUN mkdir -p /config /rules /templates \
    && groupadd -g 1000 app \
    && useradd -u 1000 -g 1000 -m -d /home/app app \
    && chown -R app:app /config /rules /templates

# Copy binary and entrypoint
COPY --from=builder /out/passctrl /usr/local/bin/passctrl
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080/tcp

VOLUME ["/config", "/rules", "/templates"]

ENTRYPOINT ["/entrypoint.sh"]
CMD ["passctrl"]
