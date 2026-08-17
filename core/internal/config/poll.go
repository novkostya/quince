package config

import (
	"context"
	"time"
)

// PollInterval is how often a running quince re-reads `config.yml` to notice a hand-edit (qn.6q).
//
// IT IS NOT A CONFIG KEY, DELIBERATELY (spec decision D1). A setting controlling how fast settings
// are re-read is a knob whose own changes are subject to itself, and nobody needs it; D12's "every
// setting has a sane default" is better served by there being no setting at all.
//
// TWO SECONDS IS A CEILING ON A HUMAN'S WAIT, not a tuned number. A hand-edit is somebody at an
// editor, for whom two seconds is not perceptibly different from instant — and unlike inotify's
// *usually instant, sometimes never*, it is a GUARANTEE.
const PollInterval = 2 * time.Second

// Watcher re-reads `config.yml` on a fixed interval and applies anything quince did not write
// itself. It is the second producer feeding the appliers `qn.6g` built.
//
// POLLING RATHER THAN inotify, and the spec's D1 records why with the measurements. The short
// version, because the reasoning is what a later reader will want to re-open:
//
//   - It costs 12.19µs per tick to read and compare a realistic 218-byte config (measured
//     2026-08-17), against 2.33µs for a bare `stat`. At one tick per two seconds that is roughly
//     0.0006% of a core, so the cheap option and the correct option are the same option.
//   - The content comparison is required under inotify TOO (see Service.lastBytes), so polling is
//     the SUBSET rather than the alternative — inotify would add an event source on top of the
//     mechanism, not instead of it.
//   - A watch on the file PATH is dead after one write, measured: `ATTRIB`, `DELETE_SELF`,
//     `IGNORED`, and then nothing for any later change including in-place ones. So a correct
//     inotify implementation is a directory watch plus name filtering plus requeue handling, in
//     front of the comparison you were going to write anyway.
//
// A network-filesystem argument was made for this and is NOT the load-bearing one — the Operator
// RAISED THE OBJECTION on 2026-08-17 and the architect re-weighted D1 on quince#1094. No ruling was
// given and none was needed; the spec carries the chain and the exact words. Cost carries D1 alone.
// Recorded so nobody defends this on the weak leg.
type Watcher struct {
	svc      *Service
	interval time.Duration
}

// NewWatcher returns a Watcher over svc. A zero or negative interval takes PollInterval, so a
// caller cannot accidentally spin.
func NewWatcher(svc *Service, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = PollInterval
	}
	return &Watcher{svc: svc, interval: interval}
}

// Run polls until ctx is done. Start it with `go w.Run(ctx)`, which is how every other background
// loop in this process is started.
//
// IT LOGS NOTHING ITSELF, and that is the point of `ReloadOutcome`: the ordinary answer is
// `ReloadUnchanged`, several times a minute forever, and a loop that narrated that would bury every
// real line in the log. `Reload` logs the two outcomes that are worth a line — applied, and refused
// — at the moment they happen.
//
// A TICK THAT OVERRUNS DOES NOT STACK. `time.Ticker` drops ticks rather than queueing them, and
// `Reload` is synchronous, so a slow filesystem makes the poll late and never concurrent. That
// matters because `Reload` takes `writeMu`: overlapping polls would queue behind a UI save and
// behind each other.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.svc.Reload()
		}
	}
}
