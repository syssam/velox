# Benchmark baselines

`root.txt`, `veloxotel.txt` and `example-graphql.txt` are `go test -bench` output
(`-count 6 -benchtime 200ms -benchmem`) for the root module, `contrib/veloxotel`
and `examples`'s `./graphql`. `scripts/bench-compare.sh` runs the same
benchmarks and compares them with these files using benchstat (pinned in the
script). CI and `scripts/ci-local.sh` run it after the scale steps.

It is report-only: the script exits 0 whatever benchstat reports. The baselines
come from one machine and CI runs on others, so CI's time deltas are
informational. Allocation counts are enforced by the budget tests, not here.
Read the allocs/op and B/op tables as a signal that travels across machines; read
sec/op as a signal only when both sides ran on the same hardware.

## Provenance

Each file starts with `#` lines giving when it was recorded, the commit, the
machine, the Go version and the load average before the run.

| Recorded (UTC)       | Commit  | Machine                                | Go       | Load before the run (1, 5, 15 min) |
|----------------------|---------|----------------------------------------|----------|------------------------------------|
| 2026-10-28T19:08:08Z | 90b80b2 | Apple M3 Max, 14 CPUs, macOS (arm64)   | go1.27.1 | 5.57 6.09 7.37                     |

The machine was shared and not idle (a 1-minute load of 5.6 on 14 CPUs, other
projects running); it did not drop under 4 in 25 minutes of waiting. A second run
against the baseline, minutes later, showed no sec/op change beyond noise
(a -5.9% on TenantScopedOneRow was the largest) and identical allocation counts.

## Updating

Run on an idle machine, then commit the three files with the reason:

    scripts/bench-compare.sh -update

Update when a change moves a benchmark on purpose, or when the Go version
changes. The `#` header lines are rewritten by the script.
