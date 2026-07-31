.PHONY: build run seed test test-console fmt vet tidy clean web web-dev docker helm-lint

BIN := bin/lineage
WEB := internal/api/adminui/web
CHART := deploy/helm/lineage
IMAGE ?= ghcr.io/proseria-research/lineage:dev

# web builds the Admin console (Vite/React) into web/dist. dist is not committed; the binary
# embeds it only under the `console` build tag. Run this after changing the console.
web:
	cd $(WEB) && pnpm install --frozen-lockfile && pnpm build

web-dev:
	cd $(WEB) && pnpm dev

# build produces the full binary with the embedded console (builds the console first).
# A plain `go build ./cmd/lineage` (no tag) compiles a stub console and needs no assets.
build: web
	go build -tags console -o $(BIN) ./cmd/lineage

run: build
	./$(BIN)

# seed loads the demo dataset into a *running* registry via the Model API. Point it elsewhere
# with LINEAGE_ENDPOINT, and re-seed a dirty registry with `make seed SEED_FLAGS=-reset`.
seed:
	go run ./cmd/lineage-seed $(SEED_FLAGS)

# test runs the default (stub) build; the console-serving test skips. Use test-console for it.
test:
	go test ./...

test-console: web
	go test -tags console ./...

fmt:
	gofmt -w internal cmd

vet:
	go vet ./...

tidy:
	go mod tidy

# docker builds the single image (console + static binary → distroless nonroot).
docker:
	docker build -t $(IMAGE) .

# helm-lint validates the chart against both profiles.
helm-lint:
	helm lint $(CHART) -f $(CHART)/values-dev.yaml
	helm lint $(CHART) -f $(CHART)/values-prod.yaml --set database.postgres.dsnSecret.name=x

clean:
	rm -rf bin data lineage.db
