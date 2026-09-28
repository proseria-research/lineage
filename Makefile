.PHONY: build run seed reset test test-console test-e2e fmt vet tidy clean web web-dev docker docker-init helm-lint sdk sdk-check cli \
        site site-dev site-preview site-check site-links site-deploy

BIN := bin/lineage
CLI_BIN := bin/lineage-cli
WEB := internal/api/adminui/web
SITE := site
CHART := deploy/helm/lineage
IMAGE ?= ghcr.io/proseria-research/lineage:dev
INIT_IMAGE ?= ghcr.io/proseria-research/lineage-init:dev

# What `make run` configures, matching the chart's defaults (§19.4) so a local registry
# behaves like a deployed one — evidence integrity included. Without these the binary's own
# defaults apply, which leave the retention floor at 0 and the console honestly reporting
# that there is none.
#
# A 3650-day floor refuses deletion of anything younger, and **nothing overrides it** — not
# ?force=true, which exists for the production-version guard and must not double as a way
# past a retention obligation. Starting over is `make reset`, not a batch of DELETEs.
RUN_ENV := LINEAGE_RETENTION_MIN_ARCHIVED_VERSION_DAYS=3650 \
           LINEAGE_RETENTION_MIN_AUDIT_AGE_DAYS=3650 \
           LINEAGE_AUDIT_ATTESTATION=on \
           LINEAGE_SEAL_INTERVAL_SECONDS=60 \
           LINEAGE_SEAL_GRACE_SECONDS=5

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
	$(RUN_ENV) ./$(BIN)

# seed loads the demo dataset into a *running* registry via the Model API. Point it elsewhere
# with LINEAGE_ENDPOINT. Seeding is additive and never deletes; to re-seed, stop the registry,
# `make reset`, and start again.
seed:
	go run ./cmd/lineage-seed $(SEED_FLAGS)

# reset clears local registry state — the SQLite database and stored artifacts — so the next
# `make run` starts empty.
#
# This is the answer to "how do I start over" on an install with a retention floor, and the
# only one: deletion refuses by design (§19.4), and a fixture loader is not a reason to reach
# around that. **Stop the registry first** — unlinking the database out from under a running
# process leaves it writing to an inode nobody can see.
reset:
	rm -rf data lineage.db

# test runs the default (stub) build; the console-serving test skips. Use test-console for it.
test:
	go test ./...

test-console: web
	go test -tags console ./...

# test-e2e builds and launches the real binary, probes all three server surfaces, exercises
# a full publish-to-resolve workflow, restarts it against durable state, and shuts it down.
test-e2e:
	go test -tags=e2e ./tests/e2e -count=1 -timeout=5m -v

fmt:
	gofmt -w internal cmd

vet:
	go vet ./...

tidy:
	go mod tidy

# docker builds the registry image (console + static binary → distroless nonroot).
docker:
	docker build -t $(IMAGE) .

# docker-init builds the KServe storage-initializer image. CI publishes both; this is the
# local equivalent of one matrix leg.
docker-init:
	docker build -f Dockerfile.init -t $(INIT_IMAGE) .

# helm-lint validates the chart against both profiles.
helm-lint:
	helm lint $(CHART) -f $(CHART)/values-dev.yaml
	helm lint $(CHART) -f $(CHART)/values-prod.yaml --set database.postgres.dsnSecret.name=x

# sdk regenerates the low-level OpenAPI manifest consumed by the Python ergonomic layer.
sdk:
	python3 sdk/generate.py

sdk-check: sdk
	python3 -m py_compile sdk/python/lineage/*.py

# cli builds the command-line client next to the server binary, never over it.
cli:
	go build -o $(CLI_BIN) ./cmd/lineage-cli

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
