package storage

import (
	"context"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// THE SPLIT (qn.6i D2) — ROLL-FORWARD DOES NOT SCAN, AND THE SCAN DOES NOT ROLL FORWARD.
//
// This is the rung's central decision gated directly. `serve` runs the first half before the
// listener binds because `Engine.Reconcile` depends on it — a job row judged before its commit is
// completed becomes `connection_lost` for a backup that SUCCEEDED — and defers the second half,
// because that is where the 36-48 seconds are.
//
// A single fixture carries both halves so neither can pass by doing the other's work: a crash
// journal that only roll-forward completes, and an unadopted on-disk version that only the scan
// picks up.
func TestRollForwardDoesNotScanAndTheScanDoesNotRollForward(t *testing.T) {
	m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())
	commitGoodTree(t, m, testUDID) // v1, so there is a latest to displace
	stageNSCommit(t, m, backups, testUDID, "job-crashed", "v2crash", PhasePrepared)

	// An unadopted on-disk version, which ONLY the scan adopts.
	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "adopt-only-by-scan", "", testUDID, BackendCopy)

	if err := m.RollForwardAll(context.Background()); err != nil {
		t.Fatalf("roll-forward: %v", err)
	}
	if _, ok, _ := st.GetVersion("v2crash"); !ok {
		t.Fatal("roll-forward did not complete the crash journal — this half runs BEFORE the listener " +
			"binds precisely so the job reconciler can see it")
	}
	if _, ok, _ := st.GetVersion("adopt-only-by-scan"); ok {
		t.Fatal("roll-forward ADOPTED an on-disk version — it is supposed to touch journals only, and " +
			"if it scans then the ~48 seconds are still in front of the listener")
	}

	if _, err := m.ReconcileScan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, ok, _ := st.GetVersion("adopt-only-by-scan"); !ok {
		t.Fatal("the scan did not adopt the on-disk version — the deferred half must still do the " +
			"whole job when it eventually runs")
	}
}

// A PARTIAL PASS REPORTS WHAT IT SKIPPED (quince#771 review).
//
// `ReconcileScan` returning only an error would make a deferral indistinguishable from a complete
// pass — and `buildStorage` promises its callers a *reconciled* Manager. The runner needs this to
// say so in the log; a later rung needs it to re-trigger.
func TestAScanReportsTheDevicesItDeferred(t *testing.T) {
	m, _ := leaseManager(t)
	// THE DEVICE HAS TO EXIST BEFORE IT CAN BE DEFERRED. `reconcileUDIDs` is the union of devices
	// with rows and devices with directories, so on an empty Manager the scan loop never reaches
	// testUDID and `Deferred` is empty for the wrong reason — which is how the first version of this
	// test failed, passing its own precondition rather than the property.
	commitGoodTree(t, m, testUDID)
	if err := m.BindJobStorage("job-live", testUDID, leaseStorageID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	res, err := m.ReconcileScan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Deferred) != 1 {
		t.Fatalf("Deferred = %v, want one entry — a pass that skipped a device and reported nothing "+
			"is a silent partial pass", res.Deferred)
	}
}

// THE RUNNER RUNS A PASS WHEN TRIGGERED, and `Reconciling` is true from the moment of the trigger.
//
// The second half is the one with a real failure mode behind it. If `reconciling` only became true
// when the goroutine picked the work up, a request arriving in that window — which at startup is
// exactly when a deploy check arrives — would be told the registry is settled when nothing had
// scanned it yet. A false `false` is worse than the 36 seconds this rung removes, because it is
// wrong rather than merely slow.
func TestRunnerReportsReconcilingFromTheTriggerUntilThePassCompletes(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	r := NewRunner(m, testLogger())

	if r.Reconciling() {
		t.Fatal("a runner that has never been triggered reports reconciling")
	}
	done := r.WaitForPass()
	r.Trigger("test")
	if !r.Reconciling() {
		t.Fatal("reconciling = false immediately after Trigger — the window between the trigger and " +
			"the goroutine picking it up is exactly when a deploy check asks")
	}

	r.Start(context.Background())
	<-done
	if r.Reconciling() {
		t.Fatal("reconciling stayed true after the pass completed")
	}
	if r.Passes() != 1 {
		t.Fatalf("Passes = %d, want 1", r.Passes())
	}
}

// DUPLICATE TRIGGERS COLLAPSE — many requests, ONE queued pass.
//
// Under a scheduled trigger plus a storage add plus a job ending, several triggers landing while a
// pass is queued is the ordinary case, not a corner. Running one 48-second walk per trigger would
// make the runner the new version of the problem it was built to remove.
//
// The runner is deliberately NOT started until every trigger is in, which is what makes this
// deterministic rather than a race: with nothing consuming the channel, the collapse is the only
// thing that can bound the count.
func TestDuplicateTriggersCollapseIntoOnePass(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	r := NewRunner(m, testLogger())

	done := r.WaitForPass()
	for i := 0; i < 25; i++ {
		r.Trigger("burst")
	}
	r.Start(context.Background())
	<-done

	// One pass has completed. Give the loop room to run a second if the collapse failed — if 25
	// triggers had queued 25 passes, the counter would climb past 1 here.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if r.Passes() > 1 {
			t.Fatalf("Passes = %d after 25 triggers — duplicates are not collapsing, so each trigger "+
				"costs a full walk", r.Passes())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A TRIGGER ARRIVING WHILE A PASS RUNS QUEUES ANOTHER ONE, and this is the other half of the
// collapse rule rather than a contradiction of it.
//
// `pending` is cleared as a pass STARTS, not when it ends. A trigger landing mid-pass may describe a
// storage this pass has already walked past — a disk added thirty seconds into a scan — so swallowing
// it would leave that disk invisible until something else asked. Collapse while QUEUED, re-queue
// while RUNNING.
func TestATriggerDuringAPassQueuesAnotherPass(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	r := NewRunner(m, testLogger())
	r.Start(context.Background())

	first := r.WaitForPass()
	r.Trigger("one")
	<-first

	second := r.WaitForPass()
	r.Trigger("two")
	<-second

	if r.Passes() != 2 {
		t.Fatalf("Passes = %d, want 2 — a trigger after a pass must run another one", r.Passes())
	}
}
