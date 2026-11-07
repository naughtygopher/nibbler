# 0007. Flush timer armed on first item

- Status: Accepted
- Date: 2026-11-07

## Context

Partial batches were flushed by a free-running `time.Ticker`, reset only after a batch was processed. This has 2 problems:

1. **Idle wake-ups:** with an empty batch, the listener still wakes up every `TickerDuration`, forever, to do nothing
2. **Phase is unrelated to arrival:** an item arriving 10ms before a tick is flushed almost alone, while one arriving just after a tick waits the full duration. Batches at low throughput are smaller than they need to be, which wastes round-trips to the database or API

The README describes `TickerDuration` as the maximum acceptable latency of an item. A timer which starts with the first item of a batch gives exactly that.

## Decision

The ticker is replaced by a `time.Timer` which is created stopped.

| Event | Timer |
|---|---|
| An item makes `len(batch) == 1` | `Reset(TickerDuration)` |
| The batch is full | `Stop()`, then process with `TriggerFull` |
| The timer fires | process with `TriggerTicker` |

After a batch is processed or reset, the timer stays disarmed until the next first item. This relies on Go 1.23+ timer semantics, where `Stop` and `Reset` guarantee no stale value is received afterwards; the `go 1.25` directive enables them. A defensive empty-batch check remains on the timer case for applications running with `GODEBUG=asynctimerchan=1`.

`TriggerTicker` and `TickerDuration` keep their names for compatibility. The godoc of `TickerDuration` now reads: the maximum time an item waits before its batch is flushed.

## Consequences

- Behaviour change: a partial batch is flushed `TickerDuration` after its first item arrived, instead of at the next tick. `TestFlushLatency` asserts it on the fake clock: an item pushed at 0.9s is flushed at exactly 1.9s
- An idle listener has no timers armed and never wakes up
- Low throughput batches are fuller, since every item gets the full window to be joined by others
- Throughput is unchanged:

| Benchmark | Before (ns/item) | After (ns/item) | Delta |
|---|---|---|---|
| `Throughput/size=1` | 641.3 ± 1% | 645.4 ± 1% | ~ (p=0.529) |
| `Throughput/size=10` | 156.4 ± 2% | 156.8 ± 1% | ~ (p=0.756) |
| `Throughput/size=100` | 84.4 ± 1% | 84.1 ± 2% | ~ (p=0.684) |
| `Throughput/size=1000` | 102.0 ± 0% | 102.5 ± 1% | ~ (p=0.379) |
| `ThroughputParallel/size=100` | 147.7 ± 2% | 146.3 ± 2% | ~ (p=0.424) |

## Alternatives considered

- Keep the ticker and reset it on the first item: fixes the phase problem, but still wakes up an idle listener every `TickerDuration`
- Rename to `FlushTimeout` / `TriggerTimeout`: more accurate, but breaks every user for a naming improvement
