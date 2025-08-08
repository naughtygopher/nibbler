// Package nibbler provides a simple interface for micro-batch processing
/*
Each micro batch can start processing when either of the conditions are fulfilled
1. When the ticker ticks
2. When the batch is "full"
*/
package nibbler

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrValidation = errors.New("validation failed")

type Trigger string

const (
	TriggerTicker Trigger = "TICKER"
	TriggerFull   Trigger = "BATCH_FULL"
)

type BatchProcessor[T any] func(ctx context.Context, trigger Trigger, batch []T) error

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

func (cfg *Config[T]) Sanitize() {
	if cfg.ProcessingTimeout < time.Millisecond {
		cfg.ProcessingTimeout = time.Second
	}

	if cfg.TickerDuration < time.Millisecond {
		cfg.TickerDuration = time.Minute
	}

	if cfg.Size == 0 {
		cfg.Size = 100
	}
}

func (cfg *Config[T]) Validate() error {
	if cfg.Processor == nil {
		return fmt.Errorf("batch processor cannot be empty:%w", ErrValidation)
	}

	return nil
}

func (cfg *Config[T]) SanitizeValidate() error {
	cfg.Sanitize()
	return cfg.Validate()
}

type Nibbler[T any] struct {
	cfg   *Config[T]
	batch []T
	queue chan T
}

func (bat *Nibbler[T]) panicRecovery(rec any, err error) error {
	if err != nil {
		return err
	}

	if rec == nil {
		return nil
	}

	err, iserror := rec.(error)
	if !iserror {
		err = fmt.Errorf("%+v", rec)
	}

	return err
}

func (bat *Nibbler[T]) processBatch(trigger Trigger) (err error) {
	defer func() {
		err = bat.panicRecovery(recover(), err)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), bat.cfg.ProcessingTimeout)
	defer cancel()

	err = bat.cfg.Processor(ctx, trigger, bat.batch)
	if err != nil {
		return err
	}

	// Batch is reset after successfully processing it
	// explicit reset to reuse already allocated memory, and also not to accidentally
	// use items from previous batch.
	bat.batch = bat.batch[:0]

	return err
}

// Receiver returns a write only channel for pushing items to the batch processor
func (bat *Nibbler[T]) Receiver() chan<- T {
	return bat.queue
}

// Listen listens to the receiver channel for processing the micro batches
func (bat *Nibbler[T]) Listen() {
	var (
		ticker = time.NewTicker(bat.cfg.TickerDuration)
		size   = int(bat.cfg.Size)
	)

	defer func() {
		// release all resources, just in case
		ticker.Stop()
		close(bat.queue)
	}()

	for {
		if err := bat.listener(ticker, size); err == nil {
			continue
		}

		if !bat.cfg.ResumeAfterErr {
			break
		}

		// The batch is reset to go past the failed batch if resume after error is enabled.
		// There's no advantage of keeping the failed batch if resume is enabled
		bat.batch = bat.batch[:0]
	}
}

func (bat *Nibbler[T]) listener(ticker *time.Ticker, size int) (err error) {
	select {
	case <-ticker.C:
		// process non empty batch
		if len(bat.batch) > 0 {
			err = bat.processBatch(TriggerTicker)
		}

	case value := <-bat.queue:
		bat.batch = append(bat.batch, value)
		// process batch immediately if full, instead of waiting for ticker
		if len(bat.batch) >= size {
			err = bat.processBatch(TriggerFull)
		}
	}

	if err != nil && bat.cfg.ProcessorErr != nil {
		bat.cfg.ProcessorErr(bat.batch, err)
	}

	return err
}

func New[T any](cfg *Config[T]) (*Nibbler[T], error) {
	err := cfg.SanitizeValidate()
	if err != nil {
		return nil, err
	}

	return &Nibbler[T]{
		cfg:   cfg,
		batch: make([]T, 0, cfg.Size),
		queue: make(chan T, cfg.Size),
	}, nil
}

func Start[T any](cfg *Config[T]) (*Nibbler[T], error) {
	bat, err := New(cfg)
	if err != nil {
		return nil, err
	}

	go bat.Listen()

	return bat, nil
}
