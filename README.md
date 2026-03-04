# Disk Treemap

Lightweight WizTree-like browser UI for disk usage analysis in Docker.

## Features

- On-demand scans only (no background indexing when idle)
- Live scan indicator (current path, scanned items, discovered bytes)
- Single active scan lock
- Root path constrained by `ANALYZE_ROOT`
- SQLite-backed scan snapshots (`/data/scan.db`)
- Scan history list with manual deletion and automatic retention cap
- Directory-level scan diff (growth/shrink/new/removed)
- Treemap UI + largest-items table + breadcrumb drilldown
- Search/filter/sort on children and largest views
- URL state for deep-linking (`scan`, `path`, `base_scan`, filters)
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
- `SCAN_HISTORY_MAX_RUNS=50` (retains newest completed/failed scans)

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
- `GET /api/v1/scans?limit=<n>&status=<queued|running|completed|failed>`
- `GET /api/v1/scans/{scan_id}`
- `DELETE /api/v1/scans/{scan_id}`
- `GET /api/v1/scans/{scan_id}/children?path=<absolute-path>&limit=<n>&q=<substring>&type=<file|dir>&min_size=<bytes>&sort=<size_desc|size_asc|name_asc|name_desc>`
- `GET /api/v1/scans/{scan_id}/largest?path=<absolute-path>&limit=<n>&q=<substring>&type=<file|dir>&min_size=<bytes>&sort=<size_desc|size_asc|name_asc|name_desc>`
- `GET /api/v1/scans/{target_scan_id}/diff?base_scan_id=<scan_id>&path=<absolute-path>&limit=<n>`

Diff response fields per directory item:

- `before_bytes`
- `after_bytes`
- `delta_bytes`
- `delta_percent`
- `change_class` (`new`, `grew`, `shrunk`, `removed`, `unchanged`)

Notes:

- `path` must be absolute and inside `ANALYZE_ROOT`.
- Children/largest sorting defaults to `size_desc`.
- Diff compares direct child directories at the requested path.

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
- Diff is directory-level in this version (not file-level)
