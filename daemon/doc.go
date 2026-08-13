// Package daemon is feedwatch's embeddable poll scheduler: a loop that calls
// App.Poll on a cadence and publishes each run's outcome on a channel.
//
// The scheduler is not a layer beneath the frontends
// (docs/adr/0007-library-and-frontends.md): it is another consumer of App. A TUI
// embeds it for live updates, an HTTP server embeds it next to its handlers, and
// neither becomes the core. The App belongs to the embedder, so the scheduler
// never closes it, exactly as an App never closes an injected store.
//
//	s := daemon.New(app, daemon.WithInterval(5*time.Minute))
//	go func() { _ = s.Run(ctx) }()
//	for ev := range s.Events() {
//		if ev.Err != nil {
//			log.Printf("poll failed: %v", ev.Err)
//			continue
//		}
//		log.Printf("%d new item(s)", ev.Result.NewItems)
//	}
//
// # The interval is a wake cadence
//
// WithInterval controls only how often the scheduler asks what is due; it is not
// a per-feed poll interval. feedwatch already schedules per feed: the store
// selects the feeds whose next-due time has passed, and per-feed intervals and
// failure backoff decide that time. The scheduler therefore polls with an empty
// request and never forces, so per-feed politeness and backoff stay in effect. A
// scheduler that forced every feed on every tick would hammer publishers and
// defeat that machinery.
//
// # Runs never overlap
//
// Run owns its loop and spawns no goroutine that outlives the call; the caller
// decides whether to run it in a goroutine. A tick that arrives while a poll is
// in flight is received and dropped, never queued, so a poll slower than the
// cadence cannot stack runs behind itself.
//
// # Publishing blocks, and failures do not stop the loop
//
// Each outcome is published with a blocking send, so a slow consumer slows the
// scheduler rather than losing events: a consumer must drain Events. A poll
// failure is published in Event.Err and the loop continues to the next tick,
// since a transient store or network failure must not silently kill background
// polling; whether to stop is the embedder's decision.
//
// Run returns ctx.Err() once the context is done, and closes the event channel
// before returning, so a consumer ranging over Events terminates. On
// cancellation mid-poll it publishes the final partial envelope if that can be
// done without blocking. A Scheduler is single-use: because Run closes the
// channel, a second Run returns ErrAlreadyRunning.
//
// For the stability commitment this package carries, see the feedwatch package
// documentation.
package daemon
