# 0008. Flush on stop

- Status: Accepted
- Date: 2026-11-07

## Context

Since [0002](0002-context-aware-lifecycle.md), items pending when the listener stops are dropped: the partial batch and whatever is buffered in the receiver. This is right for pocache, where a lost cache refresh is harmless, but loses data for write-behind uses like the README's bank statements example. Users cannot flush from outside, since the batch is owned by the listener goroutine.

## Decision

An opt-in config field and a dedicated trigger:

```go
type Config[T any] struct {
	// ...
	FlushOnStop bool
}

const TriggerStop Trigger = "STOP"
```

When `Listen` returns and its context is done, with `FlushOnStop` enabled:

1. A batch which already failed was reported to `ProcessorErr` and is not retried
2. The number of items buffered in the receiver is read once, and at most that many are drained. Producers which keep sending cannot prolong the flush
3. Drained items are appended to the partial batch, and processed in batches of `Size` with `TriggerStop`
4. Every batch context is `context.WithTimeout(context.WithoutCancel(ctx), ProcessingTimeout)`. The flush is not aborted by the cancellation which triggered it, and is still bounded per batch
5. Errors and panics are reported to `ProcessorErr`, and flushing continues irrespective of `ResumeAfterErr`
6. `Done()` is closed after the flush. `Listen` still returns `context.Cause(ctx)`

The flush does not run if the listener stopped because of a processor error while its context was alive.

The worst case shutdown time is `ceil((Size - 1 + QueueSize) / Size) × ProcessingTimeout`, e.g. 11 seconds for `Size=100`, `QueueSize=1000`, `ProcessingTimeout=1s`.

## Consequences

- Graceful shutdown without data loss: stop producers, cancel the context, wait on `Done()`
- Processors can tell a shutdown flush apart via `TriggerStop`, e.g. to skip non-essential work
- Items sent after the flush started are dropped, and producers which do not stop first may block on a full receiver. Both are documented on `FlushOnStop`
- Default behaviour is unchanged, there is no cost when disabled

## Alternatives considered

- A public `Flush()` method: the batch is owned by the listener goroutine, so it needs a lock or a request channel on the hot path
- Always flush: slows down shutdown for users who do not need it, e.g. pocache
- Drain until the receiver is empty: never finishes with live producers
- Retry the in-flight batch which was cancelled: risks processing it twice, since the processor may have partially succeeded. Processors which must finish can use `context.WithoutCancel` themselves
