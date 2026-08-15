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

// Exactly one storage is default, and it is the first — SLOT order is the contract, which is not
// the order of `config.yml`. The caller hoists the entry carrying `default: true` before this type
// sees the list (quince#722), so at this layer `slots[0]` is the default, full stop.
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
		t.Error("the default must be the first slot — slot order decides it")
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

	got, ok := m.RecheckStorage("shuttle")
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

	if _, ok := m.RecheckStorage("nosuch"); ok {
		t.Error("an unknown storage NAME must not resolve — that is the 404")
	}
}

// THE RACE TEST (quince#445 review). It must hammer the SAME slot the recheck rewrites — the
// review's own first probe passed while racing a recheck of `shuttle` against a read of slot 0, so
// the two never touched the same memory and it proved nothing. A green race probe that never
// overlapped is the same class as a mutation that never applied.
//
// Run under `-race`, which the Go gate does.
func TestRecheckDoesNotRaceReadsOfTheSameSlot(t *testing.T) {
	m, _ := twoStorageManager(t)
	// The DEFAULT — the slot every read path touches. TWO handles on it now: recheck is keyed on
	// NAME (quince#610) while `slotFor` still resolves a job's `storage_id`, so the test needs both
	// and collapsing them would silently exercise one path twice.
	target := m.slots[0].StorageID
	targetName := m.slots[0].Name

	m.SetRefresher(func(name string) (Slot, bool) {
		return Slot{
			StorageID: target, Name: name, Root: "/backups",
			Reachable: true, BackendName: BackendCopy, Backend: m.slots[0].Backend,
		}, true
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			m.RecheckStorage(targetName)
		}
	}()
	for i := 0; i < 200; i++ {
		// Every reader shape that touches the slot list, including the one the review's probe
		// caught: BackendName reads the default slot's field while the recheck rewrites it.
		_ = m.BackendName()
		_ = m.Storages("")
		_, _ = m.slotFor(&target)
		_, _ = m.defaultSlot()
	}
	<-done
}
