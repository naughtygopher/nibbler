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

In any high throughput event/stream processing, it is imperative to process them in batches instead of individually. Processing events in batches when done properly optimize usage of the downstream dependencies like databases, external systems (if they support) etc by significantly reducing [IOPS](https://en.wikipedia.org/wiki/IOPS). When deciding on how to process batches, it is important to still be able to process them realtime or near realtime. So, if we wait for a batch to be "full", and for any reason if the batch is not full fast enough, then processing would be indefinitely delayed. Hence the batches have to be flushed periodically, based on an acceptable tradeoff. The tradeoff in this case is, when the batch is not filled very fast, then we lose near realtime processing, rather would only be processed every N seconds/minute/duration.

### Config

```golang
type BatchProcessor[T any] func(ctx context.Context, trigger trigger, batch []T) error

type Config[T any] struct {
    // ProcessingTimeout is context timeout for processing a single batch
    ProcessingTimeout time.Duration
    // TickerDuration is the ticker duration, for when a non empty batch would be processed
    TickerDuration    time.Duration
    // Size is the micro batch size
    Size uint

    // Processor is the function which processes a single batch
    Processor BatchProcessor[T]

    // ResumeAfterErr if true will continue listening and keep processing if the processor returns
    // an error, or if processor panics. In both cases, ProcessorErr would be executed
    ResumeAfterErr bool
    // ProcessorErr is executed if the processor returns erorr or panics
    ProcessorErr   func(failedBatch []T, err error)
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
