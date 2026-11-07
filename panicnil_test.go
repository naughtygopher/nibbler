//go:debug panicnil=1

package nibbler

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

// With panicnil=1, which is the default for modules declaring Go older than 1.21, recover
// returns nil for panic(nil). The panic must still be reported, and the batch must be released.
func TestPanicNil(tt *testing.T) {
	synctest.Test(tt, func(t *testing.T) {
		var (
			rec   = &recorder[int]{}
			calls = 0
		)
		ctx, cancel := context.WithCancel(t.Context())
		nib, err := Start(ctx, &Config[int]{
			Size:           1,
			ResumeAfterErr: true,
			Processor: func(_ context.Context, trigger Trigger, batch []int) error {
				calls++
				if calls == 1 {
					panic(nil)
				}
				return rec.processor(nil)(ctx, trigger, batch)
			},
			ProcessorErr: rec.processorErr,
		})
		require.NoError(t, err)

		nib.Receiver() <- 1
		nib.Receiver() <- 2
		synctest.Wait()

		failed, errs := rec.Failed()
		require.Equal(t, [][]int{{1}}, failed)
		require.Len(t, errs, 1)
		require.ErrorIs(t, errs[0], ErrPanic)
		// the failed batch is not processed again
		require.Equal(t, [][]int{{2}}, rec.Batches())

		cancel()
		<-nib.Done()
	})
}
