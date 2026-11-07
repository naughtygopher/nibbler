<p align="center"><img src="https://github.com/user-attachments/assets/1b34c21a-8031-43d3-a172-44e039b58190" alt="nibbler gopher" width="256px"/></p>

[![](https://github.com/naughtygopher/nibbler/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/naughtygopher/nibbler/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/naughtygopher/nibbler.svg)](https://pkg.go.dev/github.com/naughtygopher/nibbler)
[![Go Report Card](https://goreportcard.com/badge/github.com/naughtygopher/nibbler?cache_invalidate=1)](https://goreportcard.com/report/github.com/naughtygopher/nibbler)
[![Coverage Status](https://coveralls.io/repos/github/naughtygopher/nibbler/badge.svg?branch=main&cache_invalidate=1)](https://coveralls.io/github/naughtygopher/nibbler?branch=main)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/naughtygopher/nibbler/blob/main/LICENSE)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go#stream-processing)

# Nibbler

Nibbler is a resilient & minimal package which helps you implement micro-batch processing, _within an application_. Nibbler remains minimal with its 0 external dependencies and remains resilient within the context of the application by gracefully handling errors and panics.

IMPORTANT: This is not a general purpose _*distributed*_ task queue.

## What is Micro-batch Processing?

Micro-batch processing is a way to handle data by breaking a big task into smaller pieces and processing them one piece at a time. This method is useful in processing realtime data or stream processing, where the incoming data is split into "micro-batches" and processed quickly, rather than waiting to collect all data at once or process one by one.

The same concept can also be extended to handle events processing. So, we have a queue subscriber, and instead of processing the events individually, we create micro batches and process them.

The processing of a single micro batch can be triggered in two ways, based on time or if the micro batch size is full. i.e. process a non empty batch if duration X has passed since its first item arrived, or if the batch size is full

<p align="center">
<img src="https://github.com/user-attachments/assets/0a7df1c0-2d23-475e-9cc3-205f3f9bf4c4" alt="nibbler" width="384px"/>
</p>

## Why use nibbler?

High-throughput event and stream processing systems benefit significantly from batch processing rather than handling events individually. Well-implemented batching optimizes resource utilization across downstream dependencies like databases, APIs, and external systems-by dramatically reducing [IOPS](https://en.wikipedia.org/wiki/IOPS). Batch operations enable more efficient use of network round-trips, connection pools, and database transaction overhead. For example, a single bulk INSERT of 100 rows is substantially cheaper than 100 individual INSERT statements, both in terms of I/O operations and connection utilization.

However, batching introduces a latency tradeoff. A naive implementation that waits for a batch to reach full capacity before processing will delay event handling indefinitely during low-traffic periods. If your batch size is 100 and events arrive at 1 per second, you'd wait over a minute and a half before any processing occurs-unacceptable for systems requiring near-realtime responsiveness.

Nibbler addresses this with a dual-trigger flush mechanism: batches are processed either when they reach a configured size threshold or when a time interval elapses; whichever occurs first. This bounded-latency approach ensures:

1. **High throughput scenarios**: Batches fill quickly and flush at capacity, maximizing I/O efficiency

2. **Low throughput scenarios**: Partial batches flush at the configured interval, guaranteeing a maximum processing delay

3. **Variable load patterns**: The system self-adjusts without manual intervention, processing full batches during traffic spikes while maintaining responsiveness during quiet periods

The configurable flush interval (TickerDuration) represents your maximum acceptable latency. The worst-case delay between event arrival and processing. The batch size (Size) determines your maximum I/O optimization. Together, these parameters let you tune the latency-efficiency tradeoff to match your specific requirements.

### Config

```golang
type BatchProcessor[T any] func(ctx context.Context, trigger Trigger, batch []T) error

type Config[T any] struct {
	// ProcessingTimeout is context timeout for processing a single batch. If less than 1ms, defaults to 1s
	ProcessingTimeout time.Duration
	// TickerDuration is the maximum time an item waits before its batch is flushed. The timer
	// starts when the first item of a batch arrives. If less than 1ms, defaults to 1s
	TickerDuration time.Duration

	// Size is the micro batch size. If 0, defaults to 100
	Size uint
	// QueueSize is the buffer length of the receiver channel. If 0, defaults to Size
	QueueSize uint

	// Processor is a required configuration, it is called when a batch process is initiated either by
	// ticker or when the batch is full. The 'batch' slice received must not be changed, if you have to
	// process the batch asynchronously, copy the batch to a new slice prior to sending it to a Go routine.
	Processor BatchProcessor[T]

	// ResumeAfterErr if true will continue listening and keep processing if the processor returns
	// an error, or if processor panics. In both cases, ProcessorErr would be executed
	ResumeAfterErr bool
	// ProcessorErr is the function which is executed if processor encounters an error or panic.
	// A panic is wrapped with ErrPanic. failedBatch is reused after it returns, and must not be retained.
	// A panic within ProcessorErr is recovered and discarded.
	ProcessorErr func(failedBatch []T, err error)

	// FlushOnStop if true processes pending items when Listen stops because its context is done,
	// instead of dropping them.
	FlushOnStop bool
}
```

A batch is processed with one of the triggers:

| Trigger | When |
|---|---|
| `TriggerFull` | the batch has `Size` items |
| `TriggerTicker` | `TickerDuration` has passed since the first item of the batch arrived |
| `TriggerStop` | pending items flushed after the listener stopped, only with `FlushOnStop` |

## How to use nibbler?

Below is an example showing how batching is used for a "banking" app which bulk processes account statements. It is also available as a runnable example in [example_test.go](example_test.go).

```golang
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/naughtygopher/nibbler"
)

type AccStatement struct {
	AccountID string
	Balance   int
}

// Bank bulk inserts account statements. Its fields are only accessed by the listener goroutine,
// and read after the listener is done.
type Bank struct {
	accounts     map[string]int
	totalBalance int
}

func (bnk *Bank) ProcessAccountsBatch(_ context.Context, _ nibbler.Trigger, batch []AccStatement) error {
	// assume this is a single bulk insert into the database, instead of one insert per statement
	for _, st := range batch {
		bnk.accounts[st.AccountID] = st.Balance
		bnk.totalBalance += st.Balance
	}
	return nil
}

func main() {
	bnk := &Bank{accounts: make(map[string]int)}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	nib, err := nibbler.Start(ctx, &nibbler.Config[AccStatement]{
		Size:           10,
		TickerDuration: time.Second,
		Processor:      bnk.ProcessAccountsBatch,
		// statements still pending when ctx is cancelled are processed instead of dropped
		FlushOnStop: true,
	})
	if err != nil {
		panic(err)
	}

	receiver := nib.Receiver()
	for i := range 95 {
		receiver <- AccStatement{
			AccountID: fmt.Sprintf("account_id_%d", i),
			Balance:   50000 / (i + 1),
		}
	}

	// stop producers first, then cancel and wait for the listener to finish
	cancel()
	<-nib.Done()

	fmt.Printf("accounts: %d, total balance: %d\n", len(bnk.accounts), bnk.totalBalance)
	fmt.Println("stopped:", nib.Err())
}
```

You can find all usage details in the tests.

## Shutdown

`Listen(ctx)` runs until `ctx` is done, and `Start(ctx, cfg)` runs `Listen` in a new goroutine. Cancelling `ctx` also cancels the context of the batch being processed.

1. Stop the producers
2. Cancel the context
3. Wait on `<-nib.Done()`, and use `nib.Err()` for the reason the listener stopped

Pending items are dropped by default. With `FlushOnStop`, the partial batch and the items buffered in the receiver at the time of stop are processed with `TriggerStop`. The flush is not cancelled by the listener's context, but every batch is still limited by `ProcessingTimeout`.

The receiver channel is never closed. If a producer may outlive the listener, select on `Done()` when sending:

```golang
select {
case nib.Receiver() <- item:
case <-nib.Done():
	// the listener has stopped
}
```

To run the listener within an existing worker pool, use `New` and `Listen` directly:

```golang
workers.Go(func() { _ = nib.Listen(ctx) })
```

## Benchmarks

End to end throughput, from pushing an item into the receiver until it is handed to a no-op processor. Intel i7-14700, Go 1.27.3.

| Batch size | ns/item | allocs/item |
|---|---|---|
| 1 | 616.5 | 4 |
| 10 | 113.3 | 0 |
| 100 | 35.7 | 0 |
| 1000 | 28.9 | 0 |
| 100, parallel producers | 61.2 | 0 |

The listener does not allocate per item. The allocations are per batch, for its context.

```bash
$ go test -run '^$' -bench . -benchmem ./...
```

## Migrating from v0.2

- `Start(cfg)` becomes `Start(ctx, cfg)`, and `Listen()` becomes `Listen(ctx) error`
- Use `Done()` and `Err()` to wait for the listener and get the reason it stopped
- The receiver is never closed. Select on `Done()` when sending, if the listener may stop
- `TickerDuration` defaults to 1s as documented, it was 1m
- The flush timer starts when the first item of a batch arrives, instead of ticking at a fixed interval
- Processor panics are wrapped with `ErrPanic`
- New: `QueueSize`, `FlushOnStop` & `TriggerStop`
- Go 1.25 or newer is required

## Design decisions

The reasoning behind nibbler's design is recorded as [architecture decision records](docs/adr/README.md).

## The gopher

The gopher used here was created using [Gopherize.me](https://gopherize.me/). Nibbler is out there eating your events/streams
one chunky bite at a time.
