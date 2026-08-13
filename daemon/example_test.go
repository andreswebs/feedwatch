package daemon_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/daemon"
)

// ExampleScheduler embeds background polling in-process: the scheduler wakes on a
// cadence, polls whatever the store says is due, and publishes each run on
// Events. The consumer must drain the channel, since publishing blocks. Stopping
// is the caller's job: cancel the context, and Run closes Events so the range
// terminates.
func ExampleScheduler() {
	app, err := feedwatch.New(feedwatch.Defaults())
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	s := daemon.New(app,
		daemon.WithInterval(5*time.Minute),
		daemon.WithPollOnStart(true),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = s.Run(ctx) }()

	for ev := range s.Events() {
		if ev.Err != nil && ev.Result.Polled == 0 {
			log.Printf("poll failed: %v", ev.Err)
			continue
		}
		fmt.Printf("%d new item(s) from %d feed(s)\n", ev.Result.NewItems, ev.Result.Polled)
		cancel()
	}
}
