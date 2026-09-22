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

# Directories that participate in drift-check. Each regenerates into a local
# ./velox/ output that's gitignored — so `git diff` alone can't detect when the
# generator emits broken code. `go build ./...` inside each one exercises the
# full generated surface and surfaces type/import drift that the diff-only path
# misses.
#
# Most of these are their own Go module. examples/realworld is NOT — it has no
# go.mod, so its gitignored velox/ output belongs to the ROOT module. That makes
# omitting it especially bad: its stale generated code is compiled by every root
# `go build ./...`, `go test ./...`, `golangci-lint run` and coverage run, while
# nothing regenerates it. It sat several generator fixes behind before being
# added here. CI generates it explicitly (.github/workflows/ci.yml) — this list
# is what makes a local regen match CI.
DRIFT_CHECK_MODULES=(
    examples/basic
    examples/edge-schema
    examples/fullgql
    examples/fulltest
    examples/globalid
    examples/json-field
    examples/multitenant
    examples/realworld
    examples/tree
    examples/versioned-migration
    tests/external-module
    tests/parity
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
    # Build targets: most modules build the whole module (./...). tests/parity
    # generates two separate ORM clients (velox/ and ent/) and additionally
    # carries a compile-gate test that re-runs generation, so build only the
    # generated client packages there.
    BUILD_TARGETS=(./...)
    if [[ "${dir}" == "tests/parity" ]]; then
        BUILD_TARGETS=(./velox/... ./ent/...)
    fi
    echo "==> building ${dir}"
    if ! (cd "${dir}" && go build "${BUILD_TARGETS[@]}"); then
        echo "warning: ${dir} build failed after regen" >&2
        FAILED_EXAMPLES+=("${dir}")
    fi
done

echo "==> formatting"
# Format only velox-owned code. .references/ent and .references/ent-contrib
# are upstream read-only reference checkouts; recursive gofmt on "." would
# silently rewrite their files and cause ghost "modified content" drift.
# testdata/ is pruned too: golden files pin generator output byte-for-byte
# and their import paths (github.com/test/project/...) do not resolve, so
# goimports would strip the imports it cannot find and corrupt the pins.
# Classify once, in a single pass: generated files carry the marker in
# their first lines (bounded to 3 — a generator SOURCE may mention the
# marker string further down). gofmt and goimports then run only over the
# handwritten set; generator output is written already formatted and must
# never be goimports'ed (see above). One awk process instead of a fork
# pair per file — the per-file loop this replaces cost ~3 minutes.
HANDWRITTEN=()
while IFS= read -r path; do
    [[ -n "${path}" ]] && HANDWRITTEN+=("${path}")
done < <(find . \
    -type d \( -name .references -o -name .git -o -name node_modules -o -name testdata \) -prune \
    -o -type f -name '*.go' -print0 \
    | xargs -0 awk '
        FNR==1 { if (prev != "" && !gen) print prev; prev = FILENAME; gen = 0 }
        FNR<=3 && /Code generated .* DO NOT EDIT/ { gen = 1 }
        FNR>3 { next }
        END { if (prev != "" && !gen) print prev }')
if [[ ${#HANDWRITTEN[@]} -eq 0 ]]; then
    # The classifier runs in a process substitution, so a failure there would
    # otherwise leave the array empty and skip formatting while still
    # reporting success.
    echo "error: no handwritten Go files classified; the awk pass failed" >&2
    exit 1
fi
if [[ ${#HANDWRITTEN[@]} -gt 0 ]]; then
    gofmt -s -w "${HANDWRITTEN[@]}"
    if command -v goimports >/dev/null 2>&1; then
        goimports -w "${HANDWRITTEN[@]}"
    else
        echo "warning: goimports not installed; skipping import ordering" >&2
    fi
fi

# The builds above ran BEFORE formatting; a formatting pass that corrupts
# generated output would otherwise leave every module broken while the
# script still reports success. Cached rebuilds are cheap.
echo "==> verifying builds after formatting"
for dir in "${DRIFT_CHECK_MODULES[@]}"; do
    [[ -f "${dir}/generate.go" ]] || continue
    BUILD_TARGETS=(./...)
    if [[ "${dir}" == "tests/parity" ]]; then
        BUILD_TARGETS=(./velox/... ./ent/...)
    fi
    if ! (cd "${dir}" && go build "${BUILD_TARGETS[@]}"); then
        echo "warning: ${dir} build failed after formatting" >&2
        FAILED_EXAMPLES+=("${dir}")
    fi
done

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
