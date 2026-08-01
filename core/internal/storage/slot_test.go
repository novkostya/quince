package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// qn.6c story 3. These are the tests the change exists for: every other test in this package holds
// exactly ONE slot, and against one slot a resolver that ignores its argument and returns slots[0]
// passes everything. Two slots is what makes the difference observable.

const (
	slotAID = "01JSTORAGEA00000000000000"
	slotBID = "01JSTORAGEB00000000000000"
)

// twoSlots is A-then-B, so A is the default. Resolving to B therefore cannot be an accident of
// declaration order.
func twoSlots() []Slot {
	return []Slot{
		{StorageID: slotAID, Name: "internal", Root: "/srv/a", BackendName: BackendZFS},
		{StorageID: slotBID, Name: "shuttle", Root: "/srv/b", BackendName: BackendCopy},
	}
}

func TestSlotForResolvesTheVersionsOwnStorage(t *testing.T) {
	m := &Manager{slots: twoSlots()}

	id := slotBID
	got, ok := m.slotFor(&id)
	if !ok {
		t.Fatal("a version attributed to a configured storage must resolve")
	}
	if got.Root != "/srv/b" {
		t.Errorf("want storage B's root, got %q — resolving to the default is the cross-storage bug", got.Root)
	}
}

// An id no configured storage carries must FAIL rather than fall back to the default. The fallback
// is the tempting shape and it is the wrong one: it would answer confidently with another storage's
// root, which is worse than answering not-found.
func TestSlotForRefusesAnUnknownStorage(t *testing.T) {
	m := &Manager{slots: twoSlots()}

	unknown := "01JSTORAGEZ00000000000000"
	if _, ok := m.slotFor(&unknown); ok {
		t.Error("an unknown storage id must not resolve — guessing a root invents a fact")
	}
}

// A nil storage_id is the pre-qn.6c world. It resolves only if an UNATTRIBUTED slot exists, and
// two attributed slots is exactly the case where it must not: quince knows about two storages and
// cannot say which of them an unattributed version sits on.
func TestSlotForRefusesNilAgainstAttributedStorages(t *testing.T) {
	if _, ok := (&Manager{slots: twoSlots()}).slotFor(nil); ok {
		t.Error("an unattributed version must not resolve against attributed storages")
	}
	if _, ok := (&Manager{slots: []Slot{{Root: "/srv/a"}}}).slotFor(nil); !ok {
		t.Error("an unattributed version must resolve against the unattributed storage")
	}
}

// The call site, which is the assertion that matters. Testing slotFor alone would leave the
// possibility that toWire never calls it — the defect shape quince#417 caught, where the helper was
// proven and the birth site was not.
func TestBrowseRootRendersUnderTheVersionsOwnStorage(t *testing.T) {
	m := &Manager{slots: twoSlots()}

	id := slotBID
	v := m.toWire(store.VersionRow{
		ID: "01J0V0000000000000000000", UDID: "00008140-000A1B2C3D4E5F60",
		Backend: BackendCopy, Kind: "full", IsLatest: true, StorageID: &id,
		CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	})

	if !strings.HasPrefix(v.BrowseRoot, "/srv/b/") {
		t.Errorf("browse_root must sit under storage B, got %q", v.BrowseRoot)
	}
	if strings.HasPrefix(v.BrowseRoot, "/srv/a/") {
		t.Errorf("browse_root resolved against the DEFAULT storage rather than the version's own: %q", v.BrowseRoot)
	}
}

// The same call site, on the version whose storage quince cannot name. "" is chosen over a guessed
// path deliberately: browse_root is a non-nullable string, and an empty one is visibly broken where
// a plausible-looking wrong path is not.
//
// EXACT equality, and the first version of this test is why. It asserted only that the result held
// no configured root — which a RELATIVE path satisfies, and a relative path is exactly what the
// code produced, because filepath.Join drops an empty element rather than propagating it. The test
// was named "IsEmpty" and never checked emptiness (quince#433 review). A near-miss assertion on the
// value you actually care about is worth less than no assertion, because it reads as coverage.
//
// Both shapes are pinned: is_latest true and false take different branches through browseRoot, and
// only one of them was ever exercised here.
func TestBrowseRootIsEmptyForAnUnresolvableStorage(t *testing.T) {
	unknown := "01JSTORAGEZ00000000000000"
	for _, latest := range []bool{true, false} {
		m := &Manager{slots: twoSlots()}
		v := m.toWire(store.VersionRow{
			ID: "01J0V0000000000000000000", UDID: "00008140-000A1B2C3D4E5F60",
			Backend: BackendCopy, Kind: "full", IsLatest: latest, StorageID: &unknown,
			CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		})
		if v.BrowseRoot != "" {
			t.Errorf("is_latest=%v: browse_root must be exactly empty for an unresolvable storage, got %q",
				latest, v.BrowseRoot)
		}
	}
}

// The NULL-row-against-an-attributed-Manager window, which is the reachable half of the finding.
// quince#422 moved the attribution sweep above reconciliation so this closes at startup — but the
// sweep LOGS AND RETURNS on error, and then every version on the device renders through here.
func TestBrowseRootIsEmptyForAnUnattributedRowOnAnAttributedManager(t *testing.T) {
	m := &Manager{slots: []Slot{{StorageID: slotAID, Root: "/srv/a", BackendName: BackendZFS}}}

	v := m.toWire(store.VersionRow{
		ID: "01J0V0000000000000000000", UDID: "00008140-000A1B2C3D4E5F60",
		Backend: BackendZFS, Kind: "full", IsLatest: true, // StorageID nil
		CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	})

	if v.BrowseRoot != "" {
		t.Errorf("an unattributed row on an attributed Manager must yield no path, got %q", v.BrowseRoot)
	}
}

// NewManager panics on an empty slot list rather than producing an index-out-of-range three calls
// later. config.CheckStorages refuses to serve without a declared storage, so reaching here with
// none is a programming error, not a state to degrade through.
func TestNewManagerRefusesNoSlots(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewManager must panic on an empty slot list")
		}
	}()
	NewManager(nil, nil, nil, nil, RetentionPolicy{}, nil, nil)
}
