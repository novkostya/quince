package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
)

// qn.6c story 5c — the Storage wire object, ruled 2026-07-31 with the split cause added 2026-08-01.

func twoStorageManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = "01JSTORAGEDEFAULT0000000"
	m.slots[0].Name = "internal"
	m.slots = append(m.slots, Slot{
		StorageID: "01JSTORAGESHUTTLE0000000", Name: "shuttle", Root: "/mnt/shuttle",
		Reachable: false, UnreachableCode: "missing_medium", UnreachableReason: "the medium is not present",
	})
	return m, st
}

// The cause is TWO fields and both are present-and-null when reachable. Same reasoning as
// Version.storage_id: a present null is a fact, an absent key is a version-skew question.
func TestStorageCauseIsSplitAndNeverAbsent(t *testing.T) {
	m, _ := twoStorageManager(t)
	list := m.Storages("")
	if len(list) != 2 {
		t.Fatalf("want both storages listed, got %d", len(list))
	}

	b, err := json.Marshal(list[0])
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, key := range []string{`"unreachable_code":null`, `"unreachable_reason":null`} {
		if !strings.Contains(got, key) {
			t.Errorf("a reachable storage must carry %s — present and null, never omitted: %s", key, got)
		}
	}

	if list[1].UnreachableCode == nil || *list[1].UnreachableCode != "missing_medium" {
		t.Errorf("the code must be machine-readable, got %v", list[1].UnreachableCode)
	}
	// The prose must carry what the code cannot: which medium, in the daemon's own words.
	if list[1].UnreachableReason == nil || *list[1].UnreachableReason == "" {
		t.Error("the prose reason must be present — an enum alone cannot be shown to a user")
	}
}

// An unreached storage has no backend, and "unknown" says so rather than guessing. It is a declared
// enum value precisely so a client is not left reading "" as either "none" or "not sent".
func TestUnreachableStorageReportsUnknownBackend(t *testing.T) {
	m, _ := twoStorageManager(t)
	if got := m.Storages("")[1].Backend; got != "unknown" {
		t.Errorf("want backend \"unknown\" for a storage never reached, got %q", got)
	}
}

// Exactly one storage is default, and it is the first — declaration order is the contract.
func TestExactlyOneStorageIsDefault(t *testing.T) {
	m, _ := twoStorageManager(t)
	var n int
	for _, s := range m.Storages("") {
		if s.Default {
			n++
		}
	}
	if n != 1 {
		t.Errorf("exactly one storage must be default, got %d", n)
	}
	if !m.Storages("")[0].Default {
		t.Error("the default must be the first slot — declaration order decides it")
	}
}

// DEVICE-INDEPENDENT by ruling: will_be_full appears only with ?udid=. Putting a (device, storage)
// pair fact on the storage resource unconditionally would distort it for the rung that follows.
func TestWillBeFullIsNullWithoutAUdid(t *testing.T) {
	m, _ := twoStorageManager(t)
	for _, s := range m.Storages("") {
		if s.WillBeFull != nil {
			t.Errorf("%s: will_be_full must be null on the device-independent list, got %v", s.Name, *s.WillBeFull)
		}
	}
}

// Story 8's claim: the first backup to a storage with no prior version for THIS device is full, and
// the server owns the answer because only it knows the pair.
func TestWillBeFullIsPerDeviceAndPerStorage(t *testing.T) {
	m, st := twoStorageManager(t)
	def := m.slots[0].StorageID
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VHASONE", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full", StorageID: &def,
	}); err != nil {
		t.Fatal(err)
	}

	list := m.Storages(testUDID)
	if list[0].WillBeFull == nil || *list[0].WillBeFull {
		t.Error("the storage that already holds a version for this device must not claim a full transfer")
	}
	// The OTHER storage has no version for this device, so its next backup IS full — the warning
	// the user is owed before tens of gigabytes move.
	if list[1].WillBeFull == nil || !*list[1].WillBeFull {
		t.Error("a storage with no prior version for this device must report will_be_full")
	}
}

// A MISSING version does not count as a prior version: its artifact is gone, so the next backup
// transfers everything again. Claiming otherwise understates the cost.
func TestWillBeFullIgnoresMissingVersions(t *testing.T) {
	m, st := twoStorageManager(t)
	def := m.slots[0].StorageID
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VDEAD", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full", StorageID: &def, Missing: true,
	}); err != nil {
		t.Fatal(err)
	}
	list := m.Storages(testUDID)
	if list[0].WillBeFull == nil || !*list[0].WillBeFull {
		t.Error("a version whose artifact is gone must not suppress the full-transfer warning")
	}
}

// Recheck re-probes ONE storage through the injected refresher, and an unknown id is not found.
func TestRecheckUsesTheRefresherAndIsPerStorage(t *testing.T) {
	m, _ := twoStorageManager(t)

	var asked []string
	m.SetRefresher(func(name string) (Slot, bool) {
		asked = append(asked, name)
		return Slot{
			StorageID: "01JSTORAGESHUTTLE0000000", Name: "shuttle", Root: "/mnt/shuttle",
			Reachable: true, BackendName: BackendCopy, Backend: m.slots[0].Backend,
		}, true
	})

	got, ok := m.RecheckStorage("01JSTORAGESHUTTLE0000000")
	if !ok {
		t.Fatal("recheck must find a declared storage")
	}
	if !got.Reachable {
		t.Error("the refresher's new state must be returned — a recheck that reports the stale answer is not a recheck")
	}
	if len(asked) != 1 || asked[0] != "shuttle" {
		t.Errorf("exactly the asked-for storage must be re-probed, got %v", asked)
	}
	// And it PERSISTS, so the next GET agrees with the recheck rather than contradicting it.
	if !m.Storages("")[1].Reachable {
		t.Error("the rechecked state must persist on the Manager")
	}

	if _, ok := m.RecheckStorage("01JSTORAGENOSUCH00000000"); ok {
		t.Error("an unknown storage id must not resolve — that is the 404")
	}
}
