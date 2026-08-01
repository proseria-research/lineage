.PHONY: build run seed test test-console fmt vet tidy clean web web-dev docker helm-lint sdk sdk-check cli \
        site site-dev site-preview site-check site-links site-deploy

BIN := bin/lineage
WEB := internal/api/adminui/web
SITE := site
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

# sdk regenerates the low-level OpenAPI manifest consumed by the Python ergonomic layer.
sdk:
	python3 sdk/generate.py

sdk-check: sdk
	python3 -m py_compile sdk/python/lineage/*.py

cli:
	go build -o $(BIN) ./cmd/lineage-cli

# ---- Public website & guides (site/) ----------------------------------------
# Astro + Starlight, deployed to Cloudflare as static assets. Independent of the Go
# build: nothing here is needed to compile, test, or ship the binary.

# site produces the static build in site/dist.
site:
	cd $(SITE) && pnpm install --frozen-lockfile && pnpm build

# site-dev serves with hot reload. Search is empty here — Pagefind only indexes at
# build time, so use site-preview to exercise it.
site-dev:
	cd $(SITE) && pnpm dev

# site-preview serves the built output, which is what Cloudflare will actually serve.
site-preview: site
	cd $(SITE) && pnpm preview

# site-check type-checks the Astro components.
site-check:
	cd $(SITE) && pnpm exec astro check

# site-links resolves every internal href against the built output. Pure filesystem —
# no server, no port to collide with. Paths carrying a dot or underscore (assets) are
# excluded by the pattern.
site-links: site
	@cd $(SITE) && broken=$$(grep -rhoE 'href="/[a-z0-9/-]*"' dist --include='*.html' \
	    | sed 's/href="//;s/"//' | sort -u \
	    | while read -r p; do \
	        f="dist$${p%/}/index.html"; \
	        [ -f "$$f" ] || echo "  $$p"; \
	      done); \
	  if [ -n "$$broken" ]; then echo "broken internal links:"; echo "$$broken"; exit 1; fi; \
	  echo "internal links ok"

# site-deploy publishes to Cloudflare. Needs wrangler to be authenticated.
site-deploy: site
	cd $(SITE) && pnpm exec wrangler deploy

clean:
	rm -rf bin data lineage.db $(SITE)/dist $(SITE)/.astro
