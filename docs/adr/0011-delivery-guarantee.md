# 0011. Delivery guarantee

- Status: Accepted
- Date: 2026-11-07

## Context

Nibbler's tests checked individual behaviours, but nothing stated, or verified end to end, what happens to an item between `Receiver() <- item` and the processor. Users of a batcher need to know whether an item can be lost or processed twice, e.g. to decide if the processor has to be idempotent.

While reviewing v0.3.0, one gap was found: when a module declares Go older than 1.21, `GODEBUG=panicnil=1` is the default and `recover()` returns `nil` for `panic(nil)`. A processor doing so was treated as successful: no `ProcessorErr`, and the batch stayed in the listener and was handed to the processor again with the next items.

## Decision

The guarantee is a documented contract:

1. **At most once.** An item is handed to the processor at most once. A batch which failed, by an error or a panic, is reported to `ProcessorErr` and is never retried
2. **Exactly once on a clean stop.** With `FlushOnStop`, and producers stopped before the listener's context is cancelled, every item sent is handed to the processor
3. **Dropped, never duplicated, otherwise.** Without `FlushOnStop`, or with producers still sending when the context is cancelled, pending items may be dropped. They are never processed twice
4. **Order.** Items sent by a single producer are handed to the processor in the order they were sent

"Handed to the processor" means the processor was called with the item in its batch, whatever it returned.

It is enforced by randomised stress tests, which run in CI with the race detector:

| Test | Checks |
|---|---|
| `TestStressExactlyOnce` | Points 1, 2 and 4, under random `Size`, `QueueSize`, producer count, processor errors and panics, and panics in `ProcessorErr`, 300 rounds. Also that no batch is larger than `Size`, and that `Done` closes with `context.Canceled` |
| `TestStressCancelMidStream` | Point 3: cancelling at a random moment with producers still sending. No item is processed twice, the listener stops, and producers selecting on `Done()` never block |

Every round is seeded. The seed is logged, and `NIBBLER_STRESS_SEED` reproduces a failure. The tests are skipped with `go test -short`. They take ~1.5s with `-race`.

The tests were validated by mutation: removing the final partial-batch flush, retrying failed batches, dropping an item while draining and ignoring the queue on flush each make them fail within the first rounds.

The `panic(nil)` gap is fixed by detecting a panic by the processor not having returned, instead of by the value `recover()` gives. `TestPanicNil` covers it with `//go:debug panicnil=1`, and fails without the fix.

## Consequences

- Processors which are not idempotent are safe, since an item is never delivered twice. Users who need at-least-once have to retry in the processor, or send the failed batch again from `ProcessorErr`
- A processor which calls `runtime.Goexit` (e.g. `testing.T.FailNow`) still stops the listener without a reason: `Done` is closed and `Err` returns `nil`. This is documented, and cannot be recovered from
- Changes to the listener loop are checked against the guarantee, not just against unit tests
- ~1.5s more in CI

## Alternatives considered

- At-least-once with retries: nibbler cannot tell a partially applied batch from a failed one, so retries risk duplicates, and the right policy (backoff, limits, dead letters) depends on the application
- Stating the guarantee in docs only: it was already implied by the design, and implied guarantees regress unnoticed
- Property-based testing library: a dependency for what a seeded `math/rand/v2` loop does
