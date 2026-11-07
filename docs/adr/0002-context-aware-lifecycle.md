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

## Alternatives considered

- `Close()` method: duplicates what a context already provides and needs its own synchronisation
- Keep closing the receiver on stop: it is the only way for producers to observe the stop without `Done`, but it turns a stop into a panic in producer goroutines
- Return the stop reason only from `Listen`: `Start` users would never learn why the listener stopped
