#!/usr/bin/env bash
# Velox benchmark runner
# Usage: ./benchmarks/run.sh [sql|codegen|all]
set -euo pipefail

cd "$(dirname "$0")/.."

MODE="${1:-all}"

run_sql_benchmarks() {
    echo "=== SQL Builder Benchmarks ==="
    go test -bench=. -benchmem -count=5 -timeout=10m ./dialect/sql/ | tee benchmarks/results/sql.txt
    echo ""
}

run_codegen_benchmarks() {
    echo "=== Code Generation Benchmarks ==="
    go test -bench=BenchmarkGraph -benchmem -count=5 -timeout=10m ./compiler/gen/ | tee benchmarks/results/codegen.txt
    echo ""
}

run_privacy_benchmarks() {
    echo "=== Privacy Layer Benchmarks ==="
    go test -bench=. -benchmem -count=5 -timeout=10m ./privacy/ | tee benchmarks/results/privacy.txt
    echo ""
}

run_postgres_benchmarks() {
    echo "=== Postgres vs SQLite End-to-End Benchmarks ==="
    if [ -z "${VELOX_TEST_POSTGRES:-}" ]; then
        echo "VELOX_TEST_POSTGRES not set — skipping Postgres benchmarks."
        echo "Example:"
        echo "  docker run -e POSTGRES_PASSWORD=postgres -p 5432:5432 -d postgres:18"
        echo "  VELOX_TEST_POSTGRES='postgres://postgres:postgres@localhost/postgres?sslmode=disable' \\"
        echo "    ./benchmarks/run.sh postgres"
        return 0
    fi
    go test -run '^$' -bench '_Postgres' -benchmem -count=3 -timeout=15m \
        ./tests/integration/ | tee benchmarks/results/postgres.txt
    echo ""
}

mkdir -p benchmarks/results

case "$MODE" in
    sql)
        run_sql_benchmarks
        ;;
    codegen)
        run_codegen_benchmarks
        ;;
    privacy)
        run_privacy_benchmarks
        ;;
    postgres)
        run_postgres_benchmarks
        ;;
    all)
        run_sql_benchmarks
        run_codegen_benchmarks
        run_privacy_benchmarks
        run_postgres_benchmarks
        echo "=== All benchmarks complete ==="
        echo "Results in benchmarks/results/"
        ;;
    compare)
        # Compare current results against a baseline
        if [ ! -f benchmarks/results/baseline.txt ]; then
            echo "No baseline found. Run with 'all' first, then copy results to baseline.txt"
            exit 1
        fi
        echo "=== Running current benchmarks ==="
        go test -bench=. -benchmem -count=5 -timeout=10m \
            ./dialect/sql/ ./compiler/gen/ ./privacy/ > benchmarks/results/current.txt
        echo "=== Comparison ==="
        benchstat benchmarks/results/baseline.txt benchmarks/results/current.txt
        ;;
    vs-ent)
        # Quick reproducible comparison: Velox vs Ent on the same 50-entity schema.
        # Uses 'go run' so numbers include generator compile time.
        # For generation-only numbers (3.2x claim), see docs/benchmarks.md — pre-compiled binary method.
        # Requires: Go toolchain. Ent fixture has its own go.mod at benchmarks/fixtures/ent/.
        # Output: benchmarks/results/vs-ent.txt
        echo "=== Velox vs Ent Code Generation Benchmark ==="
        mkdir -p benchmarks/results

        ROOT="$(pwd)"
        VELOX_DIR="$ROOT/benchmarks/fixtures/velox"
        ENT_DIR="$ROOT/benchmarks/fixtures/ent"
        OUT="$ROOT/benchmarks/results/vs-ent.txt"
        RUNS="${BENCH_RUNS:-3}"

        # measure <label> <dir> <cmd...>
        # Runs cmd RUNS times, records median wall-time and peak RSS.
        measure() {
            local label="$1" dir="$2"; shift 2
            local times=()
            echo "  running $label ($RUNS runs)..."
            for i in $(seq 1 "$RUNS"); do
                local t
                t=$(cd "$dir" && { time "$@" 2>/dev/null; } 2>&1 | awk '/^real/{print $2}' || echo "error")
                times+=("$t")
            done
            # pick first successful run as representative (median requires sort+bc, keep simple)
            local rep="${times[0]}"
            echo "$label: ${rep}" | tee -a "$OUT"
        }

        # peak RSS via /usr/bin/time (separate from wall-time for simplicity)
        measure_rss() {
            local label="$1" dir="$2"; shift 2
            local rss
            if [[ "$(uname)" == "Darwin" ]]; then
                rss=$(cd "$dir" && /usr/bin/time -l "$@" 2>&1 | awk '/maximum resident/{printf "%.0fMB\n", $1/1048576}')
            else
                rss=$(cd "$dir" && /usr/bin/time -v "$@" 2>&1 | awk '/Maximum resident/{printf "%.0fMB\n", $1/1024}')
            fi
            echo "$label RSS: ${rss:-n/a}" | tee -a "$OUT"
        }

        ENTITIES=$(ls "$ENT_DIR/ent/schema/"*.go 2>/dev/null | wc -l | tr -d ' ')
        {
            echo "Velox vs Ent — code generation benchmark"
            echo "Schema: ${ENTITIES} entities  |  Runs: ${RUNS}"
            echo "Date: $(date -u '+%Y-%m-%d %H:%M UTC')"
            echo "---"
        } | tee "$OUT"

        echo ""
        echo "--- Wall time ---"
        echo "--- Wall time ---" >> "$OUT"
        measure "velox" "$VELOX_DIR" go run generate.go
        measure "ent"   "$ENT_DIR"   go run generate.go

        echo ""
        echo "--- Peak memory (RSS) ---"
        echo "--- Peak memory (RSS) ---" >> "$OUT"
        measure_rss "velox" "$VELOX_DIR" go run generate.go
        measure_rss "ent"   "$ENT_DIR"   go run generate.go

        echo ""
        echo "Results saved to benchmarks/results/vs-ent.txt"
        ;;
    scale)
        # Scale benchmark across stress fixtures: time codegen + cold build
        # + peak RSS at 100/200/328-entity schemas. Used to verify the
        # cycle-break refactor (which doubled per-entity package count via
        # client/{entity}/) didn't regress velox's incremental-compile
        # advantage. Output: benchmarks/results/scale.txt
        echo "=== Velox scale benchmark (100 / 200 / 328 entities) ==="
        mkdir -p benchmarks/results

        ROOT="$(pwd)"
        OUT="$ROOT/benchmarks/results/scale.txt"

        rss_cmd() {
            if [[ "$(uname)" == "Darwin" ]]; then
                echo "/usr/bin/time -l"
            else
                echo "/usr/bin/time -v"
            fi
        }
        rss_extract() {
            if [[ "$(uname)" == "Darwin" ]]; then
                # macOS reports bytes
                awk '/maximum resident/{printf "%.0fMB\n", $1/1048576}'
            else
                # Linux reports KB
                awk '/Maximum resident/{printf "%.0fMB\n", $1/1024}'
            fi
        }

        {
            echo "Velox scale benchmark"
            echo "Date: $(date -u '+%Y-%m-%d %H:%M UTC')"
            echo "Platform: $(uname -sr) $(uname -m)"
            echo "Go: $(go version | awk '{print $3, $4}')"
            echo "---"
        } | tee "$OUT"

        for n in 100 200 328; do
            DIR="$ROOT/benchmarks/fixtures/stress-$n"
            if [[ ! -d "$DIR" ]]; then
                echo "warning: stress-$n fixture missing at $DIR — skipping" | tee -a "$OUT"
                continue
            fi

            echo "" | tee -a "$OUT"
            echo "=== stress-$n ===" | tee -a "$OUT"

            # 1) regen — cold (forces parsing the schema dir, so generator startup cost included)
            (cd "$DIR" && rm -rf velox)
            REGEN=$( { time (cd "$DIR" && go run generate.go >/dev/null 2>&1); } 2>&1 | awk '/^real/{print $2}')
            echo "regen wall: $REGEN" | tee -a "$OUT"

            # 2) cold build
            go clean -cache >/dev/null 2>&1
            BUILD=$( { time (cd "$DIR" && go build ./velox/... 2>/dev/null); } 2>&1 | awk '/^real/{print $2}')
            echo "cold build wall: $BUILD" | tee -a "$OUT"

            # 3) peak RSS during build
            go clean -cache >/dev/null 2>&1
            RSS=$(cd "$DIR" && $(rss_cmd) go build ./velox/... 2>&1 >/dev/null | rss_extract)
            echo "cold build RSS: ${RSS:-n/a}" | tee -a "$OUT"

            # 4) incremental rebuild — touch one entity, rebuild
            # This is the metric that motivated per-entity packages in the first place.
            ENT_FILE=$(find "$DIR/velox" -maxdepth 2 -name 'entity000.go' | head -1)
            if [[ -n "$ENT_FILE" ]]; then
                touch "$ENT_FILE"
                INCR=$( { time (cd "$DIR" && go build ./velox/... 2>/dev/null); } 2>&1 | awk '/^real/{print $2}')
                echo "incremental (1-file touch): $INCR" | tee -a "$OUT"
            fi
        done

        echo "" | tee -a "$OUT"
        echo "Results: $OUT"
        ;;
    *)
        echo "Usage: $0 [sql|codegen|privacy|postgres|all|compare|vs-ent|scale]"
        exit 1
        ;;
esac
