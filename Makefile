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
# Go fuzzes one target per run. CI uses FUZZTIME=200000x.
FUZZTIME ?= 30s
FUZZ_TARGETS := \
	FuzzParse:./internal/normalize/apiargs \
	FuzzNormalize:./internal/normalize/native \
	FuzzNativeEditableBundlePreservesNeighbours:./internal/normalize/native \
	FuzzNormalizeREST:./internal/normalize/rest \
	FuzzNormalizeGraphQL:./internal/normalize/graphql \
	FuzzMutationVariableLocatorIsolation:./internal/normalize/graphql \
	FuzzRedactString:./internal/redact \
	FuzzTrustStore:./internal/config \
	FuzzContentMinimization:./internal/logging \
	FuzzRepositoryLess:./internal/policy

fuzz:
	@set -e; for t in $(FUZZ_TARGETS); do \
		echo "fuzz $${t%%:*}"; \
		go test -run '^$$' -fuzz "^$${t%%:*}$$" -fuzztime $(FUZZTIME) $${t#*:}; \
	done
bench:
	go test -run '^$$' -bench . -benchmem ./...
schema:
	go generate ./internal/schema
