package storage

import (
	"strings"
	"sync"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// qn.6g PR 3 — THE MANAGER SURVIVES A MOVING LIST. This is the spec's G9, and it is the gate the
// rung stands on.
//
// WHY IT IS THE SHARP ONE. `Slot` holds a `Backend` INTERFACE, so a torn read is an itab/data
// mismatch — a segfault, not a wrong answer — which is why `Manager.mu` exists at all. Until now the
// only runtime mutation was `RecheckStorage` replacing ONE slot in place; this PR is the first thing
// that replaces the LIST, so every position-based read becomes an index into a slice another
// goroutine may just have shortened.
//
// There is no wiring yet — `ApplyStorages` is called by nobody in production until PR 4 — so these
// drive it directly. That is the point of slicing it this way: the Manager's behaviour under a
// moving list is provable in isolation, before anything depends on it.

// movingSlot builds a usable slot on its own root, the same way newNSManager builds its one slot.
func movingSlot(t *testing.T, id, name string) Slot {
	t.Helper()
	root := t.TempDir()
	be := newNamespaceBackend(BackendCopy, clonetree.Copy, root, "test", testLogger())
	seedStorageMarker(t, root, id, BackendCopy)
	return Slot{
		StorageID: id, Name: name, Root: root, Backend: be, BackendName: BackendCopy,
		Reachable: true, Retention: generousPolicy(),
	}
}

// G9 — CONCURRENT READERS AGAINST A LIST BEING REPLACED. Every reader that touches position or
// indexes the slice runs while another goroutine swaps the list under it, including down to one
// entry and back up. Under `-race` this is the assertion; a panic or a torn Backend fails it
// outright.
func TestReadersSurviveAListBeingReplaced(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())

	a := movingSlot(t, "01JA0000000000000000000A", "alpha")
	b := movingSlot(t, "01JB0000000000000000000B", "beta")
	c := movingSlot(t, "01JC0000000000000000000C", "gamma")
	m.slots = []Slot{a, b, c}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// The writer: shrink to one, grow to three, reorder — the three shapes a config edit produces.
	wg.Add(1)
	go func() {
		defer wg.Done()
		shapes := [][]Slot{{a}, {a, b, c}, {b, a}, {c, b, a}, {a, b, c}}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			m.ApplyStorages(append([]Slot(nil), shapes[i%len(shapes)]...))
		}
	}()

	// The readers: every path that reads position or indexes the slice.
	readers := []func(){
		func() { m.Storages("") },
		func() { m.Storages(testUDID) },
		func() { m.RecheckStorage("beta") },
		func() { m.ResolveChoice("") },
		func() { m.ResolveChoice("01JB0000000000000000000B") },
		func() { m.BackendName() },
		func() { m.storageIDPtr() },
		func() { m.defaultSlot() },
		func() { m.policyFor("") },
		func() { m.policyFor("01JC0000000000000000000C") },
		func() { _, _ = m.jobSlot("no-such-job") },
	}
	for _, r := range readers {
		wg.Add(1)
		go func(read func()) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				read()
			}
		}(r)
	}

	// Long enough for the writer to complete many full cycles against every reader.
	for i := 0; i < 2000; i++ {
		m.Storages("")
	}
	close(stop)
	wg.Wait()
}

// The list can shrink to ONE while a reader holds a position that no longer exists. Asserted
// separately from the fuzz above because the fuzz proves "no crash" and this proves the ANSWER:
// after an apply, the default is the new first entry rather than a stale index into the old list.
func TestTheDefaultFollowsTheNewList(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	a := movingSlot(t, "01JA0000000000000000000A", "alpha")
	b := movingSlot(t, "01JB0000000000000000000B", "beta")
	m.slots = []Slot{a, b}

	if d, ok := m.defaultSlot(); !ok || d.Name != "alpha" {
		t.Fatalf("setup: default is %+v ok=%v, want alpha", d.Name, ok)
	}
	m.ApplyStorages([]Slot{b, a}) // the user made beta the default
	d, ok := m.defaultSlot()
	if !ok || d.Name != "beta" {
		t.Errorf("after apply the default is %q (ok=%v), want beta — position IS the default, so a "+
			"reorder must move it", d.Name, ok)
	}
	got, status, reason := m.ResolveChoice("")
	if status != 0 {
		t.Fatalf("ResolveChoice(\"\") refused with %d: %s", status, reason)
	}
	if got != b.StorageID {
		t.Errorf("ResolveChoice(\"\") = %q, want beta's id — an unnamed backup must go to the NEW default", got)
	}
}

// AN EMPTY LIST IS REFUSED, and the Manager keeps what it was serving. `config.CheckStorages`
// refuses an empty declaration before any write, so this should be unreachable — it is guarded
// because "unreachable" and "cannot happen" are different claims, and by the time an applier runs
// the file is already on disk, so a panic would take the daemon down over a document it cannot undo.
func TestApplyRefusesAnEmptyList(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	a := movingSlot(t, "01JA0000000000000000000A", "alpha")
	m.slots = []Slot{a}

	warns := m.ApplyStorages(nil)
	if len(warns) == 0 {
		t.Error("an empty apply returned no warning — it would be a silent refusal, which is exactly " +
			"what `no silent caps or fallbacks` forbids")
	}
	if d, ok := m.defaultSlot(); !ok || d.Name != "alpha" {
		t.Errorf("the Manager did not keep what it was serving: default=%q ok=%v", d.Name, ok)
	}
}

// EVERY POSITION READER SURVIVES AN EMPTY LIST — the guards, asserted individually rather than only
// through the fuzz, because the fuzz can never produce an empty list (ApplyStorages refuses one) and
// these guards exist for the case where something else does.
func TestEveryPositionReaderSurvivesAnEmptyList(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	m.mu.Lock()
	m.slots = nil // bypasses ApplyStorages deliberately: this is the state it refuses to create
	m.mu.Unlock()

	if _, ok := m.defaultSlot(); ok {
		t.Error("defaultSlot reported ok on an empty list")
	}
	if got := m.BackendName(); got != "unknown" {
		t.Errorf("BackendName on an empty list = %q, want \"unknown\" — the wire's own value for "+
			"\"quince does not know\", rather than an empty string that reads as \"none\"", got)
	}
	if p := m.storageIDPtr(); p != nil {
		t.Errorf("storageIDPtr on an empty list = %q, want nil", *p)
	}
	if _, ok := m.policyFor(""); ok {
		t.Error("policyFor reported a policy on an empty list")
	}
	if got := m.Storages(""); len(got) != 0 {
		t.Errorf("Storages on an empty list returned %d entries", len(got))
	}
	if _, ok := m.RecheckStorage("alpha"); ok {
		t.Error("RecheckStorage found a storage on an empty list")
	}

	// The two that must REFUSE with a message rather than merely not crash.
	_, status, reason := m.ResolveChoice("")
	if status != 409 {
		t.Errorf("ResolveChoice on an empty list = %d, want 409", status)
	}
	if !strings.Contains(reason, "no storage is declared") {
		t.Errorf("ResolveChoice's reason %q does not name the actual condition — an absent "+
			"declaration and an unreachable disk are two states and must not share one message", reason)
	}
	if _, err := m.jobSlot("j1"); err == nil {
		t.Error("jobSlot returned a slot on an empty list")
	}
	// Reset, through the SHIPPED op. This asserted `Manager.RepairWorkingCopy` errored, and that
	// wrapper is gone (quince#509) — it was dead and resolved to the default slot. `RepairWorking`
	// cannot nil-deref here for a structural reason rather than a guarded one: it iterates the
	// slots and an unusable one goes to `blind` without its Backend being touched. So the claim
	// that survives is the one this test is about — every position reader survives an empty list.
	if status, reason := m.RepairWorking(testUDID, ""); status != 202 {
		t.Errorf("RepairWorking on an empty list = %d %q, want 202 — nothing is dirty because "+
			"nothing is declared, and that is an answer rather than a failure", status, reason)
	}
}

// RESET AGAINST AN UNREACHABLE-ONLY LIST MUST NAME THE STORAGE, which is the only thing that tells
// a user WHICH disk to reconnect. `Slot.Reachable`'s own doc says the Backend is NIL when it is
// false, so this state is where an unconditional dereference would crash.
//
// It asserted `Manager.RepairWorkingCopy` returned an error naming the storage. That wrapper is
// gone (quince#509), and the shipped op answers differently BY DESIGN rather than by accident:
// `RepairWorking` returns 202 with the storage named in the `NOT inspected` clause, because
// reset.go's rule is that unreachable storages are named and never silently skipped — "no silent
// caps or fallbacks" applies to what could not be examined, not only to what changed.
//
// SO THE STATUS IS DELIBERATELY NOT ASSERTED AS AN ERROR HERE. The claim this test carries is the
// naming, and the naming is what a user acts on. Whether 202 is the right code for "the only
// declared storage is unreachable" is a live question about the shipped endpoint, not about the
// deleted wrapper, and quince#509 does not rule on it.
func TestResetNamesAnUnreachableStorageItCouldNotInspect(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	m.ApplyStorages([]Slot{{
		StorageID: "01JG0000000000000000000G", Name: "gone", Root: "/nowhere",
		Reachable: false, UnreachableCode: "path_unreachable", UnreachableReason: "not mounted",
	}})

	status, reason := m.RepairWorking(testUDID, "")
	if status != 202 {
		t.Fatalf("RepairWorking on an unreachable-only list = %d %q, want 202", status, reason)
	}
	if !strings.Contains(reason, "gone") {
		t.Errorf("the reason %q does not name the storage, which is the only thing that tells a user "+
			"WHICH disk to reconnect", reason)
	}
	if !strings.Contains(reason, "NOT inspected") {
		t.Errorf("the reason %q does not say the storage was not INSPECTED — a reset that examined "+
			"nothing must not read as a reset that found nothing", reason)
	}
}

// A recheck for a storage FORGOTTEN while the re-probe was in flight reports a miss rather than a
// zero-valued card. This is the window that made renderSlot take a name instead of an index.
func TestRecheckOfAStorageForgottenMidFlightIsAMiss(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	a := movingSlot(t, "01JA0000000000000000000A", "alpha")
	b := movingSlot(t, "01JB0000000000000000000B", "beta")
	m.slots = []Slot{a, b}

	// The refresher drops `beta` from the list while it is being re-probed — the filesystem work
	// happens outside the lock, which is exactly the window a config apply can land in.
	m.SetRefresher(func(name string) (Slot, bool) {
		m.ApplyStorages([]Slot{a})
		return Slot{}, false
	})

	if _, ok := m.RecheckStorage("beta"); ok {
		t.Error("recheck reported a storage that was forgotten while it was being re-probed — the " +
			"card would render from a zero Slot for something no longer declared")
	}
}
