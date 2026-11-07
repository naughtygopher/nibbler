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
	"sync/atomic"
	"time"
)

var (
	ErrValidation = errors.New("validation failed")
	// ErrAlreadyListening is returned by Listen if it is called more than once on the same Nibbler
	ErrAlreadyListening = errors.New("nibbler is already listening")
	// ErrPanic wraps the value recovered from a panicking processor. If the panic value is an
	// error, it is wrapped as well, so both errors.Is(err, ErrPanic) and errors.Is(err, value) hold.
	ErrPanic = errors.New("processor panicked")
)

type Trigger string

const (
	TriggerTicker Trigger = "TICKER"
	TriggerFull   Trigger = "BATCH_FULL"
	// TriggerStop is used for batches flushed after the listener is stopped, see Config.FlushOnStop
	TriggerStop Trigger = "STOP"
)

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
	// ProcessorErr is the function which is executed if processor encounters an error or panic
	ProcessorErr func(failedBatch []T, err error)

	// FlushOnStop if true processes pending items when Listen stops because its context is done,
	// instead of dropping them. Pending items are the partial batch and the items buffered in the
	// receiver at the time of stop, they're processed with TriggerStop. The flush is not cancelled
	// by the listener's context, but every batch is still limited by ProcessingTimeout. Errors are
	// reported to ProcessorErr and flushing continues irrespective of ResumeAfterErr. A batch which
	// already failed is not retried. Stop producers before cancelling, items sent during the flush
	// are dropped.
	FlushOnStop bool
}

func (cfg *Config[T]) Sanitize() {
	if cfg.ProcessingTimeout < time.Millisecond {
		cfg.ProcessingTimeout = time.Second
	}

	if cfg.TickerDuration < time.Millisecond {
		cfg.TickerDuration = time.Second
	}

	if cfg.Size == 0 {
		cfg.Size = 100
	}

	if cfg.QueueSize == 0 {
		cfg.QueueSize = cfg.Size
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
	cfg       Config[T]
	batch     []T
	queue     chan T
	done      chan struct{}
	err       error
	listening atomic.Bool
}

func panicErr(rec any) error {
	if err, ok := rec.(error); ok {
		return fmt.Errorf("%w: %w", ErrPanic, err)
	}
	return fmt.Errorf("%w: %v", ErrPanic, rec)
}

func (bat *Nibbler[T]) processBatch(ctx context.Context, trigger Trigger) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = panicErr(rec)
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, bat.cfg.ProcessingTimeout)
	defer cancel()

	err = bat.cfg.Processor(ctx, trigger, bat.batch)
	if err != nil {
		return err
	}

	bat.reset()

	return err
}

// reset empties the batch while reusing its memory. Items are zeroed first, otherwise the
// backing array keeps already processed items reachable and they cannot be garbage collected.
func (bat *Nibbler[T]) reset() {
	clear(bat.batch)
	bat.batch = bat.batch[:0]
}

// Receiver returns a write only channel for pushing items to the batch processor.
// The channel is never closed, if the listener may stop, select on Done when sending.
func (bat *Nibbler[T]) Receiver() chan<- T {
	return bat.queue
}

// Done returns a channel which is closed when Listen returns
func (bat *Nibbler[T]) Done() <-chan struct{} {
	return bat.done
}

// Err returns nil until Done is closed. After that, it returns the reason Listen stopped.
func (bat *Nibbler[T]) Err() error {
	select {
	case <-bat.done:
		return bat.err
	default:
		return nil
	}
}

// Listen listens to the receiver channel for processing the micro batches. It blocks until
// ctx is done, or until the processor fails while ResumeAfterErr is disabled. It returns
// context.Cause(ctx) or the processor error respectively. Items pending at stop are dropped,
// unless FlushOnStop is enabled. Listen must be called at most once per Nibbler, subsequent
// calls return ErrAlreadyListening.
func (bat *Nibbler[T]) Listen(ctx context.Context) error {
	if !bat.listening.CompareAndSwap(false, true) {
		return ErrAlreadyListening
	}
	// err is written before done is closed, so reading it after <-done is race free
	defer close(bat.done)

	bat.err = bat.listen(ctx)
	if bat.cfg.FlushOnStop && ctx.Err() != nil {
		bat.flush(context.WithoutCancel(ctx))
	}
	// remaining items are dropped, release them
	bat.reset()

	return bat.err
}

func (bat *Nibbler[T]) listen(ctx context.Context) error {
	// the flush timer is armed only while the batch is non-empty, it starts when the first
	// item of a batch arrives. So an idle listener never wakes up, and no item waits for
	// longer than TickerDuration.
	timer := time.NewTimer(bat.cfg.TickerDuration)
	timer.Stop()
	defer timer.Stop()

	size := int(bat.cfg.Size)
	for {
		// select picks randomly among ready cases, this check gives cancellation
		// priority over a ready queue
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}

		err := bat.listener(ctx, timer, size)
		if err == nil {
			continue
		}

		// The failed batch is reset after it is reported, it is neither retried nor flushed on stop
		bat.reset()
		if !bat.cfg.ResumeAfterErr {
			return err
		}
	}
}

// flush processes the partial batch and the items buffered in the receiver. It is bounded by
// the number of buffered items when it starts, so producers still sending cannot prolong it.
func (bat *Nibbler[T]) flush(ctx context.Context) {
	size := int(bat.cfg.Size)
	for range len(bat.queue) {
		bat.batch = append(bat.batch, <-bat.queue)
		if len(bat.batch) >= size {
			bat.flushBatch(ctx)
		}
	}

	if len(bat.batch) > 0 {
		bat.flushBatch(ctx)
	}
}

func (bat *Nibbler[T]) flushBatch(ctx context.Context) {
	err := bat.processBatch(ctx, TriggerStop)
	if err != nil {
		bat.reportErr(err)
		bat.reset()
	}
}

func (bat *Nibbler[T]) listener(ctx context.Context, timer *time.Timer, size int) (err error) {
	select {
	case <-ctx.Done():
		return nil

	case <-timer.C:
		// unreachable with Go 1.23+ timers, unless the main module sets GODEBUG=asynctimerchan=1,
		// where a stale tick can be delivered after Stop
		if len(bat.batch) == 0 {
			return nil
		}
		err = bat.processBatch(ctx, TriggerTicker)

	case value := <-bat.queue:
		bat.batch = append(bat.batch, value)
		if len(bat.batch) < size {
			if len(bat.batch) == 1 {
				timer.Reset(bat.cfg.TickerDuration)
			}
			return nil
		}
		// process batch immediately if full, instead of waiting for the timer
		timer.Stop()
		err = bat.processBatch(ctx, TriggerFull)
	}

	if err != nil {
		bat.reportErr(err)
	}

	return err
}

func (bat *Nibbler[T]) reportErr(err error) {
	if bat.cfg.ProcessorErr == nil {
		return
	}
	// a panicking error handler must not kill the listener, and there is nowhere else to report it
	defer func() { _ = recover() }()

	bat.cfg.ProcessorErr(bat.batch, err)
}

// New validates the config and returns a Nibbler which is not listening yet. The config is
// sanitized in place, and a copy is retained, so changing cfg after New has no effect.
func New[T any](cfg *Config[T]) (*Nibbler[T], error) {
	err := cfg.SanitizeValidate()
	if err != nil {
		return nil, err
	}

	return &Nibbler[T]{
		cfg:   *cfg,
		batch: make([]T, 0, cfg.Size),
		queue: make(chan T, cfg.QueueSize),
		done:  make(chan struct{}),
	}, nil
}

// Start is New followed by `go Listen(ctx)`. Use Done to wait for the listener to stop
// and Err to get the reason.
func Start[T any](ctx context.Context, cfg *Config[T]) (*Nibbler[T], error) {
	bat, err := New(cfg)
	if err != nil {
		return nil, err
	}

	go func() {
		_ = bat.Listen(ctx)
	}()

	return bat, nil
}
