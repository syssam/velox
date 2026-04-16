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
    all)
        run_sql_benchmarks
        run_codegen_benchmarks
        run_privacy_benchmarks
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
        # Reproducible wall-time + memory comparison: Velox vs Ent on the same 50-entity schema.
        # Requires: Go toolchain, benchmarks/fixtures/ent/go.mod already present.
        # Output: benchmarks/results/vs-ent.txt
        echo "=== Velox vs Ent Code Generation Benchmark ==="
        mkdir -p benchmarks/results

        VELOX_DIR="benchmarks/fixtures/velox"
        ENT_DIR="benchmarks/fixtures/ent"
        OUT="benchmarks/results/vs-ent.txt"

        run_timed() {
            local label="$1"; shift
            local dir="$1";  shift
            local cmd=("$@")
            local start end elapsed mem_kb
            # /usr/bin/time -l on macOS prints max RSS in bytes; -v on Linux
            if [[ "$(uname)" == "Darwin" ]]; then
                result=$( { /usr/bin/time -l "${cmd[@]}" 2>&1 1>/dev/null; } 2>&1 )
                elapsed=$(echo "$result" | awk '/real/ {print $1}')
                mem_kb=$(echo "$result" | awk '/maximum resident/ {printf "%.0f", $1/1024}')
            else
                result=$( { /usr/bin/time -v "${cmd[@]}" 2>&1 1>/dev/null; } 2>&1 )
                elapsed=$(echo "$result" | awk '/Elapsed/ {print $NF}')
                mem_kb=$(echo "$result" | awk '/Maximum resident/ {print $NF}')
            fi
            echo "$label: elapsed=${elapsed} mem=${mem_kb}KB" | tee -a "$OUT"
        }

        > "$OUT"  # reset output file

        echo "--- Schema: $(ls "$ENT_DIR/ent/schema/"*.go 2>/dev/null | wc -l | tr -d ' ') entities ---" | tee -a "$OUT"
        echo "" | tee -a "$OUT"

        # Velox
        echo "[Velox] generating..." | tee -a "$OUT"
        run_timed "velox" "$VELOX_DIR" go run "$VELOX_DIR/generate.go"

        # Ent
        echo "[Ent]   generating..." | tee -a "$OUT"
        (cd "$ENT_DIR" && run_timed "ent" "." go run generate.go)

        echo "" | tee -a "$OUT"
        echo "Results saved to $OUT"
        echo ""
        cat "$OUT"
        ;;
    *)
        echo "Usage: $0 [sql|codegen|privacy|all|compare|vs-ent]"
        exit 1
        ;;
esac
