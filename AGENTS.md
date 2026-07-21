# Repository guidance

## Validation

- On Windows, run `./scripts/check.ps1` before handing off changes. If a check cannot run, report the skipped command and reason instead of claiming full validation.
- Changes to `Dockerfile`, `docker-compose*.yaml`, or `web/` require an actual image build. `docker compose config` validates configuration only and must not be reported as a successful image build.

## Filesystem path invariant

- Any new HTTP input that selects a filesystem path must pass through `pathutil.NormalizeWithinRoot` before store or filesystem access.
- Add a test that proves a path outside `ANALYZE_ROOT` is rejected for every new path-accepting endpoint.
