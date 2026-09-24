#!/usr/bin/env bash
# Run the local CI against throwaway databases that match the CI
# test-integration matrix, with the Go version CI uses.
#
#   scripts/ci-docker.sh          full run: every ci-local job (incl. fuzz) on
#                                 the first database pair, then the DB jobs on
#                                 the second (~45 min)
#   scripts/ci-docker.sh --db     DB jobs only (integration + parity) on every
#                                 pair (~10 min)
#
# Why: a reused local database collects tables from other schemas (the parity
# harness once broke the integration migration that way), local database
# versions drift from CI's, and a newer local Go breaks tools built for CI's.
# Each pair gets fresh tmpfs-backed containers on ports 55432/53306, torn
# down afterwards even on failure.
#
# Environment:
#   CI_GO          Go toolchain to run (default: newest release of the minor
#                  version ci.yml pins; "local" uses the installed Go)
#   CI_PG_PORT     host port for Postgres (default 55432)
#   CI_MYSQL_PORT  host port for MySQL (default 53306)

set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "${root}"

MODE=full
for arg in "$@"; do
    case "${arg}" in
        --db) MODE=db ;;
        *) echo "unknown flag: ${arg}" >&2; exit 2 ;;
    esac
done

# Keep in step with the test-integration matrix in .github/workflows/ci.yml.
PAIRS=("16 8.0" "14 5.7")

export CI_PG_PORT="${CI_PG_PORT:-55432}"
export CI_MYSQL_PORT="${CI_MYSQL_PORT:-53306}"
COMPOSE=(docker compose -f docker-compose.ci.yml)

# resolve_go prints the toolchain to use. ci.yml pins a minor version
# ('1.26'), which setup-go resolves to its newest patch release; do the same.
resolve_go() {
    if [[ -n "${CI_GO:-}" ]]; then
        echo "${CI_GO}"
        return
    fi
    local minor latest
    minor="$(grep -oE "go-version: '1\.[0-9]+'" .github/workflows/ci.yml | sort | uniq -c | sort -rn | head -1 | grep -oE "1\.[0-9]+")"
    latest="$(curl -fsS 'https://go.dev/dl/?mode=json&include=all' 2>/dev/null |
        grep -oE "\"go${minor//./\\.}\.[0-9]+\"" | tr -d '"' | sort -t. -k3 -n | tail -1 || true)"
    if [[ -z "${latest}" ]]; then
        echo "warning: could not resolve the newest go${minor}.x; using the local toolchain" >&2
        echo "local"
        return
    fi
    echo "${latest}"
}

native_platform() {
    case "$(uname -m)" in
        arm64|aarch64) echo "linux/arm64" ;;
        *) echo "linux/amd64" ;;
    esac
}

down() { "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true; }
trap down EXIT

GOTOOLCHAIN="$(resolve_go)"
export GOTOOLCHAIN
echo "==> Go toolchain: ${GOTOOLCHAIN} ($(go version | awk '{print $3}'))"

FAILED=()
first=1
for pair in "${PAIRS[@]}"; do
    read -r pg my <<<"${pair}"
    export CI_PG_VERSION="${pg}" CI_MYSQL_VERSION="${my}"
    if [[ "${my}" == 5.* ]]; then
        export CI_MYSQL_PLATFORM="linux/amd64"
    else
        CI_MYSQL_PLATFORM="$(native_platform)"
        export CI_MYSQL_PLATFORM
    fi

    echo
    echo "==================================================="
    echo "  Postgres ${pg} + MySQL ${my}"
    echo "==================================================="
    down
    "${COMPOSE[@]}" up -d --wait --quiet-pull
    # CI gives the parity harness its own database on its own services.
    "${COMPOSE[@]}" exec -T postgres psql -U postgres -qc "CREATE DATABASE parity" >/dev/null
    "${COMPOSE[@]}" exec -T mysql mysql -uroot -ptest -e "CREATE DATABASE parity" 2>/dev/null

    flag="--db-only"
    if [[ "${MODE}" == full && ${first} -eq 1 ]]; then
        flag="--all"
    fi
    first=0
    if VELOX_TEST_POSTGRES="host=localhost port=${CI_PG_PORT} user=postgres password=test dbname=velox_test sslmode=disable" \
        VELOX_TEST_MYSQL="root:test@tcp(localhost:${CI_MYSQL_PORT})/velox_test?parseTime=true&multiStatements=true" \
        scripts/ci-local.sh "${flag}"; then
        :
    else
        FAILED+=("pg${pg}+mysql${my}")
    fi
    down
done

echo
if [[ ${#FAILED[@]} -eq 0 ]]; then
    echo "ci-docker: ALL PASS on every database pair"
    exit 0
fi
echo "ci-docker: failures on: ${FAILED[*]}"
exit 1
