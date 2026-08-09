package storage

import (
	"context"
	"log/slog"
	"sync"
)

// THE RECONCILIATION RUNNER (qn.6i D6, Operator ruling 2026-08-08 on quince#731).
//
// Reconciliation used to be a step on two critical paths: before the listener bound (quince#592,
// ~36 s of connection-refused after the container reported `Up`) and inside the HTTP request that
// adds a storage (quince#715, ~48 s with `writeMu` held). The ruling made it asynchronous and
// TRIGGERED — startup, storage-added, and on a schedule.
//
// ONE QUEUE, ONE PASS AT A TIME, DUPLICATES COLLAPSED. Concurrency here would buy nothing: the work
// is disk-bound, and two passes over one root would reintroduce exactly the class of race the commit
// lease exists to remove. Duplicates collapse because the triggers are not independent questions —
// "scan now" asked twice while a scan is queued is one scan, and running it twice would double a
// 48-second walk for an answer that cannot have changed.
//
// WHAT IT DELIBERATELY DOES NOT DO: retry a failed pass, or back off. A pass that fails logs and the
// next trigger runs the next one. Reconciliation is idempotent by construction — adopt-if-absent,
// mark-if-vanished, recompute — so there is nothing a retry does that the next trigger does not, and
// a retry loop is how a permanently failing storage becomes a busy loop nobody notices.
type Runner struct {
	m   *Manager
	log *slog.Logger

	trigger chan string // capacity 1: a queued pass ABSORBS further requests (the collapse)

	mu sync.Mutex
	// pending and running are separate because `reconciling` must be true from the MOMENT A TRIGGER
	// IS ACCEPTED, not from when the goroutine gets to it. Collapsing them into one flag leaves a
	// window at startup where the listener is up, the scan is queued, and health says it has
	// finished — which is precisely the false `false` this rung exists to remove.
	pending bool
	running bool
	// passes counts COMPLETED passes, so a caller can wait for one rather than sleeping. Tests use
	// it; nothing on the wire does.
	passes  int
	waiters []chan struct{}
}

// NewRunner wires a runner over a Manager. It starts nothing — call Start.
func NewRunner(m *Manager, log *slog.Logger) *Runner {
	return &Runner{m: m, log: log, trigger: make(chan string, 1)}
}

// Start runs the runner's loop until ctx is done. It returns immediately.
func (r *Runner) Start(ctx context.Context) {
	go r.loop(ctx)
}

// Trigger asks for a pass. It NEVER BLOCKS and never fails: a trigger arriving while one is already
// queued is absorbed, which is the collapse this type exists for. `reason` is for the log, so a
// reader can tell a scheduled pass from one a user's action caused.
func (r *Runner) Trigger(reason string) {
	r.mu.Lock()
	r.pending = true
	r.mu.Unlock()
	select {
	case r.trigger <- reason:
	default:
		// Already queued. Deliberately silent at Info: under a schedule this is the ordinary case,
		// and a line per absorbed trigger would be noise that hides the ones that matter.
		r.log.Debug("reconcile: a pass is already queued — request absorbed", "reason", reason)
	}
}

// Reconciling reports whether a pass is queued OR running. It is what `GET /api/health` publishes,
// and its meaning is written down in contracts §1: while true, a version list may be SHORT.
func (r *Runner) Reconciling() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending || r.running
}

func (r *Runner) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-r.trigger:
			r.runOnce(ctx, reason)
		}
	}
}

func (r *Runner) runOnce(ctx context.Context, reason string) {
	r.mu.Lock()
	// CLEARED AS THE PASS STARTS, NOT AFTER IT. A trigger arriving mid-pass must queue another one:
	// it may describe a storage this pass has already walked past. Clearing at the end would swallow
	// it, and the added disk would stay invisible until something else asked.
	r.pending = false
	r.running = true
	r.mu.Unlock()

	r.log.Info("reconcile: pass starting", "reason", reason)
	res, err := r.m.ReconcileScan(ctx)
	switch {
	case err != nil:
		r.log.Error("reconcile: pass failed — the next trigger runs the next one, nothing is retried "+
			"here", "reason", reason, "error", err)
	case len(res.Deferred) > 0:
		// A PARTIAL PASS SAYS SO. These devices had a live commit path, so the honest statement is
		// that the registry is reconciled except for them — and the deferral resolves when the job
		// ends, which is what `UnbindJob`'s re-trigger is for (wired below by the engine's release).
		r.log.Info("reconcile: pass complete, with devices DEFERRED because a backup was running on "+
			"them", "reason", reason, "deferred", res.Deferred)
	default:
		r.log.Info("reconcile: pass complete", "reason", reason)
	}

	r.mu.Lock()
	r.running = false
	r.passes++
	for _, w := range r.waiters {
		close(w)
	}
	r.waiters = nil
	r.mu.Unlock()
}

// Passes is the number of COMPLETED passes. Tests wait on it; nothing on the wire reads it.
func (r *Runner) Passes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.passes
}

// WaitForPass returns a channel closed when the next pass completes. It exists so tests can be
// deterministic instead of sleeping — a sleep long enough to be reliable is a sleep long enough to
// make the suite slow, and one short enough to be fast is a flake.
func (r *Runner) WaitForPass() <-chan struct{} {
	ch := make(chan struct{})
	r.mu.Lock()
	r.waiters = append(r.waiters, ch)
	r.mu.Unlock()
	return ch
}
