package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// The lease's gates (qn.6i D3/D4, quince#731 blocker 1). Every one of them is RED without the guard
// it names — verified by removing that guard and re-running, which is recorded in the PR rather than
// asserted here, because a test that passes both ways proves nothing about the thing it guards.

// leaseManager is newNSManager with a real storage id, because the guard is keyed on (storage,
// device) and an unattributed storage would make the key half-empty — true of a first run and not of
// the case these gates are about.
//
// THE ON-DISK MARKER IS REWRITTEN TO MATCH, and that is not fixture ceremony. Story 7's pre-backup
// check compares the slot's id against the marker at the path before every job, so a slot carrying an
// id the medium does not is a state `buildStorage` cannot produce — and the check refuses it, which
// is how the first version of this helper failed. Setting one without the other tests a storage that
// cannot exist.
func leaseManager(t *testing.T) (*Manager, string) {
	t.Helper()
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	seedStorageMarker(t, backups, leaseStorageID, BackendCopy)
	m.mu.Lock()
	m.slots[0].StorageID = leaseStorageID
	m.mu.Unlock()
	return m, backups
}

const leaseStorageID = "01JSTORAGELEASE000000000"

// G4 — A DEVICE WITH A LIVE JOB ON THIS STORAGE IS DEFERRED, AND IT IS DEFERRED RATHER THAN LOST.
//
// The second half is the half that matters. Deferring is only honest if the work comes back: a guard
// that skipped a device permanently would turn "a backup was running" into "these versions are
// invisible until someone restarts quince", which is a worse defect than the race it prevents.
//
// The re-run here is a SECOND Reconcile call, which is what a startup or an add does today. The
// automatic re-trigger when the job ends is qn.6i's PR 3 — this gate asserts the property PR 3 will
// automate, so it does not silently become the only thing holding it up.
func TestReconcileDefersADeviceWithALiveJobAndAdoptsItOnceTheJobEnds(t *testing.T) {
	m, backups := leaseManager(t)

	// An on-disk version with no registry row: reconciliation's adopt path is what would pick it up.
	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "adopt-under-lease", "", testUDID, BackendCopy)

	if err := m.BindJobStorage("job-live", testUDID, leaseStorageID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile while a job is live: %v", err)
	}
	if _, ok, _ := m.reg.GetVersion("adopt-under-lease"); ok {
		t.Fatal("the device was scanned while a backup was live on this storage — the guard is what " +
			"keeps a repair pass off a device whose commit path is running")
	}

	// The job ends. `Engine.release` is the single termination path for success, failure, cancel and
	// shutdown alike, and it is where UnbindJob is called — so every ending reaches this state.
	m.UnbindJob("job-live")
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile after the job ended: %v", err)
	}
	if _, ok, _ := m.reg.GetVersion("adopt-under-lease"); !ok {
		t.Fatal("the deferred device was never reconciled after its job ended — a deferral that " +
			"never comes back is a version that stays invisible, not a race avoided")
	}
}

// G4b — A JOB ON ANOTHER DEVICE DOES NOT DEFER THIS ONE.
//
// This is why the binding gained the udid. Keyed on the storage alone, one device's overnight Wi-Fi
// backup would defer every other device's repair on that disk — and under a SCHEDULED reconcile that
// is not a coincidence, it is most nights.
func TestReconcileScansADeviceWhoseStorageIsBusyForADifferentDevice(t *testing.T) {
	m, backups := leaseManager(t)

	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "adopt-other-device", "", testUDID, BackendCopy)

	if err := m.BindJobStorage("job-elsewhere", "SYNTHETIC-UDID-0002", leaseStorageID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok, _ := m.reg.GetVersion("adopt-other-device"); !ok {
		t.Fatal("a backup running for ANOTHER device deferred this one — the guard is per " +
			"(storage, device), and a storage-wide one would defer a whole disk for one phone")
	}
}

// G5 — A COMMIT JOURNAL A LIVE JOB IS DRIVING IS NOT ROLLED FORWARD, AND THE SAME JOURNAL IS ROLLED
// FORWARD ONCE ITS JOB IS GONE.
//
// This is blocker 1's sharpest face: `reconcileSlot` opens with PendingJournals → ResumeCommit, so
// before the guard a reconcile could pick up the journal of a commit that is still running and drive
// the same phase sequence beside the engine.
//
// The two halves are one test on purpose. Skipping is only correct because the crash case still
// works — a guard that skipped every journal would "fix" the race by abandoning roll-forward, and the
// roll-forward principle is what stops a commit failure destroying a multi-hour transfer.
func TestRollForwardSkipsAJournalALiveJobIsDrivingAndResumesItAfterwards(t *testing.T) {
	m, backups := leaseManager(t)
	commitGoodTree(t, m, testUDID)
	stageNSCommit(t, m, backups, testUDID, "job-committing", "v2live", PhasePrepared)

	if err := m.BindJobStorage("job-committing", testUDID, leaseStorageID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok, _ := m.reg.GetVersion("v2live"); ok {
		t.Fatal("a journal whose job is STILL RUNNING was rolled forward — two actors would then be " +
			"driving one commit through the same phase sequence")
	}
	if !journalExists(backups, testUDID) {
		t.Fatal("the live job's journal was cleared by reconciliation — the journal belongs to the " +
			"job that is still writing it")
	}

	// The process that was driving it is gone. This is now an ordinary crash journal.
	m.UnbindJob("job-committing")
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile after the job ended: %v", err)
	}
	if _, ok, _ := m.reg.GetVersion("v2live"); !ok {
		t.Fatal("the journal was not rolled forward once its job was gone — the guard must narrow " +
			"roll-forward, never disable it")
	}
}

// G6a — THE D4 MEASUREMENT, AND IT IS DETERMINISTIC BECAUSE THE PROBABILISTIC VERSION DOES NOT WORK.
//
// The spec records D4 as REASONED, NOT MEASURED and undertakes to withdraw it here if the window
// proves unreachable. It is NOT unreachable — this test enters it — but the honest statement is
// narrower than the spec's, and this comment is where that gets corrected:
//
//	MEASURED: with a scan interleaved between `Backend.Commit` and `registerCommitted`, the adopt path
//	inserts the version and the engine's own insert then fails on the primary key. The mechanism is
//	real, exactly as reasoned.
//	NOT MEASURED: that a real scheduler ever lands there. G6b runs the two concurrently for real and
//	PASSED 5/5 with the guard removed — the window is a few microseconds wide, so a loop racing it does
//	not hit it. Nobody has established a rate, and this test does not.
//
// The interleave is written out by hand rather than raced, which makes it a proof about the SEAM
// rather than about the scheduler. That is the right claim to gate on: the ruling requires the lease
// whether or not anyone can make the collision happen on demand, and a gate that passes because it
// missed a window is worse than no gate at all.
func TestAScanBetweenCommitAndRegisterCollidesOnThePrimaryKey(t *testing.T) {
	m, _ := leaseManager(t)
	commitGoodTree(t, m, testUDID)

	s, err := m.jobSlot("job-interleaved")
	if err != nil {
		t.Fatalf("slot: %v", err)
	}
	goodEncryptedFull(t, seedTree(t, m, testUDID, "job-interleaved"))
	tree := s.Backend.TreePath(testUDID, "job-interleaved")
	vr := Verify(tree, m.seedKind(s, testUDID))
	if !vr.OK {
		t.Fatalf("verify: %s", vr.Detail)
	}
	committed, err := s.Backend.Commit(CommitReq{
		UDID: testUDID, JobID: "job-interleaved", VersionID: m.newID(), CreatedAt: m.now(), Verify: vr,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	// THE ARTIFACT NOW EXISTS AND HAS NO ROW. This is the window, entered on purpose.
	if err := m.reconcileDevice(s, testUDID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	row, adopted, _ := m.reg.GetVersion(committed.VersionID)
	if !adopted {
		t.Fatal("the scan did not adopt the just-committed artifact — if this ever becomes the " +
			"behaviour, D4 is gone and the lease's second reason with it; say so rather than " +
			"deleting this test")
	}
	if row.JobID != nil {
		t.Fatalf("adopted row should carry a null job_id, got %v", *row.JobID)
	}

	// And this is the cost: the engine's own registry write now fails for a backup that COMPLETED.
	if err := m.registerCommitted(s, committed); err == nil {
		t.Fatal("registerCommitted succeeded after the scan had adopted the same version — D4 says " +
			"it collides on the primary key, and if it no longer does, the finding is withdrawn")
	}
}

// G6c — THE LEASE IS WHAT CLOSES G6a's WINDOW, asserted directly rather than raced.
//
// G6a proves the seam is dangerous; G6b proves the ordinary path still works. Neither proves the
// lease does anything, because neither can make the scheduler land in a microsecond window on
// demand. This does: it holds the lease exactly as `CommitJob` holds it, and asserts the scan
// declines to enter — which is the entire mechanism, checked without a race.
//
// It is RED without the TryClaim in `reconcileDevice`: the scan enters, adopts, and the assertion
// below fires.
func TestAScanWillNotEnterADeviceWhoseCommitLeaseIsHeld(t *testing.T) {
	m, backups := leaseManager(t)

	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "adopt-behind-lease", "", testUDID, BackendCopy)

	// Exactly what the commit path holds, for exactly the same key. No job is bound, so the
	// pre-check passes and the lease is the only thing standing between the scan and the tree.
	lease := m.leaseFor(leaseStorageID, testUDID)
	lease.Claim()

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok, _ := m.reg.GetVersion("adopt-behind-lease"); ok {
		t.Fatal("the scan entered a device whose commit lease was held — this is the window D4 " +
			"describes, and the lease is the only thing that closes it")
	}

	// And it must be a DEFERRAL rather than a refusal: released, the next pass does the work.
	lease.Release()
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile after release: %v", err)
	}
	if _, ok, _ := m.reg.GetVersion("adopt-behind-lease"); !ok {
		t.Fatal("the device was never reconciled after the lease was released")
	}
}

// G6b — THE GUARD, RUN FOR REAL: a commit concurrent with a scan of the same device does not fail.
//
// A regression net rather than a proof. It PASSED 5/5 with the lease removed (see G6a), so it does
// not demonstrate the window — it demonstrates that the guard does not break the ordinary path, and
// it is the shape that would catch a future deadlock or a lease leaked by a new `continue`.
//
// THE JOB IS DELIBERATELY UNBOUND, which isolates the lease from the pre-check: an unbound job
// resolves to the default slot (`jobSlot`), so the scan is not deferred and the two really do
// overlap. Run under -race, which is how the whole Go gate runs.
func TestACommitConcurrentWithAScanStillSucceeds(t *testing.T) {
	m, _ := leaseManager(t)
	commitGoodTree(t, m, testUDID) // something for the scan to walk

	goodEncryptedFull(t, seedTree(t, m, testUDID, "job-racing"))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Errors are not asserted: a scan racing a commit may legitimately observe a tree
			// mid-change. What must not happen is the COMMIT failing, which is what the assertion
			// below is about.
			_ = m.Reconcile(context.Background())
		}
	}()

	v, err := m.CommitJob(testUDID, "job-racing")
	close(stop)
	wg.Wait()

	if err != nil {
		t.Fatalf("a commit that completed was reported failed because a scan was running: %v — this "+
			"is D4, and the lease is what closes it", err)
	}
	row, ok, _ := m.reg.GetVersion(v.ID)
	if !ok {
		t.Fatalf("the committed version %s has no row", v.ID)
	}
	if row.JobID == nil {
		t.Fatalf("version %s was recorded as ADOPTED rather than committed — the scan won the race, "+
			"so the version is retention-protected and its job is not credited with it", v.ID)
	}
}
