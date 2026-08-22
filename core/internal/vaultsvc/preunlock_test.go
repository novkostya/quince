package vaultsvc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/ios-backup-crypt/fixture"
	"github.com/novkostya/quince/core/internal/wire"
)

// EVERY IDENTIFIER IN THIS FILE IS INVENTED (spec D8/D10). No real device name, serial,
// UDID, IMEI, ICCID, phone number or bundle id appears here or may be added.

const (
	fxDeviceName = "Study Tablet"
	fxIOS        = "17.5.1"
	fxClass      = "iPad"
	fxProduct    = "iPadT9,9"
	fxBuild      = "21F9000"
	fxSerial     = "SERIALINVENTED1"
	fxUDID       = "00009999-000A9999A99A999A"
	fxIMEI       = "990000000000001"
	fxICCID      = "89000000000000000001"
	fxPhone      = "+15550000001"
)

func fxApps() []string {
	return []string{"com.example.notes", "com.example.reader", "com.example.tiles"}
}

// buildBackup writes a fixture backup carrying all three plists.
func buildBackup(t *testing.T, dir string, encrypted bool, withStatus, withInfo bool) {
	t.Helper()
	spec := fixture.Spec{
		Unencrypted:    !encrypted,
		DeviceName:     fxDeviceName,
		ProductVersion: fxIOS,
		DeviceClass:    fxClass,
		ProductType:    fxProduct,
		BuildVersion:   fxBuild,
		SerialNumber:   fxSerial,
		UniqueDeviceID: fxUDID,
		Files: []fixture.File{
			{Domain: "HomeDomain", RelativePath: "Library/note.txt", Data: []byte("x")},
		},
	}
	if withStatus {
		// FIELD BY FIELD, NOT A COMPOSITE LITERAL, and that is not a style choice:
		// fixture/v0.2.0 exports aliases for File, Spec, WrittenFile and Result ONLY,
		// so the types of Spec.Status and Spec.Info cannot be NAMED by a consumer.
		// Assigning through the fields is the one route that needs no name.
		spec.Status.BackupState = "new"
		spec.Status.Date = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
		spec.Status.SnapshotState = "finished"
		spec.Status.UUID = "UUIDINVENTED0001"
		spec.Status.Version = "3.3"
		// DELIBERATELY TRUE while the version registry below says `incremental`. They
		// disagree on purpose: the assertion is that quince does not read this field.
		spec.Status.IsFullBackup = true
	}
	if withInfo {
		// Spec.Info is a POINTER to that same unnameable type, so it cannot even be
		// allocated by name. allocLike recovers the type from the nil pointer through
		// type inference — the one route a consumer has until the fixture module
		// exports the alias (novkostya/ios-backup-crypt#18).
		spec.Info = allocLike(spec.Info)
		spec.Info.DisplayName = fxDeviceName
		spec.Info.GUID = "GUIDINVENTED0001"
		spec.Info.TargetIdentifier = fxUDID
		spec.Info.TargetType = "Device"
		spec.Info.ITunesVersion = "12.12.9"
		spec.Info.LastBackupDate = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
		spec.Info.InstalledApplications = fxApps()
		spec.Info.IMEI = fxIMEI
		spec.Info.ICCID = fxICCID
		spec.Info.PhoneNumber = fxPhone
	}
	if _, err := fixture.Build(dir, spec); err != nil {
		t.Fatalf("fixture.Build: %v", err)
	}
}

func versionAt(dir string, encrypted bool) wire.Version {
	return wire.Version{
		ID: "01V", UDID: fxUDID, BrowseRoot: dir, Encrypted: encrypted,
		CreatedAt: "2026-03-04T05:06:07Z", Kind: "incremental",
	}
}

// G1 — story 1: every D2(a) field is present on a version that was NEVER UNLOCKED, and the
// Status.plist and Info.plist tiers arrive with it.
func TestThePreUnlockTierServesEveryFieldWithoutAnUnlock(t *testing.T) {
	dir := t.TempDir()
	buildBackup(t, dir, true, true, true)
	s := newService(t, versionAt(dir, true), true, &fakeVault{})

	// NO Unlock CALL ANYWHERE IN THIS TEST. That is the assertion, not the setup.
	out, code, msg := s.VersionOverview("01V")
	if code != "" {
		t.Fatalf("VersionOverview: %s — %s", code, msg)
	}

	if !out.Device.Present {
		t.Fatal("device.present = false, want true — Manifest.plist was written")
	}
	for _, c := range []struct{ name, got, want string }{
		{"name", out.Device.Name, fxDeviceName},
		{"ios_version", out.Device.IOSVersion, fxIOS},
		{"class", out.Device.Class, fxClass},
		{"product_type", out.Device.ProductType, fxProduct},
		{"build_version", out.Device.BuildVersion, fxBuild},
		{"serial_number", out.Device.SerialNumber, fxSerial},
		{"unique_device_id", out.Device.UniqueDeviceID, fxUDID},
	} {
		if c.got != c.want {
			t.Errorf("device.%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	if !out.Backup.Present || out.Backup.State != "new" || out.Backup.SnapshotState != "finished" {
		t.Errorf("backup = %+v, want present with state/snapshot_state", out.Backup)
	}
	if out.Backup.FormatVersion != "3.3" {
		t.Errorf("backup.format_version = %q, want the FORMAT version 3.3, not the iOS one",
			out.Backup.FormatVersion)
	}
	if !out.Apps.Present || len(out.Apps.BundleIDs) != len(fxApps()) {
		t.Errorf("apps = %+v, want the %d user-installed bundles", out.Apps, len(fxApps()))
	}
	if out.Apps.Cellular.IMEI != fxIMEI || out.Apps.Cellular.PhoneNumber != fxPhone {
		t.Errorf("cellular = %+v, want the invented phone identifiers", out.Apps.Cellular)
	}
}

// G2 / story 7 — the count is UNKNOWN before an unlock, and never rendered as a number.
//
// This route can never know it: the Files table is inside Manifest.db. The assertion is that
// the field is an explicit null rather than a zero, because 0 is a lie about a good backup.
func TestTheFileCountIsUnknownBeforeUnlockAndNeverZero(t *testing.T) {
	dir := t.TempDir()
	buildBackup(t, dir, true, true, true)
	s := newService(t, versionAt(dir, true), true, &fakeVault{})

	out, code, _ := s.VersionOverview("01V")
	if code != "" {
		t.Fatalf("VersionOverview: %s", code)
	}
	if out.FileCount != nil {
		t.Fatalf("file_count = %d, want nil — a locked version's count is UNKNOWN, and a "+
			"number here says quince counted something it cannot reach", *out.FileCount)
	}
}

// quince#1466 — kind comes from the version registry, NEVER from Status.plist.IsFullBackup.
//
// THE FIXTURE SETS IsFullBackup TRUE AND THE REGISTRY SAYS incremental. They disagree on
// purpose: the lab proved the plist field lies (finding #9(a)), so a reader that took it
// would report "full" here. This is the control that fails if anyone ever wires it up.
func TestKindComesFromTheRegistryAndNotFromTheLyingPlistField(t *testing.T) {
	dir := t.TempDir()
	buildBackup(t, dir, true, true, true)
	s := newService(t, versionAt(dir, true), true, &fakeVault{})

	out, code, _ := s.VersionOverview("01V")
	if code != "" {
		t.Fatalf("VersionOverview: %s", code)
	}
	if out.Kind != "incremental" {
		t.Fatalf("kind = %q, want %q — the registry's seed-sentinel answer. Status.plist "+
			"says IsFullBackup=true in this fixture and the lab proved that field lies.",
			out.Kind, "incremental")
	}
}

// Story 9 — a device whose history holds BOTH an encrypted and an unencrypted version. The
// tier must look the same on both.
//
// THIS IS THE SHAPE THE RUNG GATE RUNS ON: a real tablet's head reads IsEncrypted=false above
// twelve encrypted snapshots (spec D1). It is also the case the library route cannot serve —
// iosbackup.Open refuses an unencrypted backup — which is why quince reads Manifest.plist
// itself. Delete that reader and this test is what fails.
func TestTheTierLooksTheSameOnEncryptedAndUnencryptedVersionsOfOneDevice(t *testing.T) {
	encDir, plainDir := t.TempDir(), t.TempDir()
	buildBackup(t, encDir, true, true, true)
	buildBackup(t, plainDir, false, true, true)

	read := func(dir string, encrypted bool) wire.VersionOverview {
		t.Helper()
		s := newService(t, versionAt(dir, encrypted), true, &fakeVault{})
		out, code, msg := s.VersionOverview("01V")
		if code != "" {
			t.Fatalf("VersionOverview(encrypted=%v): %s — %s", encrypted, code, msg)
		}
		return out
	}

	enc, plain := read(encDir, true), read(plainDir, false)

	if !plain.Device.Present || plain.Device.Name != fxDeviceName {
		t.Fatalf("unencrypted device = %+v, want the same fields as the encrypted one. The "+
			"library refuses to Open an unencrypted backup, so a tier routed through it "+
			"returns nothing here.", plain.Device)
	}
	if enc.Device != plain.Device {
		t.Errorf("device differs across encryption:\n  encrypted   = %+v\n  unencrypted = %+v",
			enc.Device, plain.Device)
	}
	if enc.Apps.Present != plain.Apps.Present || len(enc.Apps.BundleIDs) != len(plain.Apps.BundleIDs) {
		t.Errorf("apps differ across encryption: %+v vs %+v", enc.Apps, plain.Apps)
	}

	// And each reports its OWN encryption state, read from its own manifest — never the
	// device's. A device holds both at once, so neither may describe the other.
	if !enc.Encrypted {
		t.Error("encrypted version reports encrypted=false")
	}
	if plain.Encrypted {
		t.Error("unencrypted version reports encrypted=true")
	}
}

// A backup that carries neither optional plist reports ABSENT, which a surface must render
// differently from "read it, and the fields were empty".
func TestAnAbsentPlistIsReportedAsAbsentRatherThanAsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	buildBackup(t, dir, true, false, false)
	s := newService(t, versionAt(dir, true), true, &fakeVault{})

	out, code, msg := s.VersionOverview("01V")
	if code != "" {
		t.Fatalf("VersionOverview: %s — %s", code, msg)
	}
	if out.Backup.Present {
		t.Error("backup.present = true with no Status.plist written")
	}
	if out.Apps.Present {
		t.Error("apps.present = true with no Info.plist written")
	}
	// Manifest.plist is always written by the fixture, so the device tier is still there —
	// the three sources report presence independently.
	if !out.Device.Present {
		t.Error("device.present = false, but Manifest.plist was written")
	}
	if out.Apps.BundleIDs == nil {
		t.Error("apps.bundle_ids = nil, want [] — a client iterating should not have to " +
			"distinguish none from absent; present already carries that")
	}
}

// A version whose artifact cannot be served is NOT FOUND rather than an empty overview.
func TestThePreUnlockTierIsNotFoundWithNoBrowsableContent(t *testing.T) {
	v := versionAt("", true)
	s := newService(t, v, true, &fakeVault{})

	_, code, msg := s.VersionOverview("01V")
	if code != wire.VaultCodeNotFound {
		t.Fatalf("code = %q (%s), want %q", code, msg, wire.VaultCodeNotFound)
	}
}

// The expensive read is memoised per version (D2c), and the memo returns the same answer.
func TestThePlistReadIsMemoisedPerVersion(t *testing.T) {
	dir := t.TempDir()
	buildBackup(t, dir, true, true, true)
	s := newService(t, versionAt(dir, true), true, &fakeVault{})

	first, code, _ := s.VersionOverview("01V")
	if code != "" {
		t.Fatalf("first: %s", code)
	}
	if _, ok := s.preCache["01V"]; !ok {
		t.Fatal("the version is not in the memo after a read (spec D2c)")
	}

	// Remove the files the tier reads. A second call must still answer, which is only
	// possible from the memo — a re-read would now report the plists absent.
	for _, f := range []string{"Manifest.plist", "Status.plist", "Info.plist"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("removing %s: %v", f, err)
		}
	}
	second, code, _ := s.VersionOverview("01V")
	if code != "" {
		t.Fatalf("second: %s", code)
	}
	if second.Device != first.Device || second.Apps.Present != first.Apps.Present {
		t.Fatalf("memoised answer differs:\n  first  = %+v\n  second = %+v", first, second)
	}
}

// allocLike returns a new zero value of whatever a nil typed pointer points at.
//
// IT EXISTS TO WORK AROUND AN UPSTREAM GAP, not because it is a good idea. `fixture/v0.2.0`
// grew Spec.Status and Spec.Info without exporting aliases for their types, so those two
// fields are unnameable outside the fixture module and Spec.Info — a pointer — cannot be
// allocated at all by ordinary means. Type inference recovers the type from the nil pointer.
//
// DELETE THIS the moment the alias ships (novkostya/ios-backup-crypt#18) and use a plain
// composite literal, which is what every other fixture field in this tree uses.
func allocLike[T any](_ *T) *T {
	var v T
	return &v
}
