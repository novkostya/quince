package storage

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
)

// qn.6c story 5b (quince#435). An unreachable storage is a LISTED STATE, not a refusal to serve —
// and the invariant that makes serving honest is that such a storage never accepts a job.

// unreachableSlot is what buildStorage produces for a declared storage it could not open: no
// backend, because a backend is chosen by probing a filesystem that was not there.
func unreachableSlot(name, code string) Slot {
	return Slot{
		Name: name, Root: "/nowhere/" + name, Reachable: false,
		UnreachableCode: code, UnreachableReason: "the medium is not present",
	}
}

// THE INVARIANT, and it is the one clause of the ruling that is not optional: a storage whose
// resolution did not succeed never accepts a job. Serving with a disk missing is only honest while
// nothing can write to a storage quince could not verify.
func TestAnUnreachableStorageNeverAcceptsAJob(t *testing.T) {
	m := &Manager{slots: []Slot{unreachableSlot("shuttle", "missing_medium")}, log: testLogger()}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"Seed", func() error { _, err := m.Seed(testUDID, "job-1"); return err }},
		{"PrepareWork", func() error { _, _, err := m.PrepareWork(testUDID, "job-1"); return err }},
		{"SeedWork", func() error { return m.SeedWork(testUDID, "job-1") }},
	} {
		err := tc.call()
		if err == nil {
			t.Errorf("%s: an unreachable storage accepted a job — the invariant is gone", tc.name)
			continue
		}
		// The refusal must NAME the storage and the cause. "storage error" tells a user nothing
		// about which disk to plug in, which is the whole point of serving instead of exiting.
		if !strings.Contains(err.Error(), "shuttle") || !strings.Contains(err.Error(), "missing_medium") {
			t.Errorf("%s: refusal must name the storage and the code, got %q", tc.name, err)
		}
	}
}

// The daemon SERVES. Before this slice a non-OK resolution returned an error from buildStorage and
// the process exited; the ruling's argument is that refusing makes the page that would explain the
// problem unreachable.
func TestReconcileServesWithAnUnreachableStorage(t *testing.T) {
	m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots = append(m.slots, unreachableSlot("shuttle", "missing_medium"))

	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "01VGOOD", "", testUDID, BackendCopy)

	var buf bytes.Buffer
	m.log = captureLogs(&buf)

	// Must not error: one unplugged disk cannot stop reconciliation of the storages that are fine.
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("an unreachable storage must not fail reconciliation: %v", err)
	}
	if !strings.Contains(buf.String(), "skipping an unreachable storage") {
		t.Error("the skip must be said out loud, not silent")
	}
	// And the reachable storage still did its work.
	if _, ok, _ := st.GetVersion("01VGOOD"); !ok {
		t.Error("the reachable storage's version was not adopted — one bad slot stopped the good one")
	}
}

// An unreachable storage's versions are NOT marked missing. Its root cannot be scanned, and
// concluding "the artifact is gone" from a scan that never happened is the state-honesty failure
// that would turn one unplugged disk into a table of dead backups.
func TestAnUnreachableStoragesVersionsAreNotMarkedMissing(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	gone := "01JSTORAGEGONE0000000000"
	m.slots = append(m.slots, Slot{
		StorageID: gone, Name: "shuttle", Root: "/nowhere", Reachable: false,
		UnreachableCode: "missing_medium",
	})

	if err := st.InsertVersion(store.VersionRow{
		ID: "01VONGONE", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full", StorageID: &gone,
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	row, _, err := st.GetVersion("01VONGONE")
	if err != nil {
		t.Fatal(err)
	}
	if row.Missing {
		t.Error("a version on an unplugged disk was marked missing — quince did not look, so it cannot say")
	}
}

// Deleting a version uses THE VERSION'S OWN storage. This read `defaultSlot().Backend` until story
// 5b — the browse_root defect quince#433 fixed, except destructive: a backend resolves paths against
// its own root, so deleting storage B's version through storage A's backend removes whatever sits at
// the matching path under A.
func TestDeleteRefusesWhenTheVersionsStorageIsUnreachable(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	gone := "01JSTORAGEGONE0000000000"
	m.slots = append(m.slots, Slot{
		StorageID: gone, Name: "shuttle", Root: "/nowhere", Reachable: false,
		UnreachableCode: "missing_medium",
	})

	if err := st.InsertVersion(store.VersionRow{
		ID: "01VDEL", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full", StorageID: &gone,
	}); err != nil {
		t.Fatal(err)
	}

	code, err := m.deleteVersion("01VDEL", "version.deleted")
	if err == nil {
		t.Fatal("deleting a version whose storage is unreachable must refuse")
	}
	if code != 409 {
		t.Errorf("want 409 (a state the user can act on), got %d", code)
	}
	// The registry row must SURVIVE. Dropping it while the artifact cannot be removed would leave
	// an orphan on the disk that quince no longer knows about.
	if _, ok, _ := st.GetVersion("01VDEL"); !ok {
		t.Error("the version row was deleted even though its artifact could not be")
	}
}

// A version attributed to no configured storage cannot be deleted either — quince cannot say which
// disk the data is on, and deleting through whichever backend is first would remove the wrong tree.
func TestDeleteRefusesWhenTheVersionHasNoKnownStorage(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = attrStorageID // attributed, so a NULL row resolves to nothing

	if err := st.InsertVersion(store.VersionRow{
		ID: "01VORPHAN", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full",
	}); err != nil {
		t.Fatal(err)
	}

	code, err := m.deleteVersion("01VORPHAN", "version.deleted")
	if err == nil || code != 409 {
		t.Fatalf("want a 409 refusal for a version with no known storage, got %d (%v)", code, err)
	}
	if _, ok, _ := st.GetVersion("01VORPHAN"); !ok {
		t.Error("the row was dropped for a version whose artifact quince could not locate")
	}
}

// THE TEST THE quince#440 MARKER EXISTS FOR, and the one this whole slice can get wrong silently.
//
// Two REACHABLE storages, an artifact under the second. Attribution must use the id of the slot
// whose Scan produced it. While that id came from `m.storageIDPtr()` — the default's — every
// storage's artifacts were attributed to the default, which is quince#439 reintroduced by the slice
// after the one that fixed it. One slot cannot tell the two implementations apart.
func TestAttributionUsesTheScannedSlotNotTheDefault(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = "01JSTORAGEDEFAULT0000000"

	// A second, real, reachable storage with its own root and its own backend.
	second := t.TempDir()
	secondID := "01JSTORAGESECOND00000000"
	m.slots = append(m.slots, Slot{
		StorageID: secondID, Name: "shuttle", Root: second, Reachable: true,
		Backend:     newNamespaceBackend(BackendCopy, clonetree.Copy, second, "test", testLogger()),
		BackendName: BackendCopy,
	})

	// The artifact lives ONLY under the second storage's root.
	verDir := nsVersionDir(second, testUDID, time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "01VSECOND", "", testUDID, BackendCopy)

	// A pre-qn.6c row for it: real, committed, unattributed.
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VSECOND", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full",
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	row, _, err := st.GetVersion("01VSECOND")
	if err != nil {
		t.Fatal(err)
	}
	if row.StorageID == nil {
		t.Fatal("the version was never attributed, though its artifact was scanned")
	}
	if *row.StorageID != secondID {
		t.Fatalf("attributed to %q, want the storage it was SCANNED FROM (%q) — the default's id "+
			"reached the attribution", *row.StorageID, secondID)
	}
}

// qn.6c story 6a — a job's storage is bound at start and resolved from the jobID every write-path
// method already carries.

// The binding REFUSES rather than falling back. A job bound to a storage that is not declared, or
// not usable, must not quietly become a job against the default: that writes a backup to a disk the
// user did not choose.
func TestBindJobStorageRefusesUnknownAndUnreachable(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = "01JSTORAGEOK000000000000"
	m.slots = append(m.slots, unreachableSlot("shuttle", "missing_medium"))
	m.slots[1].StorageID = "01JSTORAGEGONE0000000000"

	if err := m.BindJobStorage("job-1", "01JSTORAGENOSUCH00000000"); err == nil {
		t.Error("binding a job to an undeclared storage must refuse")
	}
	err := m.BindJobStorage("job-2", "01JSTORAGEGONE0000000000")
	if err == nil {
		t.Fatal("binding a job to an unreachable storage must refuse")
	}
	if !strings.Contains(err.Error(), "shuttle") || !strings.Contains(err.Error(), "missing_medium") {
		t.Errorf("the refusal must name the storage and the code, got %q", err)
	}
	if err := m.BindJobStorage("job-3", "01JSTORAGEOK000000000000"); err != nil {
		t.Errorf("binding to a usable storage must succeed, got %v", err)
	}
}

// A BOUND job writes to its own storage, not the default. This is the whole point of the seam: once
// 6b lets a request name a storage, every write-path method must land on that one.
func TestBoundJobResolvesToItsOwnStorage(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = "01JSTORAGEDEFAULT0000000"

	second := t.TempDir()
	m.slots = append(m.slots, Slot{
		StorageID: "01JSTORAGESECOND00000000", Name: "shuttle", Root: second, Reachable: true,
		Backend:     newNamespaceBackend(BackendCopy, clonetree.Copy, second, "test", testLogger()),
		BackendName: BackendCopy,
	})

	if err := m.BindJobStorage("job-1", "01JSTORAGESECOND00000000"); err != nil {
		t.Fatal(err)
	}
	got, err := m.jobSlot("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != second {
		t.Errorf("a bound job must resolve to its own storage, got %q want %q", got.Root, second)
	}

	// And an UNBOUND job still resolves to the default. That is a job from BEFORE the choice existed —
	// a retry of a pre-qn.6c row, or one whose binding was lost across a restart — not a job whose
	// storage was forgotten. The engine binds every new job (engine.go), so this arm is the legacy
	// path rather than the common one.
	unbound, err := m.jobSlot("job-none")
	if err != nil {
		t.Fatal(err)
	}
	if unbound.StorageID != "01JSTORAGEDEFAULT0000000" {
		t.Errorf("an unbound job must resolve to the default, got %q", unbound.StorageID)
	}
}

// A binding whose storage is no longer declared REFUSES rather than retargeting. The job was
// started against a specific disk; silently writing it elsewhere is how a backup lands somewhere
// nobody chose.
func TestABindingToADisappearedStorageRefuses(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = "01JSTORAGEDEFAULT0000000"
	m.mu.Lock()
	m.jobStorage = map[string]string{"job-1": "01JSTORAGEVANISHED000000"}
	m.mu.Unlock()

	if _, err := m.jobSlot("job-1"); err == nil {
		t.Fatal("a binding to a storage that is no longer declared must refuse")
	}
	// Unbinding clears it, so a retry after reconfiguration behaves as a fresh job.
	m.UnbindJob("job-1")
	if _, err := m.jobSlot("job-1"); err != nil {
		t.Errorf("after unbinding, the job must fall back to the default: %v", err)
	}
}

// qn.6c story 6b — POST /api/jobs {storage_id}. Ruled 2026-07-31: omitted → default, unknown →
// 404, unreachable → 409 (a state conflict the user can act on, never a 422 and never a queue).
func TestResolveChoice(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = "01JSTORAGEDEFAULT0000000"
	m.slots[0].Name = "internal"
	m.slots = append(m.slots, unreachableSlot("shuttle", "missing_medium"))
	m.slots[1].StorageID = "01JSTORAGEGONE0000000000"

	// Omitted → the default, and the CONCRETE id, never the word "default".
	got, status, _ := m.ResolveChoice("")
	if status != 0 || got != "01JSTORAGEDEFAULT0000000" {
		t.Errorf("omitted storage_id must resolve to the default's concrete id, got %q (%d)", got, status)
	}

	if _, status, _ := m.ResolveChoice("01JSTORAGENOSUCH00000000"); status != 404 {
		t.Errorf("an unknown storage must be 404 (matching unknown-device), got %d", status)
	}

	_, status, reason := m.ResolveChoice("01JSTORAGEGONE0000000000")
	if status != 409 {
		t.Errorf("an unreachable storage must be 409 — a state conflict, not a malformed request; got %d", status)
	}
	if !strings.Contains(reason, "shuttle") || !strings.Contains(reason, "missing_medium") {
		t.Errorf("the 409 must name the storage and the code so the user knows which disk: %q", reason)
	}
}

// The DEFAULT being unreachable is REFUSED WITH A REASON NAMING IT, never redirected to whichever
// storage happens to be reachable. A fallback there would write a backup to a disk the user did not
// choose — "no silent fallbacks" at its most expensive (Operator ruling 2026-08-01).
func TestResolveChoiceRefusesRatherThanRedirectingWhenTheDefaultIsUnreachable(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	reachable := m.slots[0] // keep a usable one in the list
	m.slots[0] = unreachableSlot("internal", "missing_medium")
	m.slots[0].StorageID = "01JSTORAGEDEFAULT0000000"
	reachable.StorageID = "01JSTORAGEOTHER000000000"
	reachable.Name = "shuttle"
	m.slots = append(m.slots, reachable)

	got, status, reason := m.ResolveChoice("")
	if status != 409 {
		t.Fatalf("an unreachable default must refuse, got %d (%q)", status, got)
	}
	if got != "" {
		t.Errorf("it must not resolve to another storage — got %q, which the user did not choose", got)
	}
	if !strings.Contains(reason, "internal") {
		t.Errorf("the refusal must NAME the default, so the user knows which disk to reconnect: %q", reason)
	}
}

// THE ROW SIDE (quince#447 review). Every other binding test asserts `jobSlot` — where the TREE
// goes — and none asserted where the ROW says it went. That gap is exactly why a backup could write
// to B and be recorded on A with a green suite.
//
// The consequence was not a stale field but active destruction: browse_root resolved to a path that
// does not exist, Verify called a good backup broken, and A's next reconciliation marked the row
// `missing` while B's correctly left it alone — a confidently wrong row the NULL-filling sweep never
// touches.
func TestCommitRecordsTheVersionOnTheJOBsStorage(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = "01JSTORAGEDEFAULT0000000"

	second := t.TempDir()
	const secondID = "01JSTORAGESECOND00000000"
	m.slots = append(m.slots, Slot{
		StorageID: secondID, Name: "shuttle", Root: second, Reachable: true,
		Backend:     newNamespaceBackend(BackendCopy, clonetree.Copy, second, "test", testLogger()),
		BackendName: BackendCopy,
	})

	const jobID = "01JOBONSECOND00000000000"
	if err := m.BindJobStorage(jobID, secondID); err != nil {
		t.Fatal(err)
	}
	s, err := m.jobSlot(jobID)
	if err != nil {
		t.Fatal(err)
	}

	const vid = "01VCOMMITTED000000000000"
	jid := jobID
	if err := m.registerCommitted(s, Committed{
		VersionID: vid, UDID: testUDID, Backend: BackendCopy, Kind: "full", JobID: &jid,
		CreatedAt: time.Now().UTC(), StructureVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	row, ok, err := st.GetVersion(vid)
	if err != nil || !ok {
		t.Fatalf("version not recorded: %v", err)
	}
	if row.StorageID == nil || *row.StorageID != secondID {
		t.Fatalf("the row must record the storage the TREE was written to, got %v want %q",
			row.StorageID, secondID)
	}

	// And is_latest must land in THAT storage's group. Promoting in the default's group would leave
	// the second storage's newest version never becoming its latest, so its browse_root has nothing
	// to resolve to — the same defect on the is_latest ruling.
	if !row.IsLatest {
		t.Error("the committed version must be latest in its own storage's group")
	}
}
