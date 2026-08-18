package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/notify"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// notifierBuffer is the event backlog the notifier may fall behind by before the bus drops it.
//
// 256, matching every other long-lived subscriber in this daemon. The runner's work per event is a
// map lookup and a few guard clauses, so it is not a plausible slow consumer; the buffer is here for
// the burst a device reattaching produces, not for a steady rate.
const notifierBuffer = 256

// startNotifier wires the qn.12 notifier into the running daemon and starts it, or does nothing when
// there is nothing to notify with.
//
// IT IS THE LAST PIECE OF THE RUNG AND THE ONE THAT MAKES IT AUTOMATIC. Every part below this line
// has been shipped and tested for weeks; until something constructed a `notify.Runner`, a quince
// install could subscribe a phone, send itself a test, and then never hear from the daemon again.
//
// NIL PUSH SERVICE MEANS DEMO MODE and is not an error: the demo fabricates its devices, so a
// reminder about one would be a notification about something that does not exist.
func startNotifier(ctx context.Context, log *slog.Logger, b *bus.Bus, cfgSvc *config.Service,
	st *store.Store, devices notify.Devices, jobs notify.Jobs, deliver notify.Deliverer) {
	// A DAEMON RUNNING WITH THE NOTIFIER OFF SAYS SO. Every early return here is a degraded mode in
	// the sense the hard rule means: the feature is installed, the UI offers it, and nothing will
	// ever arrive. It was silent until a staging deploy could not answer "is the notifier live?"
	// from anything the daemon emitted — an operator's only signal would have been a notification
	// that never came, hours later.
	//
	// INFO rather than WARN: `--demo` and a muxerless install are both correct configurations, not
	// faults. What is wrong is not knowing which one you are in.
	switch {
	case deliver == nil:
		log.Info("notify: notifier not started — no push service (demo mode); no notification will be sent")
		return
	case devices == nil:
		log.Info("notify: notifier not started — no device registry; no notification will be sent")
		return
	case jobs == nil:
		log.Info("notify: notifier not started — no backup engine, so no muxer is configured; " +
			"no notification will be sent")
		return
	}
	r := &notify.Runner{
		Log:       log,
		Devices:   devices,
		Jobs:      jobs,
		Reminders: st,
		Deliver:   deliver,
		// READ LIVE, NOT CAPTURED. Turning a category off in the UI must take effect on the next
		// notification, not the next restart (D12) — and the runner asks this on every evaluation,
		// so a closure is all it takes.
		Config: func() config.NotificationsConfig { return cfgSvc.Current().Notifications },
		Now:    time.Now,
	}
	events := make(chan wire.Envelope, notifierBuffer)
	go forwardEvents(ctx, log, b, events)
	// THE POSITIVE SIGNAL, and it is what makes the negatives above readable. One INFO line at
	// startup is the only thing that distinguishes "the notifier is running and nothing was due"
	// from "the notifier never started" — and those look identical from a phone that stayed quiet.
	log.Info("notify: notifier started", "tick", notify.TickInterval.String())
	go r.Run(ctx, events)
}

// forwardEvents pumps the bus into the notifier's channel, owning the subscription so the runner
// does not have to.
//
// THE RUNNER TAKES A PLAIN CHANNEL, and keeping it that way is worth one goroutine. `notify` depends
// on `wire`, `config` and `backup` and on no part of the daemon's plumbing, which is what lets its
// tests drive nine behaviours by writing envelopes into a channel. Handing it a `*bus.Subscription`
// would buy nothing and cost that.
//
// A DROP IS RECOVERED BY RE-EVALUATING EVERYTHING, not by pretending it did not happen. The bus
// drops a subscriber that falls behind rather than blocking the publisher, so the honest response to
// a drop is "I do not know what I missed" — and `device.updated` is already defined to re-evaluate
// the whole fleet, so a synthetic one is exactly the recovery and not a trick.
//
// WHAT IS GENUINELY LOST IS THE TERMINAL JOBS IN THE GAP: a failure that happened while the
// subscription was overflowing is not recoverable from device state, and no notification goes out
// for it. Said in the log line rather than left for somebody to work out from an absence.
func forwardEvents(ctx context.Context, log *slog.Logger, b *bus.Bus, out chan<- wire.Envelope) {
	sub := b.Subscribe(notifierBuffer)
	defer func() { b.Unsubscribe(sub) }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Dropped():
			log.Warn("notify: the notifier fell behind the event bus — resubscribing and " +
				"re-evaluating every device; any backup that finished during the gap goes unannounced")
			b.Unsubscribe(sub)
			sub = b.Subscribe(notifierBuffer)
			send(ctx, out, wire.NewEnvelope(wire.EventDeviceUpdated, nil))
		case env, ok := <-sub.C():
			if !ok {
				return
			}
			// FILTERED HERE RATHER THAN IN THE RUNNER, so the runner's channel carries only what it
			// acts on. `job.updated` fires on every phase change of every job; forwarding all of it
			// would make the notifier the daemon's busiest bus consumer to reach the same decisions.
			switch env.Type {
			case wire.EventDeviceUpdated, wire.EventDeviceAttached, wire.EventJobUpdated:
				send(ctx, out, env)
			}
		}
	}
}

// send hands one envelope to the notifier, or gives up on it.
//
// IT NEVER BLOCKS FOREVER. This goroutine's other job is to notice a bus drop, and a send that
// parked on a full channel would stop it doing that — the recovery path would be the first casualty
// of the condition it recovers from.
func send(ctx context.Context, out chan<- wire.Envelope, env wire.Envelope) {
	select {
	case out <- env:
	case <-ctx.Done():
	}
}
