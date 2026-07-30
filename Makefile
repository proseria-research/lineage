.PHONY: build run test fmt vet tidy clean web web-dev docker helm-lint

BIN := bin/lineage
WEB := internal/api/adminui/web
CHART := deploy/helm/lineage
IMAGE ?= ghcr.io/proseria-research/lineage:dev

# web builds the Admin console (Vite/React) into web/dist, which the Go binary embeds (§06).
# The committed dist means `go build` works without Node; run this after changing the console.
web:
	cd $(WEB) && pnpm install --frozen-lockfile && pnpm build

web-dev:
	cd $(WEB) && pnpm dev

build:
	go build -o $(BIN) ./cmd/lineage

run: build
	./$(BIN)

test:
	go test ./...

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
