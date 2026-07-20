package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// qn.4b `versions verify` (CLI escape hatch): VerifyVersion re-runs structural verification on a
// committed version's tree (resolved via browseRoot); VerifyLatest does the device's current latest.
// Unknown → ok=false; a version whose on-disk tree is later damaged → ok=true (still known) but
// rep.OK=false with a reason (state honesty: never a false "verified").
func TestVerifyVersionAndLatest(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	commitGoodTree(t, m, testUDID)
	v := m.Versions(testUDID)[0]

	rep, ok := m.VerifyVersion(v.ID)
	if !ok || !rep.OK {
		t.Fatalf("VerifyVersion(good) = ok=%v rep.OK=%v detail=%q", ok, rep.OK, rep.Detail)
	}
	if !rep.Encrypted || rep.Kind != "full" || rep.VersionID != v.ID {
		t.Fatalf("report = %+v, want encrypted/full/%s", rep, v.ID)
	}

	latest, ok := m.VerifyLatest(testUDID)
	if !ok || !latest.OK || latest.VersionID != v.ID {
		t.Fatalf("VerifyLatest = ok=%v rep=%+v, want the latest %s verified", ok, latest, v.ID)
	}

	if _, ok := m.VerifyVersion("no-such-version"); ok {
		t.Fatal("VerifyVersion(unknown) must report ok=false")
	}
	if _, ok := m.VerifyLatest("no-such-udid"); ok {
		t.Fatal("VerifyLatest(no committed version) must report ok=false")
	}

	// Damage the committed tree on disk → verification FAILS honestly, not a false "verified".
	if err := os.Remove(filepath.Join(backups, testUDID, "latest", "Status.plist")); err != nil {
		t.Fatalf("damage latest tree: %v", err)
	}
	rep, ok = m.VerifyVersion(v.ID)
	if !ok {
		t.Fatal("a known-but-torn version must still be found (ok=true)")
	}
	if rep.OK || rep.Detail == "" {
		t.Fatalf("torn version reported OK=%v detail=%q, want failed with a reason", rep.OK, rep.Detail)
	}
}
