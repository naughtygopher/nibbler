package nibbler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errProcessing = errors.New("failed processing")

// recorder records every processor and error callback invocation
type recorder[T any] struct {
	mu       sync.Mutex
	batches  [][]T
	triggers []Trigger
	failed   [][]T
	errs     []error
}

func (rec *recorder[T]) processor(err error) BatchProcessor[T] {
	return func(_ context.Context, trigger Trigger, batch []T) error {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.batches = append(rec.batches, slices.Clone(batch))
		rec.triggers = append(rec.triggers, trigger)
		return err
	}
}

func (rec *recorder[T]) processorErr(failedBatch []T, err error) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.failed = append(rec.failed, slices.Clone(failedBatch))
	rec.errs = append(rec.errs, err)
}

func (rec *recorder[T]) Batches() [][]T {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Clone(rec.batches)
}

func (rec *recorder[T]) Triggers() []Trigger {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Clone(rec.triggers)
}

func (rec *recorder[T]) Failed() ([][]T, []error) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Clone(rec.failed), slices.Clone(rec.errs)
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestNibbler(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		var (
			totalItems      = 18
			batchSize  uint = 6
			batchFreq       = time.Second
			rec             = &recorder[string]{}
		)

		ctx, cancel := context.WithCancel(t.Context())
		nib, err := Start(ctx, &Config[string]{
			Size:              batchSize,
			ProcessingTimeout: time.Millisecond,
			TickerDuration:    batchFreq,
			Processor:         rec.processor(nil),
		})
		require.NoError(t, err)

		receiver := nib.Receiver()
		for i := range totalItems {
			if (i % int(batchSize+1)) == 0 {
				// sleep to ensure batch is flushed by the ticker
				time.Sleep(batchFreq + (time.Millisecond * 100))
			}
			receiver <- fmt.Sprintf("i:%d", i)
		}
		time.Sleep(batchFreq * 2)
		synctest.Wait()

		cancel()
		<-nib.Done()

		expected := [][]string{
			{"i:0", "i:1", "i:2", "i:3", "i:4", "i:5"}, // big slices are produced when batch is full
			{"i:6"}, // single value slices are produced by time flush
			{"i:7", "i:8", "i:9", "i:10", "i:11", "i:12"},
			{"i:13"},
			{"i:14", "i:15", "i:16", "i:17"},
		}
		require.Equal(t, expected, rec.Batches())
		require.Equal(
			t,
			[]Trigger{TriggerFull, TriggerTicker, TriggerFull, TriggerTicker, TriggerTicker},
			rec.Triggers(),
		)
	})
}

func TestListenStopsOnCancel(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		nib, err := New(&Config[int]{Processor: (&recorder[int]{}).processor(nil)})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		listenErr := make(chan error, 1)
		go func() { listenErr <- nib.Listen(ctx) }()

		synctest.Wait()
		require.False(t, isClosed(nib.Done()))

		cancel()
		require.ErrorIs(t, <-listenErr, context.Canceled)
		require.True(t, isClosed(nib.Done()))
		require.ErrorIs(t, nib.Err(), context.Canceled)
	})
}

func TestListenCancelCause(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		errCause := errors.New("shutting down")
		ctx, cancel := context.WithCancelCause(t.Context())
		nib, err := Start(ctx, &Config[int]{Processor: (&recorder[int]{}).processor(nil)})
		require.NoError(t, err)

		cancel(errCause)
		<-nib.Done()
		require.ErrorIs(t, nib.Err(), errCause)
	})
}

func TestErrBeforeDone(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		nib, err := Start(ctx, &Config[int]{Processor: (&recorder[int]{}).processor(nil)})
		require.NoError(t, err)

		synctest.Wait()
		require.NoError(t, nib.Err())

		cancel()
		<-nib.Done()
		require.ErrorIs(t, nib.Err(), context.Canceled)
	})
}

func TestProcessorCtxCancelledByParent(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		rec := &recorder[int]{}
		ctx, cancel := context.WithCancel(t.Context())
		nib, err := Start(ctx, &Config[int]{
			Size:              1,
			ProcessingTimeout: time.Hour,
			ResumeAfterErr:    true,
			Processor: func(ctx context.Context, _ Trigger, _ []int) error {
				<-ctx.Done()
				return ctx.Err()
			},
			ProcessorErr: rec.processorErr,
		})
		require.NoError(t, err)

		nib.Receiver() <- 1
		synctest.Wait()

		cancel()
		<-nib.Done()

		failed, errs := rec.Failed()
		require.Equal(t, [][]int{{1}}, failed)
		require.Len(t, errs, 1)
		require.ErrorIs(t, errs[0], context.Canceled)
		require.ErrorIs(t, nib.Err(), context.Canceled)
	})
}

func TestProcessingTimeout(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		var (
			timeout  = 3 * time.Second
			deadline = make(chan time.Duration, 1)
			expired  = make(chan error, 1)
		)
		ctx, cancel := context.WithCancel(t.Context())
		nib, err := Start(ctx, &Config[int]{
			Size:              1,
			ProcessingTimeout: timeout,
			Processor: func(ctx context.Context, _ Trigger, _ []int) error {
				dl, ok := ctx.Deadline()
				require.True(t, ok)
				deadline <- time.Until(dl)
				<-ctx.Done()
				expired <- ctx.Err()
				return nil
			},
		})
		require.NoError(t, err)

		start := time.Now()
		nib.Receiver() <- 1
		require.Equal(t, timeout, <-deadline)
		require.ErrorIs(t, <-expired, context.DeadlineExceeded)
		require.Equal(t, timeout, time.Since(start))

		cancel()
		<-nib.Done()
	})
}

func TestListenTwice(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		rec := &recorder[int]{}
		ctx, cancel := context.WithCancel(t.Context())
		nib, err := Start(ctx, &Config[int]{Size: 1, Processor: rec.processor(nil)})
		require.NoError(t, err)
		synctest.Wait()

		require.ErrorIs(t, nib.Listen(ctx), ErrAlreadyListening)
		require.False(t, isClosed(nib.Done()))
		require.NoError(t, nib.Err())

		// first listener is unaffected
		nib.Receiver() <- 1
		synctest.Wait()
		require.Equal(t, [][]int{{1}}, rec.Batches())

		cancel()
		<-nib.Done()
	})
}

func TestStartDone(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		nib, err := Start(ctx, &Config[int]{Processor: (&recorder[int]{}).processor(nil)})
		require.NoError(t, err)

		cancel()
		<-nib.Done()
		require.ErrorIs(t, nib.Err(), context.Canceled)
	})
}

func TestReceiverNotClosed(tt *testing.T) {
	trySend := func(t *testing.T, nib *Nibbler[int]) {
		t.Helper()
		require.NotPanics(t, func() {
			select {
			case nib.Receiver() <- 1:
			default:
			}
		})
	}

	tt.Run("after cancel", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			nib, err := Start(ctx, &Config[int]{Processor: (&recorder[int]{}).processor(nil)})
			require.NoError(t, err)

			cancel()
			<-nib.Done()
			trySend(t, nib)
		})
	})

	tt.Run("after error without resume", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			nib, err := Start(t.Context(), &Config[int]{
				Size:      1,
				Processor: (&recorder[int]{}).processor(errProcessing),
			})
			require.NoError(t, err)

			nib.Receiver() <- 1
			<-nib.Done()
			require.ErrorIs(t, nib.Err(), errProcessing)
			trySend(t, nib)
		})
	})
}

func TestProcessorErr(tt *testing.T) {
	panicker := func(v any) BatchProcessor[string] {
		return func(_ context.Context, _ Trigger, _ []string) error {
			panic(v)
		}
	}

	cases := []struct {
		name      string
		resume    bool
		processor BatchProcessor[string]
		assertErr func(t *testing.T, err error)
	}{
		{
			name:      "call err processor without resume",
			processor: (&recorder[string]{}).processor(errProcessing),
			assertErr: func(t *testing.T, err error) { assert.ErrorIs(t, err, errProcessing) },
		},
		{
			name:      "call err processor with resume",
			resume:    true,
			processor: (&recorder[string]{}).processor(errProcessing),
			assertErr: func(t *testing.T, err error) { assert.ErrorIs(t, err, errProcessing) },
		},
		{
			name:      "panic recovery without resume",
			processor: panicker(errProcessing),
			assertErr: func(t *testing.T, err error) { assert.ErrorIs(t, err, errProcessing) },
		},
		{
			name:      "panic recovery with resume and non error panic",
			resume:    true,
			processor: panicker("processor panic"),
			assertErr: func(t *testing.T, err error) { assert.ErrorContains(t, err, "processor panic") },
		},
	}

	for _, tc := range cases {
		tt.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := &recorder[string]{}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				nib, err := Start(ctx, &Config[string]{
					Size:           1,
					Processor:      tc.processor,
					ProcessorErr:   rec.processorErr,
					ResumeAfterErr: tc.resume,
				})
				require.NoError(t, err)

				nib.Receiver() <- "hello"
				synctest.Wait()

				if !tc.resume {
					<-nib.Done()
					tc.assertErr(t, nib.Err())

					failed, errs := rec.Failed()
					require.Equal(t, [][]string{{"hello"}}, failed)
					tc.assertErr(t, errs[0])
					return
				}

				require.False(t, isClosed(nib.Done()))
				nib.Receiver() <- "again"
				synctest.Wait()

				failed, errs := rec.Failed()
				require.Equal(t, [][]string{{"hello"}, {"again"}}, failed)
				for _, err := range errs {
					tc.assertErr(t, err)
				}

				cancel()
				<-nib.Done()
			})
		})
	}
}

func TestDroppedOnStop(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		rec := &recorder[int]{}
		ctx, cancel := context.WithCancel(t.Context())
		nib, err := Start(ctx, &Config[int]{
			Size:           10,
			TickerDuration: time.Second,
			Processor:      rec.processor(nil),
		})
		require.NoError(t, err)

		for i := range 3 {
			nib.Receiver() <- i
		}
		synctest.Wait()

		cancel()
		<-nib.Done()
		time.Sleep(time.Minute)
		require.Empty(t, rec.Batches())
	})
}

func TestQueueSize(tt *testing.T) {
	processor := (&recorder[int]{}).processor(nil)

	tt.Run("defaults to size", func(t *testing.T) {
		nib, err := New(&Config[int]{Size: 7, Processor: processor})
		require.NoError(t, err)
		require.Equal(t, 7, cap(nib.Receiver()))
	})

	tt.Run("explicit", func(t *testing.T) {
		nib, err := New(&Config[int]{Size: 7, QueueSize: 1024, Processor: processor})
		require.NoError(t, err)
		require.Equal(t, 1024, cap(nib.Receiver()))
	})
}

func TestConfigCopied(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		rec := &recorder[int]{}
		cfg := &Config[int]{Size: 2, Processor: rec.processor(nil)}
		nib, err := New(cfg)
		require.NoError(t, err)

		// changes after New must have no effect on the Nibbler
		cfg.Size = 1
		cfg.Processor = nil

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = nib.Listen(ctx) }()

		nib.Receiver() <- 1
		nib.Receiver() <- 2
		synctest.Wait()
		require.Equal(t, [][]int{{1, 2}}, rec.Batches())

		cancel()
		<-nib.Done()
	})
}

func TestSanitizeValidate(tt *testing.T) {
	var (
		processor = func(ctx context.Context, trigger Trigger, batch []string) error { return nil }
	)

	tt.Run("all valid", func(t *testing.T) {
		asserter := assert.New(t)
		cfg := Config[string]{
			ProcessingTimeout: time.Minute,
			TickerDuration:    time.Second,
			Size:              10,
			QueueSize:         1000,

			Processor:      processor,
			ResumeAfterErr: false,
			ProcessorErr:   func(failedBatch []string, err error) {},
		}
		_, err := New(&cfg)
		asserter.NoError(err)
		asserter.EqualValues(time.Minute, cfg.ProcessingTimeout)
		asserter.EqualValues(time.Second, cfg.TickerDuration)
		asserter.EqualValues(10, cfg.Size)
		asserter.EqualValues(1000, cfg.QueueSize)
		asserter.False(cfg.ResumeAfterErr)
	})

	tt.Run("use defaults", func(t *testing.T) {
		asserter := assert.New(t)
		cfg := Config[string]{
			ProcessingTimeout: time.Nanosecond,
			TickerDuration:    time.Nanosecond,
			Size:              0,

			Processor:      processor,
			ProcessorErr:   nil,
			ResumeAfterErr: false,
		}
		_, err := New(&cfg)
		asserter.NoError(err)
		asserter.EqualValues(time.Second, cfg.ProcessingTimeout)
		asserter.EqualValues(time.Second, cfg.TickerDuration)
		asserter.EqualValues(100, cfg.Size)
		asserter.EqualValues(100, cfg.QueueSize)
		asserter.Nil(cfg.ProcessorErr)
		asserter.False(cfg.ResumeAfterErr)
	})

	tt.Run("invalid", func(t *testing.T) {
		asserter := assert.New(t)
		cfg := Config[string]{
			Processor: nil,
		}
		_, err := Start(t.Context(), &cfg)
		asserter.ErrorIs(err, ErrValidation)
	})
}
