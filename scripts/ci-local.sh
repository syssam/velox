#!/usr/bin/env bash
# Replay every CI job locally in the same order GitHub Actions does,
# so `scripts/ci-local.sh` gives you the same pass/fail verdict before
# you push. Driven by repeated push-observe-fix cycles chasing CI-only
# failures — anything the live ci.yml does here first.
#
# Skipped vs. CI (by design):
#   codeql         — requires GitHub Advanced Security for private repos.
#   apidiff        — only runs on pull_request; no PR context locally.
#   test-integration — needs Postgres + MySQL service containers. Provide
#                    VELOX_TEST_POSTGRES / VELOX_TEST_MYSQL env vars and
#                    pass --integration to run.
#   fuzz           — 20 minutes total; pass --fuzz to include.
#   macos matrix   — CI-only.
#
# Usage:
#   scripts/ci-local.sh              Run the fast jobs.
#   scripts/ci-local.sh --integration  Also run live-DB integration tests.
#   scripts/ci-local.sh --fuzz       Also run the full 10x 2min fuzz suite.
#   scripts/ci-local.sh --all        Run everything.

set -euo pipefail

if [[ ! -f go.mod ]] || ! grep -q 'module github.com/syssam/velox' go.mod; then
    echo "error: must be run from velox repository root" >&2
    exit 2
fi

RUN_INTEGRATION=0
RUN_FUZZ=0
for arg in "$@"; do
    case "${arg}" in
        --integration) RUN_INTEGRATION=1 ;;
        --fuzz) RUN_FUZZ=1 ;;
        --all) RUN_INTEGRATION=1; RUN_FUZZ=1 ;;
        *) echo "unknown flag: ${arg}" >&2; exit 2 ;;
    esac
done

FAILED=()
record_fail() { FAILED+=("$1"); echo "❌ $1" >&2; }
record_pass() { echo "✅ $1"; }

generate_root_fixtures() {
    # Mirrors the "Generate fixtures (root module)" step that runs in
    # every job which compiles the full module.
    go run tests/integration/generate.go
    (cd examples/realworld && go run generate.go)
}

# ---------- test job ----------
echo
echo "==> test job"
generate_root_fixtures
if go test -race -cover -coverprofile=coverage.out ./... >/tmp/ci-local-test.log 2>&1; then
    record_pass "test"
else
    tail -20 /tmp/ci-local-test.log
    record_fail "test"
fi

# ---------- coverage gates (mirror the ci.yml awk parser) ----------
echo
echo "==> coverage gates"
check_pkg() {
    local pkg="$1" threshold="$2"
    local cov
    cov=$(awk -v p="${pkg}" '
        NR > 1 && index($1, p "/") == 1 {
            stmts = $(NF-1); hits = $NF
            total += stmts; if (hits > 0) covered += stmts
        }
        END { if (total > 0) printf "%.1f", covered / total * 100 }
    ' coverage.out 2>/dev/null || true)
    if [[ -z "${cov}" ]]; then
        echo "  ${pkg}: (no data)"
        return
    fi
    local verdict
    verdict=$(awk -v c="${cov}" -v t="${threshold}" 'BEGIN { print (c >= t) ? "ok" : "FAIL" }')
    printf "  %-55s %6s%% / %s%%  %s\n" "${pkg}" "${cov}" "${threshold}" "${verdict}"
    if [[ "${verdict}" == "FAIL" ]]; then
        record_fail "coverage:${pkg}"
    fi
}
if [[ -f coverage.out ]]; then
    check_pkg "github.com/syssam/velox/privacy" 90
    check_pkg "github.com/syssam/velox/dialect/sql" 85
    check_pkg "github.com/syssam/velox/compiler/gen/sql" 85
    check_pkg "github.com/syssam/velox/runtime" 80
    check_pkg "github.com/syssam/velox/dialect" 85
    check_pkg "github.com/syssam/velox/compiler/gen" 80
    check_pkg "github.com/syssam/velox/contrib/graphql" 80
    check_pkg "github.com/syssam/velox/dialect/sql/schema" 80
    check_pkg "github.com/syssam/velox/dialect/sql/sqlgraph" 80
    check_pkg "github.com/syssam/velox/compiler/load" 75
fi

# ---------- build job ----------
echo
echo "==> build job"
if CGO_ENABLED=0 go build ./... 2>/tmp/ci-local-build.log; then
    record_pass "build"
else
    cat /tmp/ci-local-build.log
    record_fail "build"
fi

# ---------- lint job ----------
echo
echo "==> lint job"
if command -v golangci-lint >/dev/null 2>&1; then
    if golangci-lint run >/tmp/ci-local-lint.log 2>&1; then
        record_pass "lint"
    else
        tail -30 /tmp/ci-local-lint.log
        record_fail "lint"
    fi
else
    echo "  golangci-lint not installed — skipping"
fi

# ---------- drift-check job ----------
echo
echo "==> drift-check job"
if bash scripts/regen.sh --check >/tmp/ci-local-drift.log 2>&1; then
    record_pass "drift-check"
else
    tail -10 /tmp/ci-local-drift.log
    record_fail "drift-check"
fi

# ---------- security job ----------
echo
echo "==> security job (govulncheck)"
if command -v govulncheck >/dev/null 2>&1; then
    if govulncheck ./... >/tmp/ci-local-vuln.log 2>&1; then
        record_pass "security"
    else
        tail -20 /tmp/ci-local-vuln.log
        record_fail "security"
    fi
else
    echo "  govulncheck not installed — skipping (install: go install golang.org/x/vuln/cmd/govulncheck@latest)"
fi

# ---------- examples matrix (mode=test entries only) ----------
echo
echo "==> examples (basic, fullgql, integration-test)"
for ex in basic fullgql integration-test; do
    if (cd "examples/${ex}" && go run generate.go >/dev/null 2>&1 && go test -race ./... >/tmp/ci-local-ex-${ex}.log 2>&1); then
        record_pass "examples:${ex}"
    else
        tail -15 "/tmp/ci-local-ex-${ex}.log" 2>/dev/null || true
        record_fail "examples:${ex}"
    fi
done

# ---------- benchmark job ----------
echo
echo "==> benchmark job (smoke, count=1 not 5 for speed)"
if go test -bench=. -benchmem -count=1 -timeout=5m ./dialect/sql/ ./compiler/gen/ ./privacy/ >/tmp/ci-local-bench.log 2>&1; then
    record_pass "benchmark"
else
    tail -15 /tmp/ci-local-bench.log
    record_fail "benchmark"
fi

# ---------- optional: fuzz (long) ----------
if [[ ${RUN_FUZZ} -eq 1 ]]; then
    echo
    echo "==> fuzz job (2 min × 10 targets = 20 min)"
    targets=(
        '^FuzzQuote$:./dialect/sql/'
        '^FuzzPredicateEQ$:./dialect/sql/'
        '^FuzzPredicateLike$:./dialect/sql/'
        '^FuzzPredicateIn$:./dialect/sql/'
        '^FuzzSelectBuilder$:./dialect/sql/'
        '^FuzzEscapeStringValue$:./dialect/sql/'
        '^FuzzPascal$:./compiler/gen/'
        '^FuzzSnake$:./compiler/gen/'
        '^FuzzCamel$:./compiler/gen/'
        '^FuzzPascalSnakeRoundTrip$:./compiler/gen/'
    )
    for t in "${targets[@]}"; do
        name="${t%:*}"; pkg="${t#*:}"
        if go test -fuzz="${name}" -fuzztime=2m "${pkg}" >/tmp/ci-local-fuzz.log 2>&1; then
            record_pass "fuzz:${name}"
        else
            tail -10 /tmp/ci-local-fuzz.log
            record_fail "fuzz:${name}"
        fi
    done
else
    echo
    echo "==> fuzz job (skipped — pass --fuzz to run)"
fi

# ---------- optional: test-integration (needs live DBs) ----------
if [[ ${RUN_INTEGRATION} -eq 1 ]]; then
    echo
    echo "==> test-integration (requires VELOX_TEST_POSTGRES / VELOX_TEST_MYSQL)"
    if go test -tags integration -race -v ./dialect/sql/schema/ -run "Test(Postgres|MySQL|MultiDialect)" >/tmp/ci-local-integ.log 2>&1; then
        record_pass "test-integration:schema"
    else
        tail -20 /tmp/ci-local-integ.log
        record_fail "test-integration:schema"
    fi
    if go test -race -v ./tests/integration/ -run "Test(Postgres|MySQL)Helper" >/tmp/ci-local-integ2.log 2>&1; then
        record_pass "test-integration:e2e"
    else
        tail -20 /tmp/ci-local-integ2.log
        record_fail "test-integration:e2e"
    fi
else
    echo
    echo "==> test-integration (skipped — pass --integration with DBs up)"
fi

# ---------- summary ----------
echo
if [[ ${#FAILED[@]} -eq 0 ]]; then
    echo "==================================================="
    echo "  ci-local: ALL PASS — safe to push"
    echo "==================================================="
    exit 0
fi
echo "==================================================="
echo "  ci-local: ${#FAILED[@]} failure(s):"
for f in "${FAILED[@]}"; do echo "    - ${f}"; done
echo "==================================================="
exit 1
