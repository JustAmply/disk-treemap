# syntax=docker/dockerfile:1.7

# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine AS build
WORKDIR /src

ARG TARGETOS=linux
ARG TARGETARCH=amd64

ENV CGO_ENABLED=0 \
    GOFLAGS=-trimpath

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /out/disk-treemap ./cmd/server \
    && mkdir -p /out/data

# Runtime stage (root user for broader scan read permissions)
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/disk-treemap /app/disk-treemap
COPY web /app/web
COPY --from=build /out/data /data

USER 0:0

ENV LISTEN_ADDR=:8080 \
    DATA_DIR=/data

EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/disk-treemap"]
