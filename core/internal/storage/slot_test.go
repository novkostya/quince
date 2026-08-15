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
// slot order.
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

// NewManager ACCEPTS AN EMPTY SLOT LIST, and this test is the inversion of one that required it to
// panic (qn.6e, Operator ruling 2026-08-07).
//
// The old test read: "NewManager panics on an empty slot list rather than producing an
// index-out-of-range three calls later. config.CheckStorages refuses to serve without a declared
// storage, so reaching here with none is a programming error, not a state to degrade through."
//
// ITS PREMISE WAS RETIRED, NOT ITS CAUTION. CheckStorages no longer refuses a zero-storage start: a
// first run has no `storage:` key at all, and quince serves so one can be added from the UI. Zero
// slots is therefore the FIRST-RUN state, and a panic there would be a crash on the one path every
// new user takes.
//
// The caution it protected is intact and lives at CALL time instead — qn.6g moved it there because
// the list can be replaced while the process runs, so "non-empty at construction" was already
// worthless as a guarantee. movinglist_test.go gates every reader on an empty list; this asserts
// only that CONSTRUCTING one is allowed.
// THIS ASSERTS CONSTRUCTION ONLY, and deliberately does not call a method on the result. Every
// dependency here is nil — as it was in the panic test this replaces — so a reader would segfault on
// the nil Registry rather than on anything to do with the empty list, and a one-slot Manager built
// the same way would fail identically. That would be a test about its own fixture.
//
// "An empty Manager ANSWERS rather than merely existing" is the claim that matters, and it is gated
// where real dependencies exist: `TestBuildStorageServesWithNoStoragesDeclared` in cmd/quince drives
// `Storages` and `ResolveChoice` through the constructor the daemon actually uses.
func TestNewManagerAcceptsNoSlots(t *testing.T) {
	if m := NewManager(nil, nil, nil, nil, nil, nil); m == nil {
		t.Fatal("NewManager returned nil for an empty slot list")
	}
}
