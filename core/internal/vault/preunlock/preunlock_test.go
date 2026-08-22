package preunlock

import (
	"os"
	"path/filepath"
	"testing"
)

// EVERY IDENTIFIER IN THIS FILE IS INVENTED (spec D8/D10).

// A MISSING plist reports its own absence. A plist that EXISTS AND WILL NOT PARSE is an
// error — that is a broken backup rather than an old one, and the two have different
// remedies, so collapsing them is the defect this rung is named after.
//
// These live here rather than in vaultsvc because this is where the split is decided, and
// no vaultsvc fixture writes a corrupt plist.

func TestAnAbsentManifestReportsAbsenceRatherThanFailing(t *testing.T) {
	dir := t.TempDir() // nothing in it at all

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read on an empty dir = %v, want no error — an absent plist is a state, not a fault", err)
	}
	if got.ManifestPresent {
		t.Error("manifest_present = true with no Manifest.plist on disk")
	}
	if got.Status.Present || got.Extras.Present {
		t.Errorf("status/info report present with nothing on disk: %+v %+v", got.Status, got.Extras)
	}
}

// THE CONTROL FOR THE THREE BELOW. Without it, a Read that failed for an unrelated reason
// would make every "returns an error" assertion pass for the wrong reason.
func TestAReadableManifestIsReadRatherThanRefused(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, true)

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read on a valid manifest = %v, want no error", err)
	}
	if !got.ManifestPresent || got.Lockdown.DeviceName != "Study Tablet" {
		t.Fatalf("lockdown = %+v, want the written fields", got.Lockdown)
	}
}

func TestACorruptManifestIsAnErrorAndNotReportedAsAbsent(t *testing.T) {
	dir := t.TempDir()
	// Present, non-empty, and not a plist.
	if err := os.WriteFile(filepath.Join(dir, "Manifest.plist"),
		[]byte("this is not a plist"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	got, err := Read(dir)
	if err == nil {
		t.Fatalf("Read = %+v with no error, want an error. A Manifest.plist that will not "+
			"parse is a BROKEN backup; reporting present=false says the backup simply has "+
			"none, which is a false statement about the user's data.", got)
	}
	if got.ManifestPresent {
		t.Error("a failed read still reported manifest_present = true")
	}
}

func TestACorruptStatusPlistIsAnErrorAndNotReportedAsAbsent(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, true)
	if err := os.WriteFile(filepath.Join(dir, "Status.plist"),
		[]byte("not a plist either"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	if _, err := Read(dir); err == nil {
		t.Fatal("a present, unparseable Status.plist read as absent rather than as an error")
	}
}

func TestACorruptInfoPlistIsAnErrorAndNotReportedAsAbsent(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, true)
	if err := os.WriteFile(filepath.Join(dir, "Info.plist"),
		[]byte("still not a plist"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	if _, err := Read(dir); err == nil {
		t.Fatal("a present, unparseable Info.plist read as absent rather than as an error")
	}
}

// Encrypted is read from THIS version's manifest. The value is the file's, whatever any
// caller believes about the device.
func TestEncryptedIsReadFromTheManifestItself(t *testing.T) {
	for _, enc := range []bool{true, false} {
		dir := t.TempDir()
		writeManifest(t, dir, enc)

		got, err := Read(dir)
		if err != nil {
			t.Fatalf("Read(encrypted=%v): %v", enc, err)
		}
		if got.Encrypted != enc {
			t.Errorf("encrypted = %v, want %v — read off this version's own manifest",
				got.Encrypted, enc)
		}
	}
}

// writeManifest writes a minimal XML Manifest.plist. XML rather than binary because the
// reader takes either and this keeps the fixture readable in the diff.
func writeManifest(t *testing.T, dir string, encrypted bool) {
	t.Helper()
	enc := "<false/>"
	if encrypted {
		enc = "<true/>"
	}
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>IsEncrypted</key>` + enc + `
  <key>Lockdown</key><dict>
    <key>DeviceName</key><string>Study Tablet</string>
    <key>ProductVersion</key><string>17.5.1</string>
    <key>DeviceClass</key><string>iPad</string>
    <key>ProductType</key><string>iPadT9,9</string>
    <key>BuildVersion</key><string>21F9000</string>
    <key>SerialNumber</key><string>SERIALINVENTED1</string>
    <key>UniqueDeviceID</key><string>00009999-000A9999A99A999A</string>
  </dict>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(dir, "Manifest.plist"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing Manifest.plist: %v", err)
	}
}
