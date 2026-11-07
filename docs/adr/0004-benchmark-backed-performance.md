# 0004. Benchmark-backed performance

- Status: Accepted
- Date: 2026-11-07

## Context

Nibbler claims to be efficient, but has no benchmarks to back it. ADRs 0005, 0007 and 0009 change the listener's hot loop, and each needs before/after numbers to prove it does not regress, or that it helps.

## Decision

`bench_test.go` measures end-to-end throughput: items pushed into the receiver until every one of them has been handed to a no-op processor. `TickerDuration` is 1 hour so only full batches are flushed.

| Benchmark | Shape |
|---|---|
| `BenchmarkThroughput/size={1,10,100,1000}` | 1 producer |
| `BenchmarkThroughputParallel/size=100` | `GOMAXPROCS` producers via `b.RunParallel` |

Every change to the listener loop attaches a `benchstat` comparison against the previous baseline:

```bash
go test -run '^$' -bench . -benchmem -count 10 ./... > new.txt
benchstat old.txt new.txt
```

### Baseline

Intel i7-14700, 28 threads, Go 1.27.3, after ADR 0003.

| Benchmark | ns/item | B/item | allocs/item |
|---|---|---|---|
| `Throughput/size=1` | 630.0 ± 1% | 272 | 4 |
| `Throughput/size=10` | 156.1 ± 3% | 27 | 0 |
| `Throughput/size=100` | 84.2 ± 2% | 2 | 0 |
| `Throughput/size=1000` | 101.8 ± 1% | 0 | 0 |
| `ThroughputParallel/size=100` | 145.7 ± 2% | 2 | 0 |

The listener itself does not allocate per item. The 272 B and 4 allocations are per batch, from `context.WithTimeout` for the batch context, and amortise to ~0 from `size=100` onwards.

## Consequences

- Performance claims in the README and in later ADRs are backed by numbers
- Benchmarks are not run in CI, since shared runners are too noisy for a regression gate. They are run locally for every change to the listener loop

## Alternatives considered

- Benchmark only the listener loop with a pre-filled channel: excludes channel contention with producers, which is what users actually pay for
- Run benchmarks in CI with a threshold: shared runners vary by more than 10% between runs, which would make the gate either flaky or meaningless
