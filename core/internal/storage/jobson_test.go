package storage

import (
	"reflect"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// qn.6g — `Manager.JobsOn`, the liveness question `DELETE /api/config/storage/{name}` asks before it
// writes (Operator ruling 2026-08-06, quince#577, option (b)).
//
// The HANDLER's half — the 422, its shape, the name → id join — is asserted in `internal/httpapi`.
// What lives here is everything that depends on the binding map, which is this package's.

// twoSlotManager builds a Manager with two usable, id-bearing storages, so a binding can be
// attributed to one of them rather than to "the only one there is".
//
// `newNSManager`'s single slot carries an EMPTY StorageID, which cannot exercise this at all: every
// question about it would be a question about the empty-id guard.
func twoSlotManager(t *testing.T) *Manager {
	t.Helper()
	st := openStore(t)
	slot := func(name, id string) Slot {
		root := t.TempDir()
		be := newNamespaceBackend(BackendCopy, clonetree.Copy, root, "test", testLogger())
		seedStorageMarker(t, root, id, BackendCopy)
		return Slot{Name: name, Root: root, StorageID: id, Backend: be,
			BackendName: BackendCopy, Reachable: true, Retention: generousPolicy()}
	}
	return NewManager([]Slot{slot("pool", "01STORAGEPOOL"), slot("shuttle", "01STORAGESHUTTLE")},
		st, st, bus.New(), seqID(), testLogger())
}

// The binding is what makes a storage busy, and it is SCOPED: a job on pool says nothing about
// shuttle. A JobsOn that ignored the id would make every forget refuse on a busy install.
func TestJobsOnReportsOnlyTheJobsBoundToThatStorage(t *testing.T) {
	m := twoSlotManager(t)
	if err := m.BindJobStorage("01JOBPOOL", "01STORAGEPOOL"); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if got := m.JobsOn("01STORAGEPOOL"); !reflect.DeepEqual(got, []string{"01JOBPOOL"}) {
		t.Errorf("JobsOn(pool) = %v, want [01JOBPOOL]", got)
	}
	if got := m.JobsOn("01STORAGESHUTTLE"); len(got) != 0 {
		t.Errorf("JobsOn(shuttle) = %v, want none — a job on another disk must not block this "+
			"storage's forget", got)
	}
}

// SORTED, and this is the assertion that a map iteration cannot satisfy by luck.
//
// The refusal message names `busy[0]`, so an unsorted answer would name a DIFFERENT job on
// successive identical requests — a user cancels what the message said, retries, and is told about
// another one with no explanation of where it came from. Go randomises map iteration deliberately,
// so this is a real difference rather than a theoretical one.
func TestJobsOnIsSortedSoTheRefusalNamesTheSameJobTwice(t *testing.T) {
	m := twoSlotManager(t)
	for _, id := range []string{"01JOBC", "01JOBA", "01JOBB"} {
		if err := m.BindJobStorage(id, "01STORAGEPOOL"); err != nil {
			t.Fatalf("bind %s: %v", id, err)
		}
	}

	want := []string{"01JOBA", "01JOBB", "01JOBC"}
	for i := 0; i < 20; i++ { // map order varies per range, so one call proves nothing
		if got := m.JobsOn("01STORAGEPOOL"); !reflect.DeepEqual(got, want) {
			t.Fatalf("call %d: JobsOn = %v, want %v — the refusal quotes the first element, and a "+
				"message that names a different job each time is unactionable", i, got, want)
		}
	}
}

// AN UNBINDING CLEARS IT, which is what makes the refusal temporary rather than permanent.
//
// The remedy in the message is *"wait for it to finish, or cancel it"*, and that sentence is only
// true if a finished job stops counting. `UnbindJob` is what the engine calls at job end.
func TestJobsOnStopsReportingAFinishedJob(t *testing.T) {
	m := twoSlotManager(t)
	if err := m.BindJobStorage("01JOBPOOL", "01STORAGEPOOL"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	m.UnbindJob("01JOBPOOL")

	if got := m.JobsOn("01STORAGEPOOL"); len(got) != 0 {
		t.Errorf("JobsOn after UnbindJob = %v, want none — the refusal promises that waiting for "+
			"the job clears it, and this is what makes that true", got)
	}
}

// THE EMPTY ID IS NOT A WILDCARD, and this is the guard that keeps an unplugged disk removable.
//
// An empty storage_id means quince has never reached the path, so no backup was ever bound to it —
// and the storage a user most wants to forget is precisely the one that never came up (quince#570).
//
// THE BINDING IS PLANTED DIRECTLY, and that is the only way to test this rather than a shortcut.
// `BindJobStorage` cannot produce an empty-id binding — it refuses an unreachable slot, and an
// empty id is what an unreachable one has — so a test that bound through the public path would find
// nothing in the map and pass with the guard DELETED. That is exactly the shape of a test that
// cannot fail: the harness would be supplying the property under test. Planting it stages the state
// the guard exists for, where "" is a value in the map and the loop would otherwise match it.
func TestJobsOnAnEmptyStorageIDIsAlwaysNone(t *testing.T) {
	m := twoSlotManager(t)
	m.mu.Lock()
	m.jobStorage = map[string]string{"01JOBGHOST": ""}
	m.mu.Unlock()

	if got := m.JobsOn(""); got != nil {
		t.Errorf("JobsOn(\"\") = %v, want nil — a storage quince has never reached cannot have a "+
			"running backup, and refusing its forget would make an unplugged disk unremovable", got)
	}
}
