package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

// The notifier's runner — the half that decides WHEN (spec D5).
//
// `Evaluate` and `ForTerminal` answer *what should this device be told*; this answers *now?*, and it
// is the piece that makes the assisted model automatic rather than a button somebody has to press.
//
// THREE TRIGGERS, AND EACH ONE IS THERE FOR A DIFFERENT REASON:
//
//   - a DEVICE EVENT, because a phone appearing on the network is the moment a reminder becomes
//     actionable — this is the whole "opportunity signal" the rung is named for, driven from
//     visibility quince already has rather than from an artifact anyone must install;
//   - a JOB TERMINAL, because a failure is worth saying immediately and a success clears the track;
//   - a TICK, because a device that is already visible and simply crosses the staleness threshold
//     produces no event at all, and without it that device would wait for someone to unplug it.
//
// The tick is FIXED AT ONE HOUR and not configurable: the thresholds are in days, so an hour is
// already an order of magnitude finer than the finest question it answers, and a knob here would be
// a setting whose only correct value is the default (D12).
// EXPORTED so the daemon can name it in the line it logs at startup: "the notifier started" is much
//
// more useful when it also says how long a quiet period is expected to be.
const TickInterval = time.Hour

// Devices is the presence and staleness the runner reads. An interface so this package does not
// depend on the device registry, and so a test can stage a fleet.
type Devices interface {
	Devices() []wire.Device
}

// Jobs answers whether a device is already backing up, so a reminder is not sent about a thing that
// is happening.
type Jobs interface {
	RunningFor(udid string) bool
}

// Reminders is the per-device track (spec D5) — one row per device, not per kind.
type Reminders interface {
	PushReminder(udid string) (time.Time, bool, error)
	SetPushReminder(udid string, at time.Time) error
	ClearPushReminder(udid string) error
}

// Deliverer sends a decision. `notify` never dials anything itself; this is the seam to `pushsvc`.
type Deliverer interface {
	DeliverDecision(ctx context.Context, d Decision) error
}

// Runner drives the notifier. It owns no state beyond its dependencies — the track lives in the DB,
// so a restart resumes rather than re-notifying.
type Runner struct {
	Log       *slog.Logger
	Devices   Devices
	Jobs      Jobs
	Reminders Reminders
	Deliver   Deliverer
	Config    func() config.NotificationsConfig
	Now       func() time.Time
}

// Run blocks until ctx is cancelled, evaluating on each of the three triggers.
//
// ONE EVALUATION AT A TIME, BY CONSTRUCTION: everything happens on this goroutine, so the cooldown
// check and the write that satisfies it cannot interleave. Two concurrent evaluations of one device
// would both see "cooldown elapsed" and both send — the double notification D5 exists to prevent,
// arriving by a different road than the one that ruling was about.
func (r *Runner) Run(ctx context.Context, events <-chan wire.Envelope) {
	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.evaluateAll(ctx)
		case ev, ok := <-events:
			if !ok {
				return
			}
			r.handle(ctx, ev)
		}
	}
}

func (r *Runner) handle(ctx context.Context, ev wire.Envelope) {
	switch ev.Type {
	// BOTH DEVICE EVENTS, AND THE ATTACH IS THE ONE THAT MATTERS (found by wiring, quince#1124).
	//
	// `device.attached` is the opportunity signal this rung is named for — a phone appearing on the
	// network. `device.updated` is a different fact: the registry publishes it when ENRICHMENT
	// CHANGED SOMETHING (`changed && listed`) or when a backup is announced. A phone that reconnects
	// to Wi-Fi with the same name, the same pairing and the same encryption setting — which is what
	// a phone does every day — emits `device.attached` and NO `device.updated` at all.
	//
	// Listening only for `updated` therefore missed the recurring case the assisted model exists for,
	// and would have degraded to the hourly tick without anything looking broken. Both are handled
	// because both are genuine chances to ask: the guard clauses and the cooldown make a redundant
	// evaluation free.
	case wire.EventDeviceAttached, wire.EventDeviceUpdated:
		// A DEVICE EVENT RE-EVALUATES EVERY DEVICE, not just the one that moved. It is cheap — the
		// registry is in memory and the guard clauses reject fast — and scoping it to one device
		// would mean carrying the udid out of an `any`-typed payload, which is a decode that can
		// silently fail and leave the feature quietly dead.
		r.evaluateAll(ctx)
	case wire.EventJobUpdated:
		r.handleJob(ctx, ev)
	}
}

// handleJob routes a FINISHED job to a kind, and clears the reminder track on success.
//
// CLEARING ON SUCCESS IS WHAT MAKES THE COOLDOWN MEAN SOMETHING. Without it a device that was
// reminded, then backed up, then went stale again would wait out the remainder of a cooldown that
// belongs to a lapse which is over.
func (r *Runner) handleJob(ctx context.Context, ev wire.Envelope) {
	job, ok := ev.Data.(wire.Job)
	if !ok {
		// A payload this runner cannot read is not something to guess at. It is logged rather than
		// ignored, because a silent decode failure here presents as "notifications stopped working"
		// with nothing anywhere saying why.
		r.Log.Warn("notify: job event payload was not a Job; no notification sent")
		return
	}
	if !terminal(job.State) {
		return
	}
	dev, ok := r.device(job.UDID)
	if !ok {
		return
	}
	if job.State == backup.StateSucceeded {
		if err := r.Reminders.ClearPushReminder(job.UDID); err != nil {
			r.Log.Warn("notify: could not clear the reminder track", "error", err)
		}
	}
	code := ""
	if job.Error != nil {
		code = job.Error.Code
	}
	d, send := ForTerminal(dev, job.State, code, r.Config())
	if !send {
		return
	}
	r.send(ctx, d, false)
}

// evaluateAll walks the fleet for reminders that are due.
func (r *Runner) evaluateAll(ctx context.Context) {
	cfg := r.Config()
	now := r.Now()
	for _, dev := range r.Devices.Devices() {
		at, _, err := r.Reminders.PushReminder(dev.UDID)
		if err != nil {
			r.Log.Warn("notify: could not read the reminder track", "error", err)
			continue
		}
		d, send := Evaluate(dev, Reminder{LastSentAt: at}, cfg, r.Jobs.RunningFor(dev.UDID), now)
		if !send {
			continue
		}
		r.send(ctx, d, true)
	}
}

// send delivers and, for a REMINDER, records the track.
//
// THE TRACK IS WRITTEN ONLY FOR REMINDERS, and that is D5's cooldown belonging to the track rather
// than to a kind. A failure notification is an event downstream of something the user did; recording
// it would silence the next genuine reminder for a day.
//
// IT RECORDS EVEN WHEN DELIVERY FAILED, deliberately. The alternative — record only on success —
// retries every evaluation against a push service that is down, which is a notification storm the
// moment it comes back. A missed reminder costs one cycle; the storm costs the user's trust in
// notifications entirely.
func (r *Runner) send(ctx context.Context, d Decision, isReminder bool) {
	if err := r.Deliver.DeliverDecision(ctx, d); err != nil {
		r.Log.Warn("notify: delivery failed", "kind", string(d.Kind), "error", err)
	}
	if !isReminder {
		return
	}
	if err := r.Reminders.SetPushReminder(d.UDID, r.Now()); err != nil {
		r.Log.Warn("notify: could not record the reminder", "error", err)
	}
}

func (r *Runner) device(udid string) (wire.Device, bool) {
	for _, d := range r.Devices.Devices() {
		if d.UDID == udid {
			return d, true
		}
	}
	return wire.Device{}, false
}

// terminal reports whether a job state is finished. `cancelled` IS terminal and routes to nothing —
// `KindForTerminal` decides that, not this.
func terminal(state string) bool {
	switch state {
	case backup.StateSucceeded, backup.StateFailed, backup.StateCancelled, backup.StateConnectionLost:
		return true
	}
	return false
}
