package nibbler_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/naughtygopher/nibbler"
)

const stressRounds = 300

// stressSeed returns the seed for a test. It is logged, and can be set with NIBBLER_STRESS_SEED
// to reproduce a failure.
func stressSeed(t *testing.T) uint64 {
	t.Helper()
	seed := uint64(time.Now().UnixNano())
	if env := os.Getenv("NIBBLER_STRESS_SEED"); env != "" {
		parsed, err := strconv.ParseUint(env, 10, 64)
		if err != nil {
			t.Fatalf("invalid NIBBLER_STRESS_SEED: %v", err)
		}
		seed = parsed
	}
	t.Logf("seed: %d, reproduce with NIBBLER_STRESS_SEED=%d", seed, seed)
	return seed
}

// The producer of an item is encoded in it, so that ordering can be checked per producer.
const producerStride = 1_000_000

// TestStressExactlyOnce checks the delivery guarantee: with FlushOnStop, and producers stopped
// before the context is cancelled, every item sent is handed to the processor exactly once,
// irrespective of processor errors, panics and panics in ProcessorErr.
func TestStressExactlyOnce(tt *testing.T) {
	if testing.Short() {
		tt.Skip("stress test")
	}
	seed := stressSeed(tt)

	for round := range stressRounds {
		var (
			// cfgRnd is only used by this goroutine, procRnd only by the listener goroutine
			cfgRnd      = rand.New(rand.NewPCG(seed, uint64(round)))
			procRnd     = rand.New(rand.NewPCG(seed^0xdeadbeef, uint64(round)))
			size        = uint(cfgRnd.IntN(50) + 1)
			queueSize   = uint(cfgRnd.IntN(200))
			producers   = cfgRnd.IntN(8) + 1
			perProducer = cfgRnd.IntN(2000)
			tick        = time.Duration(cfgRnd.IntN(5)+1) * time.Millisecond
			// written by the processor, which is single goroutine, and read after Done is closed
			got       []int
			oversized int
		)
		desc := func() string {
			return "round " + strconv.Itoa(round) + ", seed " + strconv.FormatUint(seed, 10)
		}

		ctx, cancel := context.WithCancel(context.Background())
		nib, err := nibbler.Start(ctx, &nibbler.Config[int]{
			Size:              size,
			QueueSize:         queueSize,
			TickerDuration:    tick,
			ProcessingTimeout: time.Second,
			ResumeAfterErr:    true,
			FlushOnStop:       true,
			Processor: func(_ context.Context, _ nibbler.Trigger, batch []int) error {
				got = append(got, batch...)
				if len(batch) > int(size) {
					oversized++
				}
				switch procRnd.IntN(20) {
				case 0:
					return errors.New("failed")
				case 1:
					panic("panicked")
				}
				return nil
			},
			ProcessorErr: func(_ []int, _ error) {
				if procRnd.IntN(10) == 0 {
					panic("panic in ProcessorErr")
				}
			},
		})
		if err != nil {
			cancel()
			tt.Fatalf("%s: %v", desc(), err)
		}

		var wg sync.WaitGroup
		for producer := range producers {
			wg.Go(func() {
				for i := range perProducer {
					nib.Receiver() <- producer*producerStride + i
				}
			})
		}
		wg.Wait()
		cancel()

		select {
		case <-nib.Done():
		case <-time.After(10 * time.Second):
			tt.Fatalf("%s: listener did not stop", desc())
		}

		if !errors.Is(nib.Err(), context.Canceled) {
			tt.Fatalf("%s: Err() = %v, want context.Canceled", desc(), nib.Err())
		}
		if oversized > 0 {
			tt.Fatalf("%s: %d batches larger than size %d", desc(), oversized, size)
		}

		want := producers * perProducer
		if len(got) != want {
			tt.Fatalf("%s: processed %d items, sent %d (size=%d queue=%d)", desc(), len(got), want, size, queueSize)
		}

		next := make([]int, producers)
		for _, v := range got {
			producer, i := v/producerStride, v%producerStride
			if i != next[producer] {
				tt.Fatalf("%s: producer %d: got item %d, want %d, lost, duplicated or out of order", desc(), producer, i, next[producer])
			}
			next[producer]++
		}
	}
}

// TestStressCancelMidStream cancels while producers are still sending. Items may be dropped,
// but none may be processed twice, and the listener must stop and not block its producers.
func TestStressCancelMidStream(tt *testing.T) {
	if testing.Short() {
		tt.Skip("stress test")
	}
	seed := stressSeed(tt)

	for round := range stressRounds {
		var (
			cfgRnd = rand.New(rand.NewPCG(seed, uint64(round)))
			// written by the processor, which is single goroutine, and read after Done is closed
			seen = map[int]int{}
		)

		ctx, cancel := context.WithCancel(context.Background())
		nib, err := nibbler.Start(ctx, &nibbler.Config[int]{
			Size:           uint(cfgRnd.IntN(20) + 1),
			QueueSize:      uint(cfgRnd.IntN(50)),
			TickerDuration: time.Millisecond,
			FlushOnStop:    cfgRnd.IntN(2) == 0,
			ResumeAfterErr: true,
			Processor: func(ctx context.Context, _ nibbler.Trigger, batch []int) error {
				for _, v := range batch {
					seen[v]++
				}
				return ctx.Err()
			},
		})
		if err != nil {
			cancel()
			tt.Fatalf("round %d, seed %d: %v", round, seed, err)
		}

		var wg sync.WaitGroup
		for producer := range 4 {
			wg.Go(func() {
				for i := 0; ; i++ {
					select {
					case nib.Receiver() <- producer*producerStride + i:
					case <-nib.Done():
						return
					}
				}
			})
		}

		time.Sleep(time.Duration(cfgRnd.IntN(3000)) * time.Microsecond)
		cancel()

		select {
		case <-nib.Done():
		case <-time.After(10 * time.Second):
			tt.Fatalf("round %d, seed %d: listener did not stop", round, seed)
		}
		wg.Wait()

		for v, n := range seen {
			if n != 1 {
				tt.Fatalf("round %d, seed %d: item %d processed %d times", round, seed, v, n)
			}
		}
	}
}
