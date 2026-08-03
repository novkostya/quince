package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
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
	// 5. IT CARRIES THE KNOWN UUID (quince#570). It used to appear only inside the Reason prose, so
	//    `Storage.id` reached the wire EMPTY for a storage quince had created and could name — and
	//    `id` is how a client scopes anything to a storage, `Version.storage_id` above all. An
	//    unplugged disk with no id has no discoverable history, which is the opposite of what an
	//    unplugged disk needs.
	if st.StorageID != "01JUSB000000000000000000" {
		t.Errorf("missing_medium must carry the known storage id, got %q — an unplugged disk with "+
			"an empty id cannot be scoped to its own versions", st.StorageID)
	}
}

// The OTHER half of the same field, pinned beside it so the asymmetry is deliberate rather than
// discovered: a storage that was NEVER created has no id, because none was ever minted. Carrying
// the known UUID for `missing_medium` NARROWS what `id` means; it does not invent one here.
func TestResolveUnreachableCarriesNoStorageID(t *testing.T) {
	st, err := ResolveStorage("usb", filepath.Join(t.TempDir(), "not-there"), probeAs(BackendCopy),
		unknownLookup, fixedNow, "test", idGen("01JNEVER0000000000000000"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if st.Resolution != ResolutionUnreachable {
		t.Fatalf("want unreachable, got %q", st.Resolution)
	}
	if st.StorageID != "" {
		t.Errorf("a storage that was never created must carry NO id, got %q — fabricating one "+
			"would invent an identity for a disk quince has never seen", st.StorageID)
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
	m := &Manager{slots: []Slot{{StorageID: testStorageID}}}
	got := m.storageIDPtr()
	if got == nil || *got != "01JSTORAGE0000000000000000" {
		t.Fatalf("want the manager's storage id, got %v", got)
	}
}

// An unconfigured Manager must insert NULL, not "". They are different states on the wire and ""
// is not one of them: contracts §2 says null means NOT YET ATTRIBUTED, and an empty string would
// be a value that no consumer has a rule for.
func TestManagerWithNoStorageIDAttributesNullNotEmptyString(t *testing.T) {
	m := &Manager{slots: []Slot{{}}}
	if got := m.storageIDPtr(); got != nil {
		t.Fatalf("an unattributed Manager must yield nil, got %q", *got)
	}
}

// The two tests above exercise the HELPER. These exercise the CALL SITES, which is where the
// regression would actually be (quince#417 review): deleting `StorageID: m.storageIDPtr()` from
// registerCommitted or adopt left both helper tests green, because neither went through a row's
// birth. A getter returning a field is not the claim this change makes.

const testStorageID = "01JSTORAGE0000000000000000"

func TestRegisterCommittedAttributesTheVersionToItsStorage(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, RetentionPolicy{})
	m.slots[0].StorageID = testStorageID

	const vid, udid = "01JV0000000000000000000001", "00008140-000A1B2C3D4E5F60"
	if err := m.registerCommitted(m.slots[0], Committed{
		VersionID: vid, UDID: udid, Backend: BackendCopy, Kind: "full",
		CreatedAt: time.Now().UTC(), StructureVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("registerCommitted: %v", err)
	}

	row, ok, err := st.GetVersion(vid)
	if err != nil || !ok {
		t.Fatalf("committed version missing: ok=%v err=%v", ok, err)
	}
	if row.StorageID == nil {
		t.Fatal("a version committed by a Manager with a storage must NOT be stored unattributed")
	}
	if *row.StorageID != testStorageID {
		t.Errorf("storage_id = %q, want %q", *row.StorageID, testStorageID)
	}
}

// The other direction, and the one a future refactor is most likely to get wrong: a Manager with
// no storage must write NULL, not "". contracts §2 has no rule for an empty storage_id, so it
// would be a value that reads as "attributed" while naming nothing.
func TestRegisterCommittedWithNoStorageWritesNullNotEmptyString(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, RetentionPolicy{}) // storageID "" by construction

	const vid, udid = "01JV0000000000000000000002", "00008140-000A1B2C3D4E5F60"
	if err := m.registerCommitted(m.slots[0], Committed{
		VersionID: vid, UDID: udid, Backend: BackendCopy, Kind: "full",
		CreatedAt: time.Now().UTC(), StructureVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("registerCommitted: %v", err)
	}

	row, _, err := st.GetVersion(vid)
	if err != nil {
		t.Fatal(err)
	}
	if row.StorageID != nil {
		t.Fatalf("want NULL for an unattributed Manager, got %q", *row.StorageID)
	}
}

// adopt is the second birth site: a version found on disk is attributed to the root it was
// SCANNED FROM, which is known at that point and never needs guessing later.
func TestAdoptAttributesTheVersionToTheStorageItWasScannedFrom(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, RetentionPolicy{})
	m.slots[0].StorageID = testStorageID

	const vid, udid = "01JV0000000000000000000003", "00008140-000A1B2C3D4E5F60"
	m.adopt(m.slots[0], udid, Artifact{
		UDID: udid, Backend: BackendCopy, IsLatest: true,
		Marker: Marker{
			VersionID: vid, UDID: udid, Kind: "full", Encrypted: true,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
	})

	row, ok, err := st.GetVersion(vid)
	if err != nil || !ok {
		t.Fatalf("adopted version missing: ok=%v err=%v", ok, err)
	}
	if row.StorageID == nil || *row.StorageID != testStorageID {
		t.Fatalf("an adopted version must carry the storage it was scanned from, got %v", row.StorageID)
	}
}

// --- the CONSEQUENCE of per-(device, storage) is_latest, not just the flag ---
//
// The tests in internal/store prove the flag is scoped correctly. This proves what the scoping is
// FOR (quince#418 review): browse_root resolves through is_latest, so a device's newest version on
// each storage must resolve to that storage's own `latest/`.
//
// I deferred this to G1 claiming it needed the registry. It does not — browseRoot is a pure
// function that takes the root as a parameter, so two calls with two roots assert the whole thing
// today. The end-to-end version (through toWire, two Managers) is still G1's; this is the cheap
// one that pins the failure a user would actually meet.
func TestBrowseRootResolvesPerStorageWhenEachHasItsOwnLatest(t *testing.T) {
	const udid = "00008140-000A1B2C3D4E5F60"
	rootA, rootB := "/srv/pool", "/mnt/usb"
	createdA := time.Date(2026, 7, 20, 3, 30, 0, 0, time.UTC)
	createdB := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)

	// Under the ruling BOTH are latest — one per storage — so each resolves to its own latest/.
	gotA := browseRoot(rootA, udid, BackendCopy, nil, true, createdA)
	gotB := browseRoot(rootB, udid, BackendCopy, nil, true, createdB)

	if want := latestDir(rootA, udid); gotA != want {
		t.Errorf("storage A: browse_root = %q, want %q", gotA, want)
	}
	if want := latestDir(rootB, udid); gotB != want {
		t.Errorf("storage B: browse_root = %q, want %q", gotB, want)
	}
	if gotA == gotB {
		t.Fatal("two storages must not resolve to the same browse_root")
	}
}

// THE FAILURE THE RULING EXISTS TO PREVENT, pinned as a negative.
//
// Under a single global latest, committing to storage B demotes storage A's newest version. This
// shows what that does: A's version — whose artifact is still sitting in latest/ — resolves to a
// versions/<ts>/ directory instead. The wire would hand the UI a path that does not exist, and
// Verify would report a perfectly good version as broken.
func TestBrowseRootPointsAtAMissingDirIfTheNewestVersionIsDemoted(t *testing.T) {
	const udid = "00008140-000A1B2C3D4E5F60"
	root := "/srv/pool"
	created := time.Date(2026, 7, 20, 3, 30, 0, 0, time.UTC)

	asLatest := browseRoot(root, udid, BackendCopy, nil, true, created)
	demoted := browseRoot(root, udid, BackendCopy, nil, false, created)

	if asLatest == demoted {
		t.Fatal("precondition: is_latest must change where browse_root points")
	}
	if want := latestDir(root, udid); asLatest != want {
		t.Errorf("latest resolves to %q, want %q", asLatest, want)
	}
	// The demoted path is under versions/, which for the NEWEST version is a directory that does
	// not exist — its content has not been rotated out of latest/.
	if want := nsVersionDir(root, udid, created); demoted != want {
		t.Errorf("demoted resolves to %q, want %q", demoted, want)
	}
	if !strings.Contains(demoted, "/versions/") {
		t.Errorf("the demoted path should be under versions/: %q", demoted)
	}
}

// --- storage-scoped reconciliation and seed kind (qn.6c story 3) ---

func TestOwnsIsGroupMembershipNotEquality(t *testing.T) {
	sid := "01JSTORAGE-A"
	other := "01JSTORAGE-B"

	unattributed := &Manager{slots: []Slot{{}}}
	attributed := &Manager{slots: []Slot{{StorageID: sid}}}

	// The NULL group is a real group: an unattributed Manager owns unattributed rows.
	if !unattributed.slots[0].owns(nil) {
		t.Error("an unattributed Manager must own unattributed rows — that is the pre-qn.6c world")
	}
	if unattributed.slots[0].owns(&sid) {
		t.Error("an unattributed Manager must not claim a row that knows where it lives")
	}
	// An attributed Manager must NOT claim a row whose storage is unknown.
	if attributed.slots[0].owns(nil) {
		t.Error("a NULL row is not this storage's — quince does not know where it lives")
	}
	if !attributed.slots[0].owns(&sid) {
		t.Error("a row on this storage is owned")
	}
	if attributed.slots[0].owns(&other) {
		t.Error("another storage's row is not ours to judge")
	}
}

// seedKind must not report `incremental` for a first backup to a NEW storage. That is a FULL
// transfer, and story 8's whole claim — telling the user before tens of gigabytes move — rests on
// this answer being per-storage.
func TestSeedKindIsFullForAFirstBackupToThisStorage(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, RetentionPolicy{})
	m.slots[0].StorageID = "01JSTORAGE-B" // a storage this device has never been backed up to

	const udid = "00008140-000A1B2C3D4E5F60"
	onStorageA := "01JSTORAGE-A"
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VA1", UDID: udid, Backend: BackendCopy, Kind: "full",
		CreatedAt: time.Now().UTC(), StorageID: &onStorageA,
	}); err != nil {
		t.Fatalf("seed a version on the OTHER storage: %v", err)
	}

	if got := m.seedKind(m.slots[0], udid); got != "full" {
		t.Fatalf("a first backup to a new storage is FULL, got %q — this is story 8's claim", got)
	}
}

// The control: a device that DOES have a version on this storage is incremental.
func TestSeedKindIsIncrementalWhenThisStorageAlreadyHasAVersion(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, RetentionPolicy{})
	sid := "01JSTORAGE-A"
	m.slots[0].StorageID = sid

	const udid = "00008140-000A1B2C3D4E5F60"
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VA1", UDID: udid, Backend: BackendCopy, Kind: "full",
		CreatedAt: time.Now().UTC(), StorageID: &sid,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if got := m.seedKind(m.slots[0], udid); got != "incremental" {
		t.Errorf("a device with a version on THIS storage is incremental, got %q", got)
	}
}

// The case my own first version got wrong, which is why membership is ONE helper.
//
// I hand-wrote the condition here instead of calling owns, and wrote `r.StorageID == nil` as
// "not ours" unconditionally. At storageID "" — the pre-qn.6c world, and what every existing test
// Manager is — that turned every unattributed row into "not ours" and reported `full` for a device
// that plainly has versions. The commit message for the very change warned that getting membership
// subtly different at each site is how these bugs arise; it arose in that commit.
func TestSeedKindTreatsUnattributedRowsAsOwnedByAnUnattributedManager(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, RetentionPolicy{}) // storageID "" by construction

	const udid = "00008140-000A1B2C3D4E5F60"
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VNULL1", UDID: udid, Backend: BackendCopy, Kind: "full",
		CreatedAt: time.Now().UTC(), // StorageID nil — pre-qn.6c
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if got := m.seedKind(m.slots[0], udid); got != "incremental" {
		t.Fatalf("an unattributed Manager owns unattributed rows, so this device HAS a version "+
			"here and the next backup is incremental; got %q", got)
	}
}

// THE FIRST-STARTUP-AFTER-UPGRADE CASE, which is what makes the attribution sweep's position
// load-bearing rather than incidental (quince#422 review).
//
// A pre-qn.6c row has storage_id NULL. Once ResolveStorage has created the marker the Manager is
// ATTRIBUTED, so it does not own that row — and on the first startup after upgrade that is EVERY
// row, while their artifacts sit on disk under this very root. If reconciliation runs before the
// sweep it sees an empty registry view, treats every artifact as unadopted, and tries to re-adopt
// the lot.
//
// This asserts the property directly: an attributed Manager must not consider a NULL row its own,
// AND the same rows must become its own once attributed. The ordering in buildStorage is what
// turns the second state into the one reconciliation actually meets.
func TestAttributedManagerDoesNotOwnPreUpgradeRowsUntilTheyAreSwept(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, RetentionPolicy{})
	sid := "01JSTORAGE-A"

	const udid = "00008140-000A1B2C3D4E5F60"
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VOLD1", UDID: udid, Backend: BackendCopy, Kind: "full",
		CreatedAt: time.Now().UTC(), // NULL storage_id — the pre-qn.6c row
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// After ResolveStorage, the Manager carries an id while the row does not.
	m.slots[0].StorageID = sid
	row, _, err := st.GetVersion("01VOLD1")
	if err != nil {
		t.Fatal(err)
	}
	if m.slots[0].owns(row.StorageID) {
		t.Fatal("precondition: an attributed Manager must NOT own a NULL row — that is the whole hazard")
	}

	// Attribution is what closes it — now done during reconciliation, from what Scan found.
	if err := st.AttributeVersion("01VOLD1", sid); err != nil {
		t.Fatalf("attribute: %v", err)
	}
	row, _, err = st.GetVersion("01VOLD1")
	if err != nil {
		t.Fatal(err)
	}
	if !m.slots[0].owns(row.StorageID) {
		t.Error("after attribution the row must be owned, or reconciliation cannot check its artifact")
	}
	if n, err := st.CountUnattributedVersions(); err != nil || n != 0 {
		t.Errorf("the sweep must leave nothing unattributed; %d remain (%v)", n, err)
	}
}
