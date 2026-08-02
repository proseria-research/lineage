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
