package nibbler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// benchNibbler starts a Nibbler which only flushes full batches, and returns a function
// which pads the stream to a multiple of size and waits until all items are processed.
func benchNibbler(b *testing.B, size uint) (*Nibbler[int], func(sent int)) {
	b.Helper()

	var (
		processed atomic.Int64
		target    atomic.Int64
		done      = make(chan struct{})
	)
	target.Store(-1)

	ctx, cancel := context.WithCancel(b.Context())
	b.Cleanup(cancel)

	nib, err := Start(ctx, &Config[int]{
		Size:           size,
		TickerDuration: time.Hour,
		Processor: func(_ context.Context, _ Trigger, batch []int) error {
			if processed.Add(int64(len(batch))) == target.Load() {
				close(done)
			}
			return nil
		},
	})
	if err != nil {
		b.Fatal(err)
	}

	wait := func(sent int) {
		receiver := nib.Receiver()
		total := sent
		if rem := sent % int(size); rem != 0 {
			total += int(size) - rem
		}
		target.Store(int64(total))
		for i := sent; i < total; i++ {
			receiver <- i
		}
		if processed.Load() == int64(total) {
			return
		}
		<-done
	}

	return nib, wait
}

func BenchmarkThroughput(b *testing.B) {
	for _, size := range []uint{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			nib, wait := benchNibbler(b, size)
			receiver := nib.Receiver()

			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				receiver <- i
			}
			wait(b.N)
		})
	}
}

func BenchmarkThroughputParallel(b *testing.B) {
	for _, size := range []uint{100} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			var sent atomic.Int64
			nib, wait := benchNibbler(b, size)
			receiver := nib.Receiver()

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					receiver <- int(sent.Add(1))
				}
			})
			wait(int(sent.Load()))
		})
	}
}
