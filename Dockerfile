# Build stage
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN go mod download \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=mod -trimpath -ldflags="-s -w" -o /out/disk-treemap ./cmd/server \
    && mkdir -p /out/data

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build --chown=65532:65532 /out/disk-treemap /app/disk-treemap
COPY --chown=65532:65532 web /app/web
COPY --from=build --chown=65532:65532 /out/data /data

ENV LISTEN_ADDR=:8080 \
    DATA_DIR=/data

EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/disk-treemap"]
