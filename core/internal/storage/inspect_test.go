package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// qn.6e gates G1–G4. The rung's central claim is that Inspect REPORTS without CHANGING, so G1 and
// G2 assert on the FILESYSTEM rather than on the returned Report: the way this can be wrong is
// that the disk moved while the answer looked right.

// G1 — an absent path is refused AND IS STILL ABSENT AFTERWARDS.
//
// This is the anti-MkdirAll gate. Select's probeNamespace opens with os.MkdirAll, so the failure
// this catches is not hypothetical: it is what happens if anyone ever routes the form through
// Select, and it presents as a typo'd path reported healthy rather than as an error.
func TestInspectMissingPathDoesNotCreateIt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "dir")

	r := Inspect(missing, InspectOptions{})

	if r.Outcome != InspectMissing {
		t.Fatalf("outcome = %q, want %q", r.Outcome, InspectMissing)
	}
	if r.Outcome.OK() {
		t.Fatalf("a missing path must not be OK()")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("INSPECT CREATED THE PATH IT WAS ASKED ABOUT: stat(%s) err = %v", missing, err)
	}
	// The parent chain must not have been created either.
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatalf("inspect created a parent of %s", missing)
	}
	if r.Backend != "" {
		t.Fatalf("a refusal must not recommend a backend, got %q", r.Backend)
	}
	if !strings.Contains(r.Reason, missing) {
		t.Fatalf("reason %q does not name the path it is about (quince#514)", r.Reason)
	}
}

// G1b — a path that exists but is not a directory, and a relative path. Separate outcomes, because
// "unusable" for all three would tell a user nothing they could not already see.
func TestInspectRefusalsAreDistinguishable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := Inspect(file, InspectOptions{}).Outcome; got != InspectNotDir {
		t.Fatalf("a regular file → %q, want %q", got, InspectNotDir)
	}
	if got := Inspect("relative/path", InspectOptions{}).Outcome; got != InspectInvalidPath {
		t.Fatalf("a relative path → %q, want %q", got, InspectInvalidPath)
	}
	if got := Inspect("", InspectOptions{}).Outcome; got != InspectInvalidPath {
		t.Fatalf("an empty path → %q, want %q", got, InspectInvalidPath)
	}
}

// G2 — an existing writable directory gets a recommendation whose reason names it, and the
// directory is left exactly as it was found: no .quince-* residue from the probes.
func TestInspectNewLeavesNoResidue(t *testing.T) {
	dir := t.TempDir()
	before := countEntries(t, dir)

	r := Inspect(dir, InspectOptions{FSType: fsType(0xdeadbeef), KernelHasZFS: noZFSModule})

	if r.Outcome != InspectNew {
		t.Fatalf("outcome = %q, want %q (reason %q)", r.Outcome, InspectNew, r.Reason)
	}
	switch r.Backend {
	case BackendReflink, BackendHardlink, BackendCopy:
	default:
		t.Fatalf("backend = %q, want a namespace backend", r.Backend)
	}
	if !strings.Contains(r.BackendReason, dir) {
		t.Fatalf("backend reason %q does not name the path it probed (quince#514)", r.BackendReason)
	}
	if got := countEntries(t, dir); got != before {
		t.Fatalf("inspect left residue in %s: %d entries before, %d after", dir, before, got)
	}
	if r.NonEmpty {
		t.Fatalf("an empty dir reported NonEmpty")
	}
	if r.TotalBytes == 0 {
		t.Fatalf("TotalBytes = 0; statfs should have answered for %s", dir)
	}
}

// G3 — a marker present is an ADOPT, carrying the marker's backend EVEN WHEN A LIVE PROBE WOULD
// DISAGREE, and a corrupt marker is its own outcome rather than "no marker here".
//
// The disagreement half is the point. A storage's backend is recorded at its creation moment and
// is immutable; a later probe that disagrees is a remount, not a re-selection. So the form must
// have nothing to offer here, and the way to prove that is to record a backend the probe cannot
// possibly return for a temp dir.
func TestInspectAdoptBeatsTheProbe(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStorageMarker(dir, StorageMarker{
		StorageID: "stor-1", Backend: BackendZFS, CreatedAt: "2026-08-07T00:00:00Z", AppVersion: "test",
	}); err != nil {
		t.Fatal(err)
	}

	// FSType answers "not zfs" deliberately: if adopt were implemented by re-probing, this dir
	// would come back reflink/hardlink/copy and the assertion below would catch it.
	r := Inspect(dir, InspectOptions{FSType: fsType(0xdeadbeef), KernelHasZFS: noZFSModule})

	if r.Outcome != InspectAdopt {
		t.Fatalf("outcome = %q, want %q", r.Outcome, InspectAdopt)
	}
	if r.Backend != BackendZFS {
		t.Fatalf("backend = %q, want the MARKER's %q — a probe must not re-select it",
			r.Backend, BackendZFS)
	}
	if r.Marker == nil || r.Marker.StorageID != "stor-1" {
		t.Fatalf("marker not reported: %+v", r.Marker)
	}
	if r.NonEmpty {
		t.Fatalf("a dir holding only the marker must not read as NonEmpty")
	}
}

func TestInspectCorruptMarkerIsItsOwnOutcome(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StorageMarkerName),
		[]byte(`{"storage_id":"s","backend":"zfs","checksum":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Inspect(dir, InspectOptions{FSType: fsType(0xdeadbeef), KernelHasZFS: noZFSModule})

	if r.Outcome != InspectCorruptMarker {
		t.Fatalf("outcome = %q, want %q — a corrupt marker must never read as a fresh path, or "+
			"quince mints a SECOND identity for one disk", r.Outcome, InspectCorruptMarker)
	}
	if r.Backend != "" {
		t.Fatalf("a corrupt marker must not produce a recommendation, got %q", r.Backend)
	}
}

// G4 — the statfs tier: a ZFS f_type yields a zfs recommendation WITH NO zfs BINARY PRESENT.
//
// The syscall is injected because no CI box can stage a ZFS filesystem, and the alternative —
// asserting this only where ZFS happens to exist — is a gate that silently does not run. The real
// reading is owed as a host measurement against the built image.
func TestInspectZFSTiers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fsType     int64
		kernelZFS  bool
		wantSignal ZFSSignal
		wantBacknd string
	}{
		{"path is zfs", zfsSuperMagic, false, ZFSPath, BackendZFS},
		{"path is zfs, kernel too", zfsSuperMagic, true, ZFSPath, BackendZFS},
		{"host has zfs, path does not", 0x9123683e, true, ZFSHost, ""},
		{"no signal at all", 0x9123683e, false, ZFSNone, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			r := Inspect(dir, InspectOptions{
				FSType:       fsType(tc.fsType),
				KernelHasZFS: func() bool { return tc.kernelZFS },
			})
			if r.ZFS != tc.wantSignal {
				t.Fatalf("signal = %q, want %q", r.ZFS, tc.wantSignal)
			}
			if tc.wantBacknd != "" && r.Backend != tc.wantBacknd {
				t.Fatalf("backend = %q, want %q", r.Backend, tc.wantBacknd)
			}
			if tc.wantBacknd == "" && r.Backend == BackendZFS {
				t.Fatalf("recommended zfs from a non-zfs path (signal %q)", r.ZFS)
			}
		})
	}
}

// G4b — ZFSNone is a MISSING SIGNAL, not a capability claim. Nothing in this package may render or
// return a sentence asserting that zfs is unavailable, because in hook mode the container has no
// zfs userland at all and zfs works perfectly. Asserted on the reason strings, which are the only
// place a sentence could leak out of here.
func TestInspectNeverClaimsZFSUnsupported(t *testing.T) {
	dir := t.TempDir()
	r := Inspect(dir, InspectOptions{FSType: fsType(0x9123683e), KernelHasZFS: noZFSModule})

	if r.ZFS != ZFSNone {
		t.Fatalf("signal = %q, want %q", r.ZFS, ZFSNone)
	}
	// THE ASSERTION IS "SAY NOTHING ABOUT ZFS", not "avoid a list of bad phrasings", and the
	// difference is the whole gate. Tier 3 is silence: quince has no signal, so any sentence it
	// emits about zfs here is a claim it cannot support, whatever words it picks. Scanning for the
	// word itself catches every phrasing, including ones nobody thought of.
	//
	// A PHRASE BLOCKLIST WAS TRIED FIRST AND WAS WRONG IN BOTH DIRECTIONS, which is why this note
	// is longer than the check:
	//
	//   - it fired on this test's OWN NAME, because t.TempDir() embeds it and every reason names
	//     its path (quince#514). A user may equally have a directory called /mnt/zfs-unsupported,
	//     so the path has to come out before any scan — quince's prose is the subject, not the
	//     user's typing.
	//   - it then fired on "reflink unsupported; link()+inode-identity probe passed on …", which
	//     is probeNamespace's real, accurate sentence about a DIFFERENT capability. Banning the
	//     word "unsupported" outright would have made a true statement unsayable.
	prose := strings.ToLower(strings.ReplaceAll(r.Reason, r.CleanPath, "") +
		" " + strings.ReplaceAll(r.BackendReason, r.CleanPath, ""))
	if strings.Contains(prose, "zfs") {
		t.Fatalf("tier 3 mentioned zfs at all; it has no signal, so silence is the only honest "+
			"answer. reason=%q backend_reason=%q", r.Reason, r.BackendReason)
	}
}

// The predicate is unchanged by being factored — zfs stays INTENT, never something Inspect's new
// tier 1 can confer. A path that IS zfs with no parent dataset configured still is not a zfs
// storage, because the backend would have nowhere to create its per-device datasets.
func TestWantZFSIsIntentOnly(t *testing.T) {
	for _, tc := range []struct {
		backend, parent, hook string
		want                  bool
	}{
		{BackendZFS, "", "", true},
		{"auto", "tank/x", "", true},
		{"auto", "", "ssh host", true},
		{"auto", "", "", false},
		{BackendReflink, "tank/x", "", false},
		{BackendCopy, "", "", false},
	} {
		if got := WantZFS(tc.backend, tc.parent, tc.hook); got != tc.want {
			t.Fatalf("WantZFS(%q,%q,%q) = %v, want %v", tc.backend, tc.parent, tc.hook, got, tc.want)
		}
	}
}

func TestInspectReportsNonEmptyWithoutRefusing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "someone-elses-data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Inspect(dir, InspectOptions{FSType: fsType(0xdeadbeef), KernelHasZFS: noZFSModule})

	// Reported and ALLOWED: a path holding real backups from before storage markers existed has no
	// marker and is not empty, and it is exactly the path an upgrading operator types.
	if r.Outcome != InspectNew {
		t.Fatalf("outcome = %q, want %q — a non-empty path is reported, not refused", r.Outcome, InspectNew)
	}
	if !r.NonEmpty {
		t.Fatalf("NonEmpty = false on a dir holding data")
	}
}

// --- helpers ---

func fsType(t int64) func(string) (int64, error) {
	return func(string) (int64, error) { return t, nil }
}

func noZFSModule() bool { return false }

func countEntries(t *testing.T, dir string) int {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}
