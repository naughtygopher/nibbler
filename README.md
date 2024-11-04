<p align="center"><img src="https://github.com/user-attachments/assets/1b34c21a-8031-43d3-a172-44e039b58190" alt="nibbler gopher" width="256px"/></p>

[![](https://github.com/naughtygopher/nibbler/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/naughtygopher/nibbler/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/naughtygopher/nibbler.svg)](https://pkg.go.dev/github.com/naughtygopher/nibbler)
[![Go Report Card](https://goreportcard.com/badge/github.com/naughtygopher/nibbler)](https://goreportcard.com/report/github.com/naughtygopher/nibbler)
[![Coverage Status](https://coveralls.io/repos/github/naughtygopher/nibbler/badge.svg?branch=main)](https://coveralls.io/github/naughtygopher/nibbler?branch=main)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/creativecreature/sturdyc/blob/master/LICENSE)

# Nibbler

Nibbler is a resilient, minimal, package which helps you implement micro-batch processing.

## What is Micro-batch Processing?

Micro-batch processing is a way to handle data by breaking a big task into smaller pieces and processing them one by one. This method is useful in real-time data or streaming situations, wher ,the incoming data is split into "micro-batches" and processed quickly, rather than waiting to collect all data at once.

The same concept can also be extended to handle events processing. So, we have a queue subscriber, and instead of processing the events individually, we create micro batches and process them.

The processing of a single micro batch can be triggered in two ways, based on a time ticker or if the micro batch size is full. i.e. process a non empty batch if duration X has passed or if the batch size is full

## How to use nibbler?

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

You can find usage details in the tests.

## The gopher

The gopher used here was created using [Gopherize.me](https://gopherize.me/). Nibbler is out there eating your events/streams
one bite at a time.
