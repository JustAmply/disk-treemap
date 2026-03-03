# Build stage
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN go mod download \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=mod -trimpath -ldflags="-s -w" -o /out/disk-treemap ./cmd/server \
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
