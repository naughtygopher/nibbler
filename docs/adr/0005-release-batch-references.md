# 0005. Release batch references

- Status: Accepted
- Date: 2026-11-07

## Context

After a batch is processed, nibbler reuses its memory with `batch = batch[:0]`. The length drops to 0, but the backing array still holds every processed item. For any `T` which contains pointers (pointers, strings, slices, maps, structs with any of them), up to `Size` processed items stay reachable until they are overwritten by later items.

With a batch size of 1,000 and 64 KiB payloads, that is up to ~64 MiB the garbage collector cannot free. On a quiet stream, or after the listener stops, they are pinned indefinitely.

## Decision

Every reset of the batch goes through one helper, which zeroes the items before truncating:

```go
func (bat *Nibbler[T]) reset() {
	clear(bat.batch)
	bat.batch = bat.batch[:0]
}
```

It is used after a successful batch, after a failed batch with `ResumeAfterErr`, and when `Listen` returns.

`TestBatchReleased` pushes items tracked with `runtime.AddCleanup` and asserts they are garbage collected in all 3 cases. All 3 fail without the change.

## Consequences

- Processed and dropped items are collectable as soon as the processor returns
- One `memclr` over `len(batch)` elements per batch. It amortises across the batch and is not measurable from `size=10` onwards:

| Benchmark | Before (ns/item) | After (ns/item) | Delta |
|---|---|---|---|
| `Throughput/size=1` | 630.0 ± 1% | 641.3 ± 1% | +1.79% (p=0.015) |
| `Throughput/size=10` | 156.1 ± 3% | 156.4 ± 2% | ~ (p=0.644) |
| `Throughput/size=100` | 84.2 ± 2% | 84.4 ± 1% | ~ (p=0.869) |
| `Throughput/size=1000` | 101.8 ± 1% | 102.0 ± 0% | ~ (p=0.303) |
| `ThroughputParallel/size=100` | 145.7 ± 2% | 147.7 ± 2% | ~ (p=0.353) |

## Alternatives considered

- Allocate a new slice per batch: frees old items but adds 1 allocation of `Size` elements per batch, which is far more expensive than clearing
- Clear only when `T` contains pointers: needs reflection to decide, and `clear` on pointer-free types is already a cheap `memclr`
