package demo

import (
	"io"
	"log/slog"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
)

// seededProvider builds the static fixture world without starting the ambient script, so these
// assertions describe the SEED rather than whatever the demo has drifted to while running.
func seededProvider() *Provider {
	return NewProvider(bus.New(), slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
}

// THE COUNTS MUST BE A FOLD OVER THE VERSIONS, not literals beside them (quince#624).
//
// The demo used to assert `BackupCount: 14, DeviceCount: 2` for `internal` while NO version carried
// a storage_id at all. So a storage page computed "0 backups here" for every device, under a header
// claiming 14, next to device cards claiming something else again — three surfaces, three answers,
// one fixture set, and nothing that could notice.
//
// These tests are what makes that unrepeatable. They do not check the NUMBERS 14 and 3 for their own
// sake — they check that whatever the numbers are, they equal what is actually in the version list.
// A future fixture edit changes both sides together or fails here.
func TestStorageCountsAreDerivedFromTheVersionList(t *testing.T) {
	p := seededProvider()

	// Count independently of the tally the provider uses, so this is a second opinion rather than
	// the same fold asserted against itself.
	wantBackups := map[string]int{}
	wantDevices := map[string]map[string]bool{}
	for _, id := range p.verOrder {
		v := p.versions[id]
		if v.Missing || v.StorageID == nil || *v.StorageID == "" {
			continue
		}
		wantBackups[*v.StorageID]++
		if wantDevices[*v.StorageID] == nil {
			wantDevices[*v.StorageID] = map[string]bool{}
		}
		wantDevices[*v.StorageID][v.UDID] = true
	}

	for _, s := range p.Storages("") {
		if s.BackupCount != wantBackups[s.ID] {
			t.Errorf("storage %q reports backup_count=%d, but %d versions name it — the count is "+
				"asserted rather than derived", s.Name, s.BackupCount, wantBackups[s.ID])
		}
		if s.DeviceCount != len(wantDevices[s.ID]) {
			t.Errorf("storage %q reports device_count=%d, but %d distinct devices have versions "+
				"there", s.Name, s.DeviceCount, len(wantDevices[s.ID]))
		}
	}
}

// The seeded world, stated once so a fixture change that moves it is visible in a diff rather than
// discovered on a screen. `internal` = 14 backups for ONE device; `shuttle` = 3 for one.
//
// ONE DEVICE, not two: studio-ipad has never been backed up and stays that way (Operator ruling,
// 2026-08-04). The old `device_count: 2` was false about a fixture in which exactly one device had
// ever been backed up anywhere — inventing a history for the pad to make the literal true would
// have cost the demo its empty-card state to satisfy a number nobody measured.
func TestSeededStorageCounts(t *testing.T) {
	p := seededProvider()
	byName := map[string]struct{ backups, devices int }{}
	for _, s := range p.Storages("") {
		byName[s.Name] = struct{ backups, devices int }{s.BackupCount, s.DeviceCount}
	}

	if got := byName["internal"]; got.backups != 14 || got.devices != 1 {
		t.Errorf("internal = %d backups / %d devices, want 14 / 1", got.backups, got.devices)
	}
	if got := byName["shuttle"]; got.backups != 3 || got.devices != 1 {
		t.Errorf("shuttle = %d backups / %d devices, want 3 / 1", got.backups, got.devices)
	}
}

// `will_be_full` IS PER (STORAGE, DEVICE) — the question the field actually asks.
//
// It used to be a hardcoded `true` for the shuttle and, for internal, "does this device have ANY
// version anywhere". With one storage those are indistinguishable; with two, the second is simply a
// different question, and it answered "not a full transfer" about a storage the device had never
// written a byte to.
func TestWillBeFullIsPerStorageNotPerDevice(t *testing.T) {
	p := seededProvider()

	// The phone has history on BOTH storages in the seeded world, so neither is a full transfer.
	for _, s := range p.Storages(udidPhone) {
		if s.WillBeFull == nil {
			t.Fatalf("storage %q omitted will_be_full for a named device", s.Name)
		}
		if *s.WillBeFull {
			t.Errorf("storage %q claims a full transfer for the phone, which has backups there",
				s.Name)
		}
	}

	// The pad has never been backed up, so EVERY storage is a full transfer for it. This is the
	// case the old device-wide test could get right by accident and the per-storage one gets right
	// for the reason.
	for _, s := range p.Storages(udidPad) {
		if s.WillBeFull == nil || !*s.WillBeFull {
			t.Errorf("storage %q does not claim a full transfer for a device that has never been "+
				"backed up anywhere", s.Name)
		}
	}
}

// The list is device-INDEPENDENT unless a device is named — the ruled shape of the endpoint, and
// the reason `will_be_full` is a pointer rather than a bool.
func TestWillBeFullIsAbsentWithoutADevice(t *testing.T) {
	for _, s := range seededProvider().Storages("") {
		if s.WillBeFull != nil {
			t.Errorf("storage %q carries will_be_full with no device asked about", s.Name)
		}
	}
}

// A MISSING version must not be counted. Its registry row survives but the artifact is gone, so
// counting it claims a backup the user cannot restore — and would make `will_be_full` answer "not a
// full transfer" about a storage holding nothing usable for that device.
func TestMissingVersionsAreNotCounted(t *testing.T) {
	p := seededProvider()
	before := storageByName(t, p, "internal").BackupCount

	v := p.versions[verHL]
	v.Missing = true
	p.versions[verHL] = v

	if after := storageByName(t, p, "internal").BackupCount; after != before-1 {
		t.Errorf("marking a version missing moved backup_count %d → %d, want %d — a gone artifact "+
			"is still being counted as a backup", before, after, before-1)
	}
}

// An UNATTRIBUTED version (null storage_id) must not be folded into any storage. It is a real state
// — the migration that added the column left older rows null rather than guessing — so a demo that
// silently assigned them would model the one behaviour the live resolver refuses.
func TestUnattributedVersionsAreNotCounted(t *testing.T) {
	p := seededProvider()
	total := func() int {
		n := 0
		for _, s := range p.Storages("") {
			n += s.BackupCount
		}
		return n
	}
	before := total()

	v := p.versions[verZFS]
	v.StorageID = nil
	p.versions[verZFS] = v

	if after := total(); after != before-1 {
		t.Errorf("unattributing a version moved the total %d → %d, want %d — a version on no "+
			"storage is being attributed to one", before, after, before-1)
	}
}

func storageByName(t *testing.T, p *Provider, name string) (out struct {
	BackupCount, DeviceCount int
}) {
	t.Helper()
	for _, s := range p.Storages("") {
		if s.Name == name {
			out.BackupCount, out.DeviceCount = s.BackupCount, s.DeviceCount
			return out
		}
	}
	t.Fatalf("no storage named %q", name)
	return out
}
