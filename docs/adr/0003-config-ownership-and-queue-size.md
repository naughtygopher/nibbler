# 0003. Config ownership & queue size

- Status: Accepted
- Date: 2026-11-07

## Context

`New` kept the caller's `*Config`, so any change to it after `New` was read by the listener goroutine: a data race, and a way to change `Size` or `Processor` mid-flight.

The receiver channel buffer was always `Size`. While the processor runs, producers block after only `Size` items, so a batch size of 100 with a 50ms bulk insert stalls producers whenever more than 100 items arrive within those 50ms. Buffering and batching are different concerns.

`Sanitize` defaulted `TickerDuration` to 1 minute, while the field comment and README document 1 second. The test asserted the buggy value.

## Decision

1. `Nibbler` stores a copy, `cfg Config[T]`. `New` still accepts `*Config[T]`, so the defaults applied by `Sanitize` remain visible to the caller
2. New field `QueueSize uint`, the receiver channel buffer length. If 0, it defaults to `Size`, which keeps the v0.2.2 behaviour
3. `TickerDuration` defaults to 1 second, as documented

## Consequences

- Changing the config after `New` has no effect, and is no longer a race
- Users can absorb bursts without growing the batch size, e.g. pocache maps its `QLength` to `QueueSize` and `BulkSize` to `Size`
- Users who relied on the undocumented 1 minute default now flush up to 60x more often. This is called out in the migration notes

## Alternatives considered

- Accept `Config[T]` by value in `New`: callers lose visibility of the sanitized defaults, and it breaks every call site for no functional gain
- Keep the 1 minute default and fix the docs: 1 minute is a poor latency bound for a micro-batcher and contradicts what users have been told so far
