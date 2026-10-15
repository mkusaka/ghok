VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?=
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build test race lint fuzz bench schema
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ghok ./cmd/ghok
test:
	go test ./...
race:
	go test -race ./...
lint:
	golangci-lint run ./...
fuzz:
	go test -run '^$$' -fuzz FuzzParse -fuzztime 30s ./internal/normalize/apiargs/
bench:
	go test -run '^$$' -bench . -benchmem ./...
schema:
	go generate ./internal/schema
