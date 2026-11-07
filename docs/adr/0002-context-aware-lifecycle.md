# 0002. Context-aware lifecycle

- Status: Accepted
- Date: 2026-11-07

## Context

In v0.2.2 `Listen()` has no exit path while `ResumeAfterErr` is enabled, so its goroutine leaks for the life of the process. With resume disabled, `Listen` closes the receiver channel on the first processor error and the next send by a producer panics with `send on closed channel`. CI had to skip `-race` for `TestProcessorErr` for exactly this reason.

Each batch context is derived from `context.Background()`, so an in-flight batch cannot be cancelled by the application. Downstream libraries like [pocache](https://github.com/naughtygopher/pocache) need a batch processor which can be cancelled and joined within their own shutdown.

## Decision

The listener is driven by a context, and the receiver channel is never closed.

```go
var ErrAlreadyListening = errors.New("nibbler is already listening")

func Start[T any](ctx context.Context, cfg *Config[T]) (*Nibbler[T], error)
func (bat *Nibbler[T]) Listen(ctx context.Context) error
func (bat *Nibbler[T]) Done() <-chan struct{}
func (bat *Nibbler[T]) Err() error
```

| Condition | `Listen` returns |
|---|---|
| `ctx` is done | `context.Cause(ctx)` |
| Processor error or panic, `ResumeAfterErr` disabled | that error |
| `Listen` already called on this instance | `ErrAlreadyListening`, immediately |

1. `Listen` is guarded by an `atomic.Bool`; a rejected second call does not affect `Done` or `Err`
2. `ctx.Err()` is checked at the start of every iteration, since `select` picks randomly among ready cases. It is a single atomic load
3. Every batch context is `context.WithTimeout(listenCtx, ProcessingTimeout)`
4. `Done()` is closed when `Listen` returns. `Err()` returns `nil` until then and the stop reason after, mirroring `ctx.Done()` and `ctx.Err()`. The stop reason is written before `Done` is closed, so reading it is race free
5. `Start` runs `go Listen(ctx)` and discards the return value; `Err()` exposes it
6. Items in the partial batch or buffered in the receiver when the listener stops are dropped

Tests run on `testing/synctest`, so tickers and timeouts use a fake clock and the suite runs in ~1s instead of ~8s. The minimum Go version is raised to 1.25 for `synctest`, and CI runs every test with `-race` on Go 1.25 and 1.27.

## Consequences

- Breaking: `Start(cfg)` becomes `Start(ctx, cfg)` and `Listen()` becomes `Listen(ctx) error`
- Breaking: Go 1.25 or newer is required
- Producers can no longer panic on send. If the listener may stop, producers should `select` on `Done()` when sending, otherwise a full receiver blocks forever
- Cancelling the parent context aborts in-flight processing; the processor sees it via `ctx.Done()`
- pocache can run `Listen(ctx)` inside its worker `WaitGroup` and join it on close
- Each batch context is now derived from the listener's context instead of `context.Background()`. When that context can be cancelled, `context.WithTimeout` registers the batch context with it and unregisters it afterwards, which costs ~100 ns per batch (see below)

### Measured cost

Measured after the fact, with the benchmarks from [0004](0004-benchmark-backed-performance.md), on Intel i7-14700, Go 1.27.3, 10 runs each, compared with `benchstat`. v0.2.2 is the last release before this ADR, v0.3.0 includes every later ADR as well. The last column is v0.3.0 with an uncancellable `context.Background()` as the listener context, which isolates this ADR's cost.

| Benchmark | v0.2.2 (ns/item) | v0.3.0 (ns/item) | v0.3.0, `context.Background()` (ns/item) |
|---|---|---|---|
| `Throughput/size=1` | 546.8 | 612.9 (slower, +12%) | 520.4 (faster, -5%) |
| `Throughput/size=10` | 124.5 | 112.7 (faster, -10%) | 101.9 (faster, -18%) |
| `Throughput/size=100` | 61.6 | 35.9 (faster, -42%) | 34.5 (faster, -44%) |

The cost is per batch, not per item. It is visible at `size=1`, where it is the whole batch, and ~11 ns/item at `size=10`. From `size=100` it is lost in the noise. The later ADRs ([0007](0007-flush-timer-armed-on-first-item.md), [0009](0009-greedy-drain.md)) more than pay for it from `size=10` onwards. A batch size of 1 is not batching, so this is accepted. Removing the cost would need a custom `context.Context` implementation or dropping cancellation of in-flight batches, neither is worth it.

## Alternatives considered

- `Close()` method: duplicates what a context already provides and needs its own synchronisation
- Keep closing the receiver on stop: it is the only way for producers to observe the stop without `Done`, but it turns a stop into a panic in producer goroutines
- Return the stop reason only from `Listen`: `Start` users would never learn why the listener stopped
