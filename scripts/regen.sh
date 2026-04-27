#!/usr/bin/env bash
# Regenerate all codegen artifacts from source-of-truth schemas.
#
# Usage:
#   scripts/regen.sh           Regenerate in place.
#   scripts/regen.sh --check   Regenerate, then fail if the working tree differs.
#
# Run from repository root.

set -euo pipefail

if [[ ! -f go.mod ]] || ! grep -q 'module github.com/syssam/velox' go.mod; then
    echo "error: must be run from velox repository root" >&2
    exit 2
fi

CHECK_MODE=0
if [[ "${1:-}" == "--check" ]]; then
    CHECK_MODE=1
fi

echo "==> updating goldens (compiler/gen/sql)"
# Don't silence output — when this fails in CI, we need the test failure
# visible in the job log. Only the PASS line on success is noisy.
go test ./compiler/gen/sql/ -update-golden

echo "==> regenerating tests/integration fixtures"
go run tests/integration/generate.go

# Sub-modules that participate in drift-check. Each has its own go.mod
# and regenerates into a local ./velox/ output that's gitignored — so
# `git diff` alone can't detect when the generator emits broken code.
# `go build ./...` inside each sub-module exercises the full generated
# surface and surfaces type/import drift that the diff-only path misses.
DRIFT_CHECK_MODULES=(
    examples/basic
    examples/edge-schema
    examples/fullgql
    examples/fulltest
    examples/globalid
    examples/json-field
    examples/tree
    examples/versioned-migration
    tests/external-module
)
FAILED_EXAMPLES=()
for dir in "${DRIFT_CHECK_MODULES[@]}"; do
    if [[ ! -f "${dir}/generate.go" ]]; then
        echo "warning: ${dir}/generate.go missing, skipping" >&2
        continue
    fi
    echo "==> regenerating ${dir}"
    if ! (cd "${dir}" && go run generate.go); then
        echo "warning: ${dir} regen failed, continuing" >&2
        FAILED_EXAMPLES+=("${dir}")
        continue
    fi
    # gqlgen step: any module with a gqlgen.yml needs gqlgen to re-emit
    # generated.go after velox produces the schema.graphql + GoModel
    # autobind targets. Without this step, examples/fullgql ends up with
    # stale gqlgen output that won't compile against new layouts (e.g.,
    # the cycle-break refactor moved CreateXxxInput to client/{entity}/
    # — gqlgen autobind needs to be re-run to pick up the new path).
    if [[ -f "${dir}/gqlgen.yml" ]]; then
        echo "==> gqlgen generate ${dir}"
        if ! (cd "${dir}" && go run github.com/99designs/gqlgen generate); then
            echo "warning: ${dir} gqlgen failed, continuing" >&2
            FAILED_EXAMPLES+=("${dir}")
            continue
        fi
    fi
    echo "==> building ${dir}"
    if ! (cd "${dir}" && go build ./...); then
        echo "warning: ${dir} build failed after regen" >&2
        FAILED_EXAMPLES+=("${dir}")
    fi
done

echo "==> formatting"
# Format only velox-owned code. .references/ent and .references/ent-contrib
# are upstream read-only reference checkouts; recursive gofmt on "." would
# silently rewrite their files and cause ghost "modified content" drift.
FMT_TARGETS=()
while IFS= read -r -d '' path; do
    FMT_TARGETS+=("${path}")
done < <(find . \
    -type d \( -name .references -o -name .git -o -name node_modules \) -prune \
    -o -type f -name '*.go' -print0)
if [[ ${#FMT_TARGETS[@]} -gt 0 ]]; then
    gofmt -s -w "${FMT_TARGETS[@]}"
    if command -v goimports >/dev/null 2>&1; then
        goimports -w "${FMT_TARGETS[@]}"
    else
        echo "warning: goimports not installed; skipping import ordering" >&2
    fi
fi

if [[ ${CHECK_MODE} -eq 1 ]]; then
    # Ignore submodules: .references/ent and .references/ent-contrib are
    # upstream reference checkouts whose working-tree drift is independent
    # of velox codegen output. On CI submodules are freshly checked out so
    # this is a no-op there; locally it keeps --check usable.
    if ! git diff --quiet --ignore-submodules=all; then
        echo "error: regenerated artifacts differ from committed state" >&2
        echo "       run scripts/regen.sh locally and commit the result" >&2
        git diff --stat --ignore-submodules=all >&2
        exit 1
    fi
    echo "==> check passed: working tree clean after regen"
fi

if [[ ${#FAILED_EXAMPLES[@]} -gt 0 ]]; then
    echo "==> done (with failures: ${FAILED_EXAMPLES[*]})"
    exit 1
fi
echo "==> done"
