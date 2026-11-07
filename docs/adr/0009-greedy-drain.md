# 0009. Greedy drain

- Status: Accepted
- Date: 2026-11-07

## Context

The listener ran one 3-way `select` (context, timer, receiver) per item. Under load the receiver usually holds many items at once, so most of those `select`s are pure overhead: each one locks the channels involved, only to pick the receiver again.

This change was gated on [0004](0004-benchmark-backed-performance.md): merge only with a statistically significant gain (p < 0.05) of at least 10% at `size=100` or `size=1000`, and no regression elsewhere.

## Decision

After receiving an item, the listener picks up already buffered items with non-blocking receives, until the batch is full or the receiver is empty:

```go
drain:
	for len(bat.batch) < size {
		select {
		case value = <-bat.queue:
			bat.batch = append(bat.batch, value)
		default:
			break drain
		}
	}
```

The flush timer from [0007](0007-flush-timer-armed-on-first-item.md) is armed after the drain, only if the batch was empty before and is still partial after it. The drain takes nanoseconds per item, so this does not change the latency bound in practice. It also avoids arming and stopping the timer for every item when `Size` is 1.

## Consequences

The gate is passed by a wide margin, and the change is faster across the board:

| Benchmark | Before (ns/item) | After (ns/item) | Delta |
|---|---|---|---|
| `Throughput/size=1` | 645.4 ± 1% | 616.5 ± 1% | -4.49% (p=0.000) |
| `Throughput/size=10` | 156.8 ± 1% | 113.3 ± 3% | -27.69% (p=0.000) |
| `Throughput/size=100` | 84.1 ± 2% | 35.7 ± 1% | -57.57% (p=0.000) |
| `Throughput/size=1000` | 102.5 ± 1% | 28.9 ± 1% | -71.85% (p=0.000) |
| `ThroughputParallel/size=100` | 146.3 ± 2% | 61.2 ± 2% | -58.14% (p=0.000) |

Allocations are unchanged. Cancellation is checked once per drain instead of once per item, so a stop can be delayed by at most `Size - 1` non-blocking receives.

## Alternatives considered

- Keep one `select` per item: simplest, but leaves more than half of the throughput on the table at typical batch sizes
- Read `len(queue)` and receive exactly that many items: same effect, but couples correctness to a length snapshot, while the non-blocking receive is self-evidently safe
