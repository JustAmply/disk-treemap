# Disk Treemap

Lightweight WizTree-like browser UI for disk usage analysis in Docker.

## Features

- On-demand scans only (no background indexing when idle)
- Live scan indicator (current path, scanned items, discovered bytes)
- Single active scan lock
- Root path constrained by `ANALYZE_ROOT`
- SQLite-backed scan snapshots (`/data/scan.db`)
- Treemap UI + largest-items table + breadcrumb drilldown
- Distroless runtime image
- Runs as root in-container for broader read access while scanning bind-mounted paths

## Container Configuration

Required:

- `ANALYZE_ROOT=/scanroot`

Optional:

- `LISTEN_ADDR=:8080`
- `DATA_DIR=/data`
- `SCAN_MAX_CONCURRENCY=4`
- `SCAN_WRITE_BATCH_SIZE=512`
- `SCAN_PROGRESS_INTERVAL_MS=200`
- `SCAN_TIMEOUT=0` (seconds, `0` disables timeout)
- `MAX_CHILDREN_PER_QUERY=500`

## Run with Docker

```bash
docker build -t disk-treemap:latest .

docker run --rm \
  --user 0:0 \
  -p 8080:8080 \
  -e ANALYZE_ROOT=/scanroot \
  -e DATA_DIR=/data \
  -v /host/path/to/analyze:/scanroot:ro \
  -v treemap-data:/data \
  disk-treemap:latest
```

Open `http://localhost:8080`.

## Compose

Update `/host/path/to/analyze` in `compose.yaml`, then:

```bash
docker compose up -d --build
```

## API

- `GET /api/v1/health`
- `GET /api/v1/config`
- `POST /api/v1/scans`
- `GET /api/v1/scans/{scan_id}` (includes `progress` while running: current path, scanned nodes/files/dirs, scanned bytes)
- `GET /api/v1/scans/{scan_id}/children?path=<absolute-path>&limit=<n>`
- `GET /api/v1/scans/{scan_id}/largest?path=<absolute-path>&limit=<n>`

Notes:

- `path` must be absolute and inside `ANALYZE_ROOT`.
- Children are sorted by `size_bytes DESC, name ASC`.

## Local Development

```bash
ANALYZE_ROOT=/tmp LISTEN_ADDR=:8080 DATA_DIR=.data go run ./cmd/server
```

Then open `http://localhost:8080`.

## Hardening Notes

The compose file includes:

- read-only container filesystem
- `/tmp` as tmpfs
- read-only bind mount for scan root

If you need stricter capabilities policy, test carefully: dropping all capabilities can reintroduce permission-denied errors even when running as root.

## Limitations

- No NTFS MFT-level acceleration (filesystem walk only)
- Symlinks are not followed
- External authentication is expected (reverse proxy)

