package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }

func unknownLookup(string) (KnownStorage, error) { return KnownStorage{}, nil }

func knownLookup(id string) StorageLookup {
	return func(string) (KnownStorage, error) { return KnownStorage{Known: true, StorageID: id}, nil }
}

func probeAs(b string) func(string) string { return func(string) string { return b } }

func idGen(id string) func() string { return func() string { return id } }

// --- the creation moment ---

func TestResolveCreatesWhenPathIsNewAndUnknown(t *testing.T) {
	root := t.TempDir()
	st, err := ResolveStorage("local", root, probeAs(BackendReflink), unknownLookup,
		fixedNow, "test-1.2.3", idGen("01JNEW000000000000000000"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !st.Verified {
		t.Error("creation probes and freezes, so it is verified by construction")
	}
	if st.Resolution != ResolutionCreated || !st.Resolution.OK() {
		t.Fatalf("want created, got %q (%s)", st.Resolution, st.Reason)
	}
	if st.Backend != BackendReflink || st.StorageID != "01JNEW000000000000000000" {
		t.Errorf("unexpected identity: %+v", st)
	}
	m, err := ReadStorageMarker(root)
	if err != nil {
		t.Fatalf("marker must exist after creation: %v", err)
	}
	if m.Backend != BackendReflink {
		t.Errorf("marker backend = %q", m.Backend)
	}
}

func TestResolveOpensWhenTheMarkerAgrees(t *testing.T) {
	root := t.TempDir()
	if err := WriteStorageMarker(root, StorageMarker{
		StorageID: "01JOLD000000000000000000", Backend: BackendZFS, CreatedAt: "2026-07-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	st, err := ResolveStorage("pool", root, probeAs(BackendZFS), knownLookup("01JOLD000000000000000000"),
		fixedNow, "test", idGen("SHOULD-NOT-BE-USED"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if st.Resolution != ResolutionOpened || st.StorageID != "01JOLD000000000000000000" {
		t.Fatalf("want opened with the existing id, got %+v", st)
	}
	if !st.Verified {
		t.Error("a probe that agreed with the marker must be reported as verified")
	}
}

// An UNPROBEABLE storage that already has a marker opens on the marker alone — and must say so.
//
// The asymmetry with the creation path is deliberate: an undetermined backend REFUSES to create
// (that guess would be frozen forever) but does not refuse to open (opening freezes nothing, and
// refusing every backup because a probe hiccuped is worse than the problem). What must not happen
// is ResolutionOpened being read as evidence a comparison ran, since Mismatch declines to call an
// empty probe a disagreement. Found at review (quince#410); the doc claimed a check that had been
// skipped.
//
// Same shape as quince#363's wifi_sync_unconfirmed vs wifi_sync_not_applied: "could not check" is
// its own fact, not a flavour of a verified outcome.
func TestResolveOpensUnverifiedWhenTheProbeCannotDetermineABackend(t *testing.T) {
	root := t.TempDir()
	if err := WriteStorageMarker(root, StorageMarker{
		StorageID: "01JOLD000000000000000000", Backend: BackendZFS, CreatedAt: "2026-07-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	st, err := ResolveStorage("pool", root, probeAs(""), knownLookup("01JOLD000000000000000000"),
		fixedNow, "test", idGen("x"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if st.Resolution != ResolutionOpened || !st.Resolution.OK() {
		t.Fatalf("an unprobeable existing storage must still open, got %q", st.Resolution)
	}
	if st.Verified {
		t.Error("nothing was compared, so Verified must be false")
	}
	if st.Backend != BackendZFS {
		t.Errorf("the backend must come from the marker, got %q", st.Backend)
	}
	// The state is recorded rather than silent: a caller about to move tens of gigabytes can see
	// that nothing confirmed the medium.
	for _, want := range []string{"could not be probed", "UNVERIFIED"} {
		if !strings.Contains(st.Reason, want) {
			t.Errorf("an unverified open must say so; missing %q in: %s", want, st.Reason)
		}
	}
}

// --- G5b: THE UNMOUNTED MOUNTPOINT ---
//
// The failure this guards is silent and its symptom is a full system disk, so it asserts all four
// negatives rather than just the refusal.
func TestResolveRefusesAnEmptyPathForAKnownStorage(t *testing.T) {
	root := t.TempDir() // readable, empty: exactly a mountpoint with nothing mounted on it
	probed := 0
	probe := func(string) string { probed++; return BackendCopy } // the ROOT filesystem's backend

	st, err := ResolveStorage("usb", root, probe, knownLookup("01JUSB000000000000000000"),
		fixedNow, "test", idGen("01JWRONG00000000000000000"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// 1. it refuses
	if st.Resolution != ResolutionMissingMedium || st.Resolution.OK() {
		t.Fatalf("want missing_medium and NOT ok, got %q", st.Resolution)
	}
	// 2. it does not re-probe (which is what would pick `copy` over the disk's real backend)
	if probed != 0 {
		t.Errorf("a known storage with an absent medium must NOT be re-probed; probed %d time(s)", probed)
	}
	// 3. it writes no second marker into the mountpoint
	if _, err := os.Stat(filepath.Join(root, StorageMarkerName)); !os.IsNotExist(err) {
		t.Errorf("a second %s was written into the mountpoint: %v", StorageMarkerName, err)
	}
	// 4. the refusal explains itself — a user with an unplugged disk must be told that, not
	//    handed an error about a directory.
	for _, want := range []string{"medium is ABSENT", "mountpoint", "Mount it"} {
		if !strings.Contains(st.Reason, want) {
			t.Errorf("refusal must name the real cause; missing %q in: %s", want, st.Reason)
		}
	}
}

// The residual, pinned so it is a KNOWN limitation rather than a surprise: with neither marker nor
// row, quince cannot tell a first declaration from an absent medium, and creates. This test exists
// to fail loudly if someone later believes the guard covers this case.
func TestResolveCreatesOnAFirstDeclarationEvenIfTheMediumIsAbsent(t *testing.T) {
	root := t.TempDir()
	st, err := ResolveStorage("usb", root, probeAs(BackendCopy), unknownLookup,
		fixedNow, "test", idGen("01JFIRST0000000000000000"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !st.Verified {
		t.Error("creation probes and freezes, so it is verified by construction")
	}
	if st.Resolution != ResolutionCreated {
		t.Fatalf("documented residual: a first declaration creates; got %q", st.Resolution)
	}
}

// --- the other refusals ---

func TestResolveRefusesABackendMismatch(t *testing.T) {
	root := t.TempDir()
	if err := WriteStorageMarker(root, StorageMarker{
		StorageID: "01JX00000000000000000000", Backend: BackendZFS, CreatedAt: "2026-07-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st, err := ResolveStorage("pool", root, probeAs(BackendCopy), knownLookup("01JX00000000000000000000"),
		fixedNow, "test", idGen("x"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if st.Resolution != ResolutionBackendMismatch || st.Resolution.OK() {
		t.Fatalf("a remount must refuse, got %q", st.Resolution)
	}
	if !strings.Contains(st.Reason, BackendZFS) || !strings.Contains(st.Reason, BackendCopy) {
		t.Errorf("the reason must name both backends: %s", st.Reason)
	}
}

func TestResolveRefusesACorruptMarker(t *testing.T) {
	root := t.TempDir()
	raw := `{"storage_id":"x","backend":"zfs","created_at":"","app_version":"","checksum":"bogus"}`
	if err := os.WriteFile(filepath.Join(root, StorageMarkerName), []byte(raw), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, err := ResolveStorage("pool", root, probeAs(BackendZFS), knownLookup("x"),
		fixedNow, "test", idGen("y"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if st.Resolution != ResolutionCorruptMarker || st.Resolution.OK() {
		t.Fatalf("a corrupt marker must refuse, got %q", st.Resolution)
	}
	// "damaged, not absent" is the distinction that keeps this off the creation path.
	if !strings.Contains(st.Reason, "damaged, not absent") {
		t.Errorf("the reason must distinguish damaged from absent: %s", st.Reason)
	}
}

func TestResolveRefusesAnUnreachablePath(t *testing.T) {
	st, err := ResolveStorage("gone", filepath.Join(t.TempDir(), "nope"), probeAs(BackendCopy),
		unknownLookup, fixedNow, "test", idGen("z"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if st.Resolution != ResolutionUnreachable || st.Resolution.OK() {
		t.Fatalf("want unreachable, got %q", st.Resolution)
	}
}

// G5c — the sibling to G5b, for the case G5b structurally cannot reach (quince#415).
//
// G5b tests "marker removed, path still readable" — the unplugged disk. This tests a path that DOES
// NOT EXIST at the moment of decision, which is the ordinary typo in a hand-edited config.yml. The
// bug it guards was worse than G5b's: quince invented the directory beside the real root, wrote a
// valid marker, reported `created verified=true`, and sent backups there while the real storage sat
// untouched — signalled only by a CREATED warning identical to a legitimate first run.
//
// THE PROBE MUST NOT BE REACHED, and counting its calls is what pins the ordering rather than the
// symptom: `probeNamespace` does os.MkdirAll, so ANY arrangement where a probe runs before this
// decision re-creates the bug by a new route. A stricter reachable() alone would not.
//
// And `writes nothing`, asserted on the PARENT — a refusal that still left the directory behind
// would have done half the damage.
func TestResolveNeverCreatesTheStorageRootItWasPointedAt(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "typoo")

	probed := 0
	probe := func(string) string { probed++; return BackendCopy }

	st, err := ResolveStorage("local", missing, probe, unknownLookup,
		fixedNow, "test", idGen("01JWRONG00000000000000000"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if st.Resolution.OK() {
		t.Fatalf("a path that does not exist must never resolve OK, got %q", st.Resolution)
	}
	if probed != 0 {
		t.Errorf("the guard must run BEFORE anything touches the path; probe called %d time(s)", probed)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("quince created the storage root it was pointed at: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a refusal must leave NOTHING behind; parent holds %d entr(ies)", len(entries))
	}
}

// An unknown probe must not become a created storage with a guessed backend: the backend is frozen
// forever at this moment, so guessing here is guessing permanently.
func TestResolveRefusesToCreateWithAnUndeterminedBackend(t *testing.T) {
	root := t.TempDir()
	st, err := ResolveStorage("local", root, probeAs(""), unknownLookup,
		fixedNow, "test", idGen("q"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if st.Resolution.OK() {
		t.Fatalf("must not create with an undetermined backend, got %q", st.Resolution)
	}
	if _, err := os.Stat(filepath.Join(root, StorageMarkerName)); !os.IsNotExist(err) {
		t.Error("no marker may be written when the backend is unknown")
	}
}

// --- attribution at commit and adopt (qn.6c story 3, first slice) ---
//
// `versions.storage_id` must record where a backup lives AT THE MOMENT IT IS MADE. Before this,
// registerCommitted and adopt built their rows without it, so a freshly committed version was
// inserted NULL and only picked up by the next startup sweep — the wire said "not yet attributed"
// about a version quince had just written itself, until a restart.
//
// The sweep is also the thing that stops being safe once there is more than one storage: it
// attributes every unattributed row to whichever storage ran it. Recording the fact at the source
// is what removes the need to guess later.

func TestManagerAttributesItsStorageID(t *testing.T) {
	m := &Manager{storageID: "01JSTORAGE0000000000000000"}
	got := m.storageIDPtr()
	if got == nil || *got != "01JSTORAGE0000000000000000" {
		t.Fatalf("want the manager's storage id, got %v", got)
	}
}

// An unconfigured Manager must insert NULL, not "". They are different states on the wire and ""
// is not one of them: contracts §2 says null means NOT YET ATTRIBUTED, and an empty string would
// be a value that no consumer has a rule for.
func TestManagerWithNoStorageIDAttributesNullNotEmptyString(t *testing.T) {
	m := &Manager{}
	if got := m.storageIDPtr(); got != nil {
		t.Fatalf("an unattributed Manager must yield nil, got %q", *got)
	}
}
