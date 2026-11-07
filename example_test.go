package nibbler_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/naughtygopher/nibbler"
)

type AccStatement struct {
	AccountID string
	Balance   int
}

// Bank bulk inserts account statements. Its fields are only accessed by the listener goroutine,
// and read after the listener is done.
type Bank struct {
	accounts     map[string]int
	totalBalance int
}

func (bnk *Bank) ProcessAccountsBatch(_ context.Context, _ nibbler.Trigger, batch []AccStatement) error {
	// assume this is a single bulk insert into the database, instead of one insert per statement
	for _, st := range batch {
		bnk.accounts[st.AccountID] = st.Balance
		bnk.totalBalance += st.Balance
	}
	return nil
}

func Example() {
	bnk := &Bank{accounts: make(map[string]int)}

	// in an application, this would be signal.NotifyContext(context.Background(), os.Interrupt)
	ctx, cancel := context.WithCancel(context.Background())

	nib, err := nibbler.Start(ctx, &nibbler.Config[AccStatement]{
		Size:           10,
		TickerDuration: time.Second,
		Processor:      bnk.ProcessAccountsBatch,
		// statements still pending when ctx is cancelled are processed instead of dropped
		FlushOnStop: true,
	})
	if err != nil {
		panic(err)
	}

	receiver := nib.Receiver()
	for i := range 95 {
		receiver <- AccStatement{
			AccountID: fmt.Sprintf("account_id_%d", i),
			Balance:   50000 / (i + 1),
		}
	}

	// stop producers first, then cancel and wait for the listener to finish
	cancel()
	<-nib.Done()

	fmt.Printf("accounts: %d, total balance: %d\n", len(bnk.accounts), bnk.totalBalance)
	fmt.Println("stopped:", nib.Err())
	// Output:
	// accounts: 95, total balance: 256775
	// stopped: context canceled
}

// The listener can be run and joined within an existing worker pool.
func ExampleNibbler_Listen() {
	var (
		processed   int
		ctx, cancel = context.WithCancel(context.Background())
		workers     sync.WaitGroup
	)

	nib, err := nibbler.New(&nibbler.Config[string]{
		Size:        100,
		QueueSize:   1000,
		FlushOnStop: true,
		Processor: func(_ context.Context, _ nibbler.Trigger, keys []string) error {
			processed += len(keys)
			return nil
		},
	})
	if err != nil {
		panic(err)
	}

	workers.Go(func() {
		err := nib.Listen(ctx)
		fmt.Println("listener stopped:", err)
	})

	for i := range 250 {
		nib.Receiver() <- fmt.Sprintf("key:%d", i)
	}

	cancel()
	workers.Wait()
	fmt.Println("processed:", processed)
	// Output:
	// listener stopped: context canceled
	// processed: 250
}
