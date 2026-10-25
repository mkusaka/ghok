VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?=
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build test race lint fuzz bench schema fmt cross ci
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ghok ./cmd/ghok
fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then printf '%s\n' "$$out"; exit 1; fi
CROSS_DIR ?= dist/cross
cross:
	@set -e; for os in darwin linux; do for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -o "$(CROSS_DIR)/ghok_$${os}_$${arch}" ./cmd/ghok; \
	done; done
# Everything CI checks, as CI runs it (scripts/push-main.sh runs this).
ci:
	$(MAKE) fmt
	GOFLAGS=-p=1 $(MAKE) test race
	$(MAKE) fuzz FUZZTIME=200000x
	$(MAKE) build cross
	GOOS=linux CGO_ENABLED=0 $(MAKE) lint
	GOOS=darwin CGO_ENABLED=0 $(MAKE) lint
test:
	go test ./...
race:
	go test -race ./...
# The version CI pins (renovate updates ci.yml), built with the local Go.
GOLANGCI_LINT_VERSION = $(shell sed -n 's/^ *version: \(v2\.[0-9.]*\)$$/\1/p' .github/workflows/ci.yml | head -1)
lint:
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "lint: no golangci-lint version in .github/workflows/ci.yml" >&2; exit 1; }
	@# Build for the host; GOOS/GOARCH then only select what gets analysed.
	env -u GOOS -u GOARCH GOBIN="$(CURDIR)/bin/golangci-lint-$(GOLANGCI_LINT_VERSION)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	"$(CURDIR)/bin/golangci-lint-$(GOLANGCI_LINT_VERSION)/golangci-lint" run ./...
# Go fuzzes one target per run. CI uses FUZZTIME=200000x.
# Name:pkg@N fixes N for filesystem-bound targets (~100-300 execs/s).
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
	FuzzRepositoryLess:./internal/policy \
	FuzzLayeredConfig:./internal/config@20000x \
	FuzzTrustLifecycle:./internal/config@5000x \
	FuzzTrustPathMatch:./internal/config@20000x

fuzz:
	@set -e; for t in $(FUZZ_TARGETS); do \
		pkg=$${t#*:}; time=$(FUZZTIME); \
		case $$pkg in *@*) time=$${pkg#*@}; pkg=$${pkg%@*};; esac; \
		echo "fuzz $${t%%:*}"; \
		go test -run '^$$' -fuzz "^$${t%%:*}$$" -fuzztime $$time $$pkg; \
	done
bench:
	go test -run '^$$' -bench . -benchmem ./...
schema:
	go generate ./internal/schema
