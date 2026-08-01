package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleStorageMarker() StorageMarker {
	return StorageMarker{
		StorageID:  "01JQZX000000000000000000",
		Backend:    BackendZFS,
		CreatedAt:  "2026-08-01T00:00:00Z",
		AppVersion: "test-1.2.3",
	}
}

// --- G3: the marker as an artifact ---

func TestStorageMarkerRoundTrips(t *testing.T) {
	root := t.TempDir()
	want := sampleStorageMarker()
	if err := WriteStorageMarker(root, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadStorageMarker(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.StorageID != want.StorageID || got.Backend != want.Backend ||
		got.CreatedAt != want.CreatedAt || got.AppVersion != want.AppVersion {
		t.Errorf("round-trip lost data: got %+v want %+v", got, want)
	}
	if got.Checksum == "" {
		t.Error("a written marker must carry its checksum")
	}
}

// A marker that cannot vouch for itself is not a marker. This is the same contract
// quince-version.json has, and the two must not differ: a reader who learns one will assume the
// other.
func TestStorageMarkerRefusesATamperedFile(t *testing.T) {
	root := t.TempDir()
	if err := WriteStorageMarker(root, sampleStorageMarker()); err != nil {
		t.Fatalf("write: %v", err)
	}
	path := filepath.Join(root, StorageMarkerName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m["backend"] = BackendCopy // edited in place; checksum left alone
	edited, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatalf("write edited: %v", err)
	}
	if _, err := ReadStorageMarker(root); !errors.Is(err, ErrStorageMarkerCorrupt) {
		t.Fatalf("an edited marker must be ErrStorageMarkerCorrupt, got %v", err)
	}
}

// An empty checksum must fail rather than pass vacuously — the hazard of a self-checksummed file
// is that omitting the field is the easiest way to forge one.
func TestStorageMarkerRefusesAnEmptyChecksum(t *testing.T) {
	root := t.TempDir()
	raw := `{"storage_id":"x","backend":"zfs","created_at":"","app_version":"","checksum":""}`
	if err := os.WriteFile(filepath.Join(root, StorageMarkerName), []byte(raw), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadStorageMarker(root); !errors.Is(err, ErrStorageMarkerCorrupt) {
		t.Fatalf("an empty checksum must be corrupt, not valid; got %v", err)
	}
}

func TestStorageMarkerAbsentIsNotExist(t *testing.T) {
	// Deliberately os.ErrNotExist rather than a sentinel: what an absent marker MEANS depends on
	// whether quince has seen this storage before, and that judgement belongs to the caller
	// (PR 3b), not to the reader.
	if _, err := ReadStorageMarker(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an absent marker must be os.ErrNotExist, got %v", err)
	}
}

func TestStorageMarkerWriteReplacesRatherThanTruncates(t *testing.T) {
	root := t.TempDir()
	first := sampleStorageMarker()
	if err := WriteStorageMarker(root, first); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	path := filepath.Join(root, StorageMarkerName)
	link := filepath.Join(root, "hardlink-witness")
	if err := os.Link(path, link); err != nil {
		t.Skipf("filesystem does not support hardlinks: %v", err)
	}
	second := sampleStorageMarker()
	second.Backend = BackendCopy
	if err := WriteStorageMarker(root, second); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	// The witness must still hold the ORIGINAL content: a truncating write would have rewritten
	// the shared inode, which is the hazard WriteMarker's os.Remove exists to avoid.
	raw, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("read witness: %v", err)
	}
	if !strings.Contains(string(raw), `"backend": "`+BackendZFS+`"`) {
		t.Errorf("the second write mutated a shared inode; witness = %s", raw)
	}
}

// The remount guard's comparison half.
func TestStorageMarkerMismatch(t *testing.T) {
	m := sampleStorageMarker() // records zfs
	if bad, _ := m.Mismatch(BackendZFS); bad {
		t.Error("the same backend must not be a mismatch")
	}
	bad, msg := m.Mismatch(BackendCopy)
	if !bad {
		t.Fatal("a different probed backend must be a mismatch")
	}
	for _, want := range []string{BackendZFS, BackendCopy, m.StorageID} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message must name both sides and the storage; missing %q in %q", want, msg)
		}
	}
	// An unknown probe is not a disagreement. "I could not determine the backend" and "the backend
	// changed" have different remedies, and reporting the first as the second is a confident claim
	// built on a failed probe.
	if bad, _ := m.Mismatch(""); bad {
		t.Error("an empty probed backend must not be reported as a mismatch")
	}
}

// --- G3, the invisibility half: the marker must perturb no existing walk ---
//
// quince#378 asked whether quince-storage.json can be added to a root that already holds
// committed versions. The answer is yes on every read path, and this asserts it rather than
// arguing it. Two of the four are observations; Scan and Verify are invisible BY CONSTRUCTION
// (they start a level or more below the storage root), so their assertions are regression guards
// against a future refactor that moves either up a level — which is exactly the kind of change
// this rung is making to the layer above them.

func TestStorageMarkerIsInvisibleToReconcileAndJournalScan(t *testing.T) {
	root := t.TempDir()
	udid := "00008140-000A1B2C3D4E5F60"
	if err := os.MkdirAll(filepath.Join(root, udid, "latest"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	before, errBefore := scanJournals(root)
	if errBefore != nil {
		t.Fatalf("scanJournals before: %v", errBefore)
	}
	udidsBefore := udidDirs(root)

	if err := WriteStorageMarker(root, sampleStorageMarker()); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	afterJournals, errAfter := scanJournals(root)
	if errAfter != nil {
		t.Fatalf("scanJournals after: %v", errAfter)
	}
	if got := len(afterJournals); got != len(before) {
		t.Errorf("scanJournals saw the marker: %d entries before, %d after", len(before), got)
	}
	after := udidDirs(root)
	if len(after) != len(udidsBefore) || len(after) != 1 || after[0] != udid {
		t.Errorf("the device walk saw the marker: before=%v after=%v", udidsBefore, after)
	}
}

// udidDirs mirrors Manager.reconcileUDIDs' own filter (IsDir + validUDID) so the assertion is
// about the same predicate the production walk applies.
func udidDirs(root string) []string {
	var out []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() && validUDID(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestStorageMarkerIsBelowNeitherScanNorVerify(t *testing.T) {
	// Structural, not behavioural: Scan reads latest/ and versions/<ts>/ under a DEVICE dir, and
	// Verify runs against a backup TREE. Both start at least one level below where the marker
	// lives, so neither can see it. Asserting the shape keeps that true if the layout moves.
	root := "/backups"
	udid := "00008140-000A1B2C3D4E5F60"
	marker := filepath.Join(root, StorageMarkerName)
	for name, dir := range map[string]string{
		"latest (Scan)":   latestDir(root, udid),
		"versions (Scan)": nsVersions(root, udid),
		"device dir":      deviceDir(root, udid),
	} {
		if !strings.HasPrefix(dir, root+"/") || dir == root {
			t.Fatalf("%s is not under the storage root: %q", name, dir)
		}
		if filepath.Dir(marker) == dir {
			t.Errorf("%s sits at the marker's own level (%q) — Scan/Verify could now see it", name, dir)
		}
	}
}

// --- G4: the offsite exclude ---

func TestStorageMarkerIsExcludedFromOffsite(t *testing.T) {
	const subdir = "iphone-backup"
	rules := AnchoredFilterRules(subdir)
	if !PathExcluded(subdir+"/"+StorageMarkerName, rules) {
		t.Fatalf("the storage marker must be excluded from offsite sync; rules = %v", rules)
	}
}

// The D5a anchoring hazard, re-proven rather than trusted: the new rule must not become an
// unanchored name match that also drops a same-named file inside backup CONTENT under latest/.
func TestOffsiteRulesStayAnchoredAfterTheMarkerRule(t *testing.T) {
	const subdir = "iphone-backup"
	udid := "00008140-000A1B2C3D4E5F60"
	rules := AnchoredFilterRules(subdir)

	// A file that merely shares the marker's NAME, inside committed content, must still sync.
	inContent := subdir + "/" + udid + "/latest/" + StorageMarkerName
	if PathExcluded(inContent, rules) {
		t.Errorf("the marker rule is matching inside backup content: %q", inContent)
	}

	// And the two pre-existing rules must still behave.
	if !PathExcluded(subdir+"/"+udid+"/working/"+udid+"/Manifest.db", rules) {
		t.Error("working/ must stay excluded")
	}
	if !PathExcluded(subdir+"/"+udid+"/versions/2026-08-01T00-00-00Z/Status.plist", rules) {
		t.Error("versions/ must stay excluded")
	}
	if PathExcluded(subdir+"/"+udid+"/latest/Manifest.db", rules) {
		t.Error("latest/ is the sole synced payload and must not be excluded")
	}
}
