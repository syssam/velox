#!/usr/bin/env bash
# Sweep pre-migration heavy files from velox leaf directories.
#
# When upgrading past the 2026-04-24 cycle-break refactor, per-entity
# heavy files (client.go, create.go, update.go, delete.go, mutation.go,
# runtime.go, filter.go, gql_mutation_input.go) move from {entity}/ into
# client/{entity}/. Velox's manifest-based cleanup only removes files
# listed in the PREVIOUS manifest, so files emitted by a pre-migration
# generator are never auto-removed on upgrade.
#
# This one-time sweep deletes the orphans.
#
# Usage:
#   scripts/sweep-cycle-break.sh [velox_dir]
#
# Examples:
#   scripts/sweep-cycle-break.sh              # defaults to ./velox
#   scripts/sweep-cycle-break.sh tests/integration
#
# See docs/migrations/2026-04-24-cycle-break.md for the full migration
# walkthrough.

set -euo pipefail

VELOX_DIR="${1:-velox}"

if [[ ! -d "$VELOX_DIR" ]]; then
    echo "error: directory not found: $VELOX_DIR" >&2
    echo "usage: $0 [velox_dir]" >&2
    exit 1
fi

# Leaf content we KEEP per {entity}/ subdir (everything else gets swept):
#   {entity}.go       — schema metadata (Label, FieldID, Columns, enum types)
#   where.go          — predicate helpers
#   gql_collection.go — GraphQL field-collection metadata (from contrib/graphql)
#   gql_node.go       — per-entity Relay Implementors list

# Known non-entity directories; never sweep their contents.
is_non_entity() {
    case "$1" in
        client|entity|query|predicate|filter|hook|intercept|internal|migrate|privacy|schema) return 0 ;;
        *) return 1 ;;
    esac
}

swept=0
for sub in "$VELOX_DIR"/*/; do
    [[ -d "$sub" ]] || continue
    name=$(basename "$sub")
    if is_non_entity "$name"; then
        continue
    fi

    while IFS= read -r -d '' f; do
        echo "  removing $f"
        rm "$f"
        swept=$((swept+1))
    done < <(find "$sub" -maxdepth 1 -name '*.go' -type f \
            ! -name "$name.go" \
            ! -name "where.go" \
            ! -name "gql_collection.go" \
            ! -name "gql_node.go" \
            -print0)
done

if [[ $swept -eq 0 ]]; then
    echo "No stale files found — layout already clean. No sweep needed."
else
    echo ""
    echo "Swept $swept stale file(s) from $VELOX_DIR."
    echo "Now re-run your velox generator (go run ./generate.go) to restore"
    echo "any that should still be there, then verify with 'go build ./...'"
fi
