.PHONY: build run test fmt vet tidy clean

BIN := bin/lineage

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
