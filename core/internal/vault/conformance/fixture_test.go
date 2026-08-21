package conformance_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/ios-backup-crypt/fixture"
	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/vault/conformance"
)

// buildFixture writes a synthetic encrypted backup and describes it as golden facts, so the
// suite asserts against what was ASKED FOR rather than against what the implementation
// happens to return.
//
// Its content is chosen for the cases that matter rather than for volume: a stamped file, an
// unstamped one (LastModified is optional in the format, and absent is the state that gets
// mishandled), a directory with no content, and two domains so the filter has something to
// filter.
func buildFixture(t *testing.T, plain bool) conformance.Fixture {
	t.Helper()
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backup")
	scratch := filepath.Join(dir, "scratch")
	for _, d := range []string{backupDir, scratch} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	stamped := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	content := map[string][]byte{
		"CameraRollDomain|Media/DCIM/IMG_0001.HEIC": []byte("pretend this is a photo"),
		"HomeDomain|Library/Preferences/a.plist":    []byte("alpha"),
		"HomeDomain|Library/Preferences/b.plist":    []byte(""),
	}

	res, err := fixture.Build(backupDir, fixture.Spec{
		Unencrypted:    plain,
		DeviceName:     "conformance-device",
		ProductVersion: "26.0",
		Files: []fixture.File{
			{Domain: "CameraRollDomain", RelativePath: "Media/DCIM/IMG_0001.HEIC", Flags: 1,
				Data: content["CameraRollDomain|Media/DCIM/IMG_0001.HEIC"], MTime: stamped},
			{Domain: "HomeDomain", RelativePath: "Library/Preferences", Flags: 2},
			{Domain: "HomeDomain", RelativePath: "Library/Preferences/a.plist", Flags: 1,
				Data: content["HomeDomain|Library/Preferences/a.plist"]},
			{Domain: "HomeDomain", RelativePath: "Library/Preferences/b.plist", Flags: 1,
				Data: content["HomeDomain|Library/Preferences/b.plist"], MTime: stamped},
		},
	})
	if err != nil {
		t.Fatalf("building the fixture: %v", err)
	}

	// The golden entry list, in the seam's stable (domain, relativePath) order.
	byPath := map[string]string{}
	for _, w := range res.Files {
		byPath[w.Domain+"|"+w.RelativePath] = w.FileID
	}
	entries := []vault.FileEntry{
		{FileID: byPath["CameraRollDomain|Media/DCIM/IMG_0001.HEIC"], Domain: "CameraRollDomain",
			RelativePath: "Media/DCIM/IMG_0001.HEIC", Kind: vault.KindFile, Size: 23, MTime: stamped},
		{FileID: byPath["HomeDomain|Library/Preferences"], Domain: "HomeDomain",
			RelativePath: "Library/Preferences", Kind: vault.KindDir, Size: 0},
		{FileID: byPath["HomeDomain|Library/Preferences/a.plist"], Domain: "HomeDomain",
			RelativePath: "Library/Preferences/a.plist", Kind: vault.KindFile, Size: 5},
		{FileID: byPath["HomeDomain|Library/Preferences/b.plist"], Domain: "HomeDomain",
			RelativePath: "Library/Preferences/b.plist", Kind: vault.KindFile, Size: 0, MTime: stamped},
	}

	fileContent := map[string][]byte{}
	for key, data := range content {
		if len(data) > 0 {
			fileContent[byPath[key]] = data
		}
	}

	return conformance.Fixture{
		Open: func(inner conformance.T) vault.Vault {
			// The SAME golden facts, opened by whichever implementation the version's
			// encryption selects. That is the point of driving both from one fixture: if the
			// two implementations ever disagree about an entry's size, its order or its
			// content, the suite is comparing them against one description rather than
			// against two that were written to match.
			if plain {
				v, err := vault.OpenUnencrypted(backupDir)
				if err != nil {
					inner.Fatalf("OpenUnencrypted: %v", err)
				}
				return v
			}
			v, err := vault.OpenEncrypted(backupDir, scratch)
			if err != nil {
				inner.Fatalf("OpenEncrypted: %v", err)
			}
			return v
		},
		Password:  res.Password,
		Encrypted: !plain,

		FileCount:   int64(len(entries)),
		Entries:     entries,
		FileContent: fileContent,
		ADirectory:  byPath["HomeDomain|Library/Preferences"],
	}
}

// TestInProcessEncryptedConformance is quince#184's gate for the in-process implementation.
func TestInProcessEncryptedConformance(t *testing.T) {
	conformance.Run(t, buildFixture(t, false))
}

// TestTheSuiteRejectsAMutant is the gate's own control: an all-pass from a suite nobody has
// seen fail proves the suite ran, not that the implementation is right.
func TestTheSuiteRejectsAMutant(t *testing.T) {
	conformance.RunMutantMustFail(t, buildFixture(t, false))
}

// TestMutantDetail reports which checks catch which defect — not a gate, a description of
// what the suite is sensitive to. It fails only if a mutation is caught by NOTHING, which
// RunMutantMustFail also catches; the value here is the log.
func TestMutantDetail(t *testing.T) {
	for m, failures := range conformance.RunMutantDetail(buildFixture(t, false)) {
		if len(failures) == 0 {
			t.Errorf("%s was caught by no check", m)
			continue
		}
		t.Logf("%s → caught by %d check(s); first: %s", m, len(failures), failures[0])
	}
}

// TestInProcessUnencryptedConformance is quince#184's gate for the PASSWORDLESS
// implementation (spec D7) — the same suite, the same golden facts, a backup nobody
// encrypted.
//
// IT RUNS THE WHOLE SUITE RATHER THAN A SUBSET, which is the honest reading of canon's
// "any implementation must pass the golden conformance suite before it can ship". An
// unencrypted version is a permitted class of version, so browsing one is not a lesser
// claim than browsing an encrypted one.
func TestInProcessUnencryptedConformance(t *testing.T) {
	conformance.Run(t, buildFixture(t, true))
}

// And its control, for the same reason the encrypted one has one: an all-pass from a suite
// nobody has seen reject anything proves the suite ran, not that the implementation is
// right. Without this, a passwordless implementation that returned plausible nonsense would
// look exactly like one that works.
func TestTheSuiteRejectsAnUnencryptedMutant(t *testing.T) {
	conformance.RunMutantMustFail(t, buildFixture(t, true))
}
