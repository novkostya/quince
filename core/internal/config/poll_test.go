package config

// The watcher's own suite (qn.6q, quince#1094). What the seam's tests cannot cover: that something
// actually drives `Reload` on a clock, and that it stops when told.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the deadline passes. Used instead of a fixed sleep so a slow
// box makes the test slower rather than flaky — the failure this guards is a watcher that never
// fires, not one that fires late.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

// STORY 1 — a hand-edit applies with nothing but time passing.
//
// The seam's tests call Reload directly; this is the half that proves something calls it.
func TestWatcherAppliesAHandEdit(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	var a countingApplier
	svc.Subscribe("test", a.apply)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewWatcher(svc, 10*time.Millisecond).Run(ctx)

	before := svc.Current().Backup.RequireEncryption
	edited := oneStorage(store) + "backup:\n  require_encryption: " + boolStr(!before) + "\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 3*time.Second, "the hand-edit to be applied", func() bool {
		return svc.Current().Backup.RequireEncryption != before
	})
	if got := a.count(); got != 1 {
		t.Errorf("applier ran %d times, want 1", got)
	}
}

// A QUIET FILE MUST STAY QUIET. Many ticks over an unchanged file apply nothing — which is the
// property that makes a 2-second poll acceptable in the first place.
func TestWatcherIsSilentOnAnUnchangedFile(t *testing.T) {
	store := t.TempDir()
	svc, _ := newLoadedService(t, oneStorage(store))
	var a countingApplier
	svc.Subscribe("test", a.apply)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewWatcher(svc, time.Millisecond).Run(ctx)

	time.Sleep(200 * time.Millisecond) // ~200 ticks
	if got := a.count(); got != 0 {
		t.Errorf("applier ran %d times over an unchanged file, want 0", got)
	}
}

// THE WATCHER STOPS WHEN ctx IS CANCELLED. A background loop that outlives its context is a leak
// that only shows up as a test binary that will not exit.
func TestWatcherStopsOnContextCancel(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))
	var a countingApplier
	svc.Subscribe("test", a.apply)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewWatcher(svc, time.Millisecond).Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	// …and it really is not polling any more.
	edited := oneStorage(store) + "reconcile:\n  interval_minutes: 45\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := a.count(); got != 0 {
		t.Errorf("applier ran %d times after cancel, want 0 — the loop is still running", got)
	}
}

// A zero or negative interval must not spin. NewWatcher substitutes the default rather than
// trusting every caller.
func TestWatcherRefusesToSpin(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(filepath.Join(dir, "config.yml"), quiet())
	for _, d := range []time.Duration{0, -time.Second} {
		if got := NewWatcher(svc, d).interval; got != PollInterval {
			t.Errorf("NewWatcher(%v).interval = %v, want %v", d, got, PollInterval)
		}
	}
}

// A BAD EDIT DOES NOT STOP THE WATCHER, and recovery needs no restart — the loop keeps polling
// across a refusal, which is what makes the D12 banner escapable.
func TestWatcherKeepsPollingAcrossARefusal(t *testing.T) {
	store := t.TempDir()
	svc, path := newLoadedService(t, oneStorage(store))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewWatcher(svc, 10*time.Millisecond).Run(ctx)

	if err := os.WriteFile(path, []byte("storage: [ bad: yaml: here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "the bad edit to be refused", svc.Discarded)

	fixed := oneStorage(store) + "reconcile:\n  interval_minutes: 25\n"
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "the repair to be applied", func() bool {
		return !svc.Discarded() && svc.Current().Reconcile.IntervalMinutes == 25
	})
}
