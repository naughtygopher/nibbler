<p align="center"><img src="https://github.com/user-attachments/assets/1b34c21a-8031-43d3-a172-44e039b58190" alt="nibbler gopher" width="256px"/></p>

[![](https://github.com/naughtygopher/nibbler/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/naughtygopher/nibbler/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/naughtygopher/nibbler.svg)](https://pkg.go.dev/github.com/naughtygopher/nibbler)
[![Go Report Card](https://goreportcard.com/badge/github.com/naughtygopher/nibbler?cache_invalidate=1)](https://goreportcard.com/report/github.com/naughtygopher/nibbler)
[![Coverage Status](https://coveralls.io/repos/github/naughtygopher/nibbler/badge.svg?branch=main&cache_invalidate=1)](https://coveralls.io/github/naughtygopher/nibbler?branch=main)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/creativecreature/sturdyc/blob/master/LICENSE)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go#stream-processing)

# Nibbler

Nibbler is a resilient & minimal package which helps you implement micro-batch processing, _within an application_. Nibbler remains minimal with its 0 external dependencies and remains resilient within the context of the application by gracefully handling errors and panics.

IMPORTANT: This is not a general purpose _*distributed*_ task queue.

## What is Micro-batch Processing?

Micro-batch processing is a way to handle data by breaking a big task into smaller pieces and processing them one piece at a time. This method is useful in processing realtime data or stream processing, where the incoming data is split into "micro-batches" and processed quickly, rather than waiting to collect all data at once or process one by one.

The same concept can also be extended to handle events processing. So, we have a queue subscriber, and instead of processing the events individually, we create micro batches and process them.

The processing of a single micro batch can be triggered in two ways, based on a time ticker or if the micro batch size is full. i.e. process a non empty batch if duration X has passed or if the batch size is full

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
type BatchProcessor[T any] func(ctx context.Context, trigger trigger, batch []T) error

type Config[T any] struct {
	// ProcessingTimeout is context timeout for processing a single batch. If less than 1ms, defaults to 1s
	ProcessingTimeout time.Duration
	// TickerDuration is the duration after which a non-empty batch would be flushed. If less than 1ms, defaults to 1s
	TickerDuration time.Duration

	// Size is the micro batch size. If 0, defaults to 100
	Size uint

	// Processor is a required configuration, it is called when a batch process is initiated either by
	// ticker or when the batch is full. The 'batch' slice received must not be changed, if you have to
	// process the batch asynchronously, copy the batch to a new slice prior to sending it to a Go routine.
	Processor BatchProcessor[T]

	// ResumeAfterErr if true will continue listening and keep processing if the processor returns
	// an error, or if processor panics. In both cases, ProcessorErr would be executed
	ResumeAfterErr bool
	// ProcessorErr is the function which is executed if processor encounters an error or panic
	ProcessorErr func(failedBatch []T, err error)
}
```

## How to use nibbler?

Below is an example showing how batching is used for a "banking" app which bulk processes account statements.

```golang
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/naughtygopher/nibbler"
)

type db struct {
	data         sync.Map
	totalBalance int
}

func (d *db) BulkAddAccountsAndBalance(pp []AccStatement) error {
	// assume we are doing a bulk insert/update into the database instead of inserting one by one.
	// Bulk operations reduce the number of I/O required between your application and the database.
	// Thereby making it better in most cases.
	for _, p := range pp {
		d.data.Store(p.AccountID, p.Balance)
		d.totalBalance += p.Balance
	}
	return nil
}

type Bank struct {
	db *db
}

func (bnk *Bank) ProcessAccountsBatch(
	ctx context.Context,
	trigger nibbler.Trigger,
	batch []AccStatement,
) error {
	err := bnk.db.BulkAddAccountsAndBalance(batch)
	if err != nil {
		return err
	}

	return nil
}

func (bnk *Bank) TotalBalance() int {
	return bnk.db.totalBalance
}

func (bnk *Bank) TotalAccounts() int {
	counter := 0
	bnk.db.data.Range(func(key, value any) bool {
		counter++
		return true
	})
	return counter
}

type AccStatement struct {
	AccountID string
	Balance   int
}

func main() {
	bnk := Bank{
		db: &db{
			data: sync.Map{},
		},
	}

	nib, err := nibbler.Start(&nibbler.Config[AccStatement]{
		Size:           10,
		TickerDuration: time.Second,
		Processor:      bnk.ProcessAccountsBatch,
	})
	if err != nil {
		panic(err)
	}

	receiver := nib.Receiver()
	for i := range 100 {
		accID := fmt.Sprintf("account_id_%d", i)
		receiver <- AccStatement{
			AccountID: accID,
			Balance:   50000 / (i + 1),
		}
	}

	// wait for batches to be processed. Ideally this wouldn't be required as our application
	// would not exit, instead just keep listening to the events stream.
	time.Sleep(time.Second)

	fmt.Printf(
		"Number of accounts %d, total balance: %d\n",
		bnk.TotalAccounts(),
		bnk.TotalBalance(),
	)
}
```

You can find all usage details in the tests.

## The gopher

The gopher used here was created using [Gopherize.me](https://gopherize.me/). Nibbler is out there eating your events/streams
one chunky bite at a time.
