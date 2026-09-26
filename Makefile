.PHONY: generate test lint check bench-hotpaths bench-baseline bench-compare bench-install ci-docker ci-docker-db

# CI's pinned linter version, read from ci.yml so the two cannot drift. A
# different local version reports zero issues on code CI rejects.
GOLANGCI_LINT_VERSION := $(shell sed -n 's/^ *version: *\(v2\.[0-9.]*\).*/\1/p' .github/workflows/ci.yml | head -1)

generate: ## Generate the gitignored fixtures the root module's tests import
	go run tests/integration/generate.go
	cd examples/realworld && go run generate.go

test: generate ## Generate fixtures, then run every test in the root module
	go test ./...

lint: ## Run golangci-lint at CI's pinned version
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "error: golangci-lint version not found in .github/workflows/ci.yml" >&2; exit 1; }
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

check: generate ## What CI's test job runs: race + coverage, then lint
	go test -race -cover ./...
	$(MAKE) lint

# Hot-path benchmarks guarded against regression.
# Keep this list short — it's meant to catch the failure modes that
# actually hurt (UpdateOne, CreateBulk), not to survey every allocation.
BENCH_HOTPATHS = '^BenchmarkUpdateOne_SQLite$$|^BenchmarkCreateBulk$$|^BenchmarkCreate_SingleRowLoop$$'
BENCH_PKG      = ./tests/integration/
BENCH_COUNT    = 10
BENCH_OUT      = testdata/bench-current.txt
BENCH_BASELINE = testdata/bench-baseline.txt

bench-install: ## Install benchstat (required for bench-compare)
	go install golang.org/x/perf/cmd/benchstat@latest

bench-hotpaths: ## Run hot-path benchmarks and write to testdata/bench-current.txt
	@mkdir -p testdata
	go test -run=^$$ -bench=$(BENCH_HOTPATHS) -benchmem -count=$(BENCH_COUNT) $(BENCH_PKG) | tee $(BENCH_OUT)

bench-baseline: ## Freeze current bench-current.txt as the new baseline (explicit action)
	@if [ ! -f $(BENCH_OUT) ]; then echo "error: run 'make bench-hotpaths' first" >&2; exit 1; fi
	cp $(BENCH_OUT) $(BENCH_BASELINE)
	@echo "baseline updated: $(BENCH_BASELINE)"
	@echo "commit testdata/bench-baseline.txt separately so the baseline bump is reviewable."

bench-compare: ## Compare bench-current.txt against bench-baseline.txt via benchstat
	@if ! command -v benchstat >/dev/null 2>&1; then echo "error: benchstat missing — run 'make bench-install'" >&2; exit 1; fi
	@if [ ! -f $(BENCH_BASELINE) ]; then echo "error: no baseline — run 'make bench-hotpaths' then 'make bench-baseline'" >&2; exit 1; fi
	@if [ ! -f $(BENCH_OUT) ]; then echo "error: no current run — run 'make bench-hotpaths' first" >&2; exit 1; fi
	benchstat $(BENCH_BASELINE) $(BENCH_OUT)

ci-docker: ## Full local CI on throwaway DBs matching the CI matrix, with CI's Go (~45 min)
	scripts/ci-docker.sh

ci-docker-db: ## Only the live-DB jobs (integration + parity) on every CI database pair (~10 min)
	scripts/ci-docker.sh --db

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
