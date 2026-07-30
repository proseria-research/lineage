.PHONY: build run test fmt vet tidy clean web web-dev

BIN := bin/lineage
WEB := internal/api/adminui/web

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

clean:
	rm -rf bin data lineage.db
