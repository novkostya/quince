package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }

func unknownLookup(string) (knownStorage, error) { return knownStorage{}, nil }

func knownLookup(id string) StorageLookup {
	return func(string) (knownStorage, error) { return knownStorage{Known: true, StorageID: id}, nil }
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
