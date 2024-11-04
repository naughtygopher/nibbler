package nibbler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNibbler(tt *testing.T) {
	var (
		requirer         = require.New(tt)
		totaliItems      = 18
		batchSize   uint = 6
		strfmt           = "i:%d"
		lastval          = fmt.Sprintf(strfmt, totaliItems-1)
		batchFreq        = time.Second
		gotchan          = make(chan []string)
		done             = make(chan struct{})
	)

	bat, err := New(&Config[string]{
		Size:              batchSize,
		ProcessingTimeout: time.Millisecond,
		TickerDuration:    batchFreq,
		Processor: func(_ context.Context, _ trigger, batch []string) error {
			gotchan <- slices.Clone(batch)

			// last val is used to identify the end of all batches so that
			// we can get a relatively deterministic complete list of batches
			if batch[len(batch)-1] == lastval {
				done <- struct{}{}
			}
			return nil
		},
	})
	requirer.NoError(err)

	go func() {
		started := make(chan struct{})
		go func() {
			started <- struct{}{}
			bat.Listen()
		}()

		<-started

		receiver := bat.Receiver()
		for i := 0; i < totaliItems; i++ {
			str := fmt.Sprintf(strfmt, i)
			if (i % int(batchSize+1)) == 0 {
				// sleep to ensure batch is flushed, so as to create a relatively deterministic batch
				time.Sleep(batchFreq + (time.Millisecond * 100))
			}
			receiver <- str
		}
	}()

	received := make([][]string, 0, batchSize)
	processingDone := false
	for !processingDone {
		select {
		case list := <-gotchan:
			received = append(received, list)
		case <-done:
			processingDone = true
		}
	}

	expected := [][]string{
		{"i:0", "i:1", "i:2", "i:3", "i:4", "i:5"}, // big slices are produced when batch is full
		{"i:6"}, // single value slices are produced by time flush
		{"i:7", "i:8", "i:9", "i:10", "i:11", "i:12"},
		{"i:13"},
		{"i:14", "i:15", "i:16", "i:17"},
	}
	requirer.EqualValues(expected, received)
}

func TestProcessorErr(tt *testing.T) {
	requirer := require.New(tt)
	errProcessing := errors.New("failed processing")

	tt.Run("call err processor without resume", func(t *testing.T) {
		asserter := assert.New(t)
		receivedErr := make(chan bool, 2)
		defer func() {
			rec := recover()
			// if resume is disabled, then trying to push a new item to the
			// receiver will panic with send on closed channel
			asserter.NotNil(rec)
			asserter.Contains(rec, "send on closed channel")
		}()

		nib, err := Start(&Config[string]{
			TickerDuration: time.Second,
			Processor: func(_ context.Context, _ trigger, _ []string) error {
				return errProcessing
			},
			ProcessorErr: func(failedBatch []string, err error) {
				asserter.ErrorIs(err, errProcessing)
				asserter.ElementsMatch([]string{"hello"}, failedBatch)
				receivedErr <- true
			},
		})
		requirer.NoError(err)

		receiver := nib.Receiver()
		receiver <- "hello"
		asserter.True(<-receivedErr)
		receiver <- "again"
	})

	tt.Run("call err processor with resume", func(t *testing.T) {
		asserter := assert.New(t)
		receivedErr := make(chan bool, 2)
		defer func() {
			rec := recover()
			// if resume is enabled, then trying to push a new item to the
			// receiver will not panic as the channel is not closed and rec would be nil
			asserter.Nil(rec)
		}()
		assertElement := "hello"
		nib, err := Start(&Config[string]{
			TickerDuration: time.Second,
			Processor: func(_ context.Context, _ trigger, _ []string) error {
				return errProcessing
			},
			ProcessorErr: func(failedBatch []string, err error) {
				asserter.ErrorIs(err, errProcessing)
				asserter.ElementsMatch([]string{assertElement}, failedBatch)
				receivedErr <- true
			},
			ResumeAfterErr: true,
		})
		requirer.NoError(err)

		receiver := nib.Receiver()
		receiver <- assertElement
		asserter.True(<-receivedErr)
		assertElement = "again"
		receiver <- assertElement
	})

	tt.Run("panic recovery without resume", func(t *testing.T) {
		asserter := assert.New(t)
		receivedErr := make(chan bool, 2)
		defer func() {
			rec := recover()
			// if resume is disabled, then trying to push a new item to the
			// receiver will panic with send on closed channel
			asserter.NotNil(rec)
			asserter.Contains(rec, "send on closed channel")
		}()

		nib, err := Start(&Config[string]{
			TickerDuration: time.Second,
			Processor: func(_ context.Context, _ trigger, _ []string) error {
				panic(errProcessing)
			},
			ProcessorErr: func(failedBatch []string, err error) {
				asserter.ErrorIs(err, errProcessing)
				asserter.ElementsMatch([]string{"hello"}, failedBatch)
				receivedErr <- true
			},
			ResumeAfterErr: false,
		})
		requirer.NoError(err)

		receiver := nib.Receiver()
		receiver <- "hello"
		asserter.True(<-receivedErr)
		receiver <- "again"
	})

	tt.Run("panic recovery with resume", func(t *testing.T) {
		receivedErr := make(chan bool, 2)
		asserter := assert.New(t)

		defer func() {
			rec := recover()
			// if resume is enabled, then trying to push a new item to the
			// receiver will not panic as the channel is not closed and rec would be nil
			asserter.Nil(rec, "panic recovery with resume")
		}()

		assertElement := "hello"
		nib, err := Start(&Config[string]{
			TickerDuration: time.Second,
			Processor: func(_ context.Context, _ trigger, _ []string) error {
				panic(errProcessing)
			},
			ProcessorErr: func(failedBatch []string, err error) {
				asserter.ErrorIs(err, errProcessing, "panic recovery with resume")
				asserter.ElementsMatch([]string{assertElement}, failedBatch, "panic recovery with resume")
				receivedErr <- true
			},
			ResumeAfterErr: true,
		})
		requirer.NoError(err, "panic recovery with resume")

		receiver := nib.Receiver()
		receiver <- assertElement
		asserter.True(<-receivedErr, "panic recovery with resume")
		assertElement = "again"
		receiver <- assertElement
	})
}

func TestSanitizeValidate(tt *testing.T) {
	var (
		processor = func(ctx context.Context, trigger trigger, batch []string) error { return nil }
	)

	tt.Run("all valid", func(t *testing.T) {
		asserter := assert.New(t)
		cfg := Config[string]{
			ProcessingTimeout: time.Minute,
			TickerDuration:    time.Second,
			Size:              10,

			Processor:      processor,
			ResumeAfterErr: false,
			ProcessorErr:   func(failedBatch []string, err error) {},
		}
		_, err := New(&cfg)
		asserter.NoError(err)
		asserter.EqualValues(time.Minute, cfg.ProcessingTimeout)
		asserter.EqualValues(time.Second, cfg.TickerDuration)
		asserter.EqualValues(10, cfg.Size)
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
		asserter.EqualValues(time.Minute, cfg.TickerDuration)
		asserter.EqualValues(100, cfg.Size)
		asserter.Nil(cfg.ProcessorErr)
		asserter.False(cfg.ResumeAfterErr)
	})

	tt.Run("invalid", func(t *testing.T) {
		asserter := assert.New(t)
		cfg := Config[string]{
			Processor: nil,
		}
		_, err := Start(&cfg)
		asserter.ErrorIs(err, ErrValidation)
	})
}
