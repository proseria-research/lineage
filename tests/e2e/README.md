# Server E2E harness

The harness builds and starts the real `lineage` binary, waits for all three HTTP
surfaces, exercises a production-style workflow, restarts against the same SQLite and
filesystem state, and verifies graceful shutdown.

```sh
make test-e2e
```

The suite is isolated behind the `e2e` build tag, so `go test ./...` remains fast. To
exercise an already-built binary, set `LINEAGE_E2E_BINARY`:

```sh
LINEAGE_E2E_BINARY=./bin/lineage go test -tags=e2e ./tests/e2e -count=1 -v
```

Process output is captured and printed when startup, requests, or shutdown fail. Each
run uses temporary ports, a temporary SQLite database, and a temporary artifact root.

The hermetic suite covers:

- concurrent production promotion and the singleton invariant;
- upload rejection cleanup and retry;
- resolve caching, conditional requests, byte ranges, and restart durability;
- idempotent migrations and startup failures;
- lineage, deployment, insight, admin-BFF, and audit persistence;
- readiness under storage failure, GC prefix safety, and in-flight graceful shutdown.

## PostgreSQL and S3

The production-backend scenario runs when both backend endpoints are configured. CI starts
PostgreSQL and MinIO and treats this scenario as a required job. To run it locally:

```sh
LINEAGE_E2E_POSTGRES_DSN='postgres://lineage:lineage@127.0.0.1:5432/lineage?sslmode=disable' \
LINEAGE_E2E_S3_ENDPOINT=http://127.0.0.1:9000 \
LINEAGE_E2E_S3_BUCKET=lineage-e2e \
LINEAGE_E2E_S3_KEY=minioadmin \
LINEAGE_E2E_S3_SECRET=minioadmin \
LINEAGE_E2E_S3_PATH_STYLE=true \
go test -tags=e2e ./tests/e2e -run TestPostgresS3ProcessWorkflow -count=1 -v
```
