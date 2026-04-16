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
    *)
        echo "Usage: $0 [sql|codegen|privacy|all|compare]"
        exit 1
        ;;
esac
