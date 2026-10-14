VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test race vet fuzz bench
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ghok ./cmd/ghok
test:
	go test ./...
race:
	go test -race ./...
vet:
	go vet ./...
fuzz:
	go test -run '^$$' -fuzz FuzzParse -fuzztime 30s ./internal/normalize/apiargs/
bench:
	go test -run '^$$' -bench . -benchmem ./...
