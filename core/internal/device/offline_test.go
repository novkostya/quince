package device

import (
	"testing"

	"github.com/novkostya/quince/core/internal/muxd"
	"github.com/novkostya/quince/core/internal/wire"
)

// qn.6a offline devices: a UDID with committed versions but no live transport is still listed (as an
// offline shell), carrying its persisted name + last_seen and NO transports — so the card renders a
// disabled-with-reason "Back up now" instead of the device vanishing.
func TestOfflineDeviceUnionedIntoList(t *testing.T) {
	r, _ := newTestRegistry(t)
	r.SetKnownUDIDs(func() []string { return []string{udidB} }) // udidB has backups but is offline
	r.LoadPersisted([]PersistedIdentity{{
		UDID:     udidB,
		LastSeen: "2026-07-20T10:00:00Z",
		Identity: Identity{Name: "old-iphone", Model: "iPhone14,2", Paired: "yes", BackupEncryption: "on"},
	}})

	// udidA is live over USB; udidB is only known via its versions.
	r.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))

	devs := r.Devices()
	byUDID := map[string]wire.Device{}
	for _, d := range devs {
		byUDID[d.UDID] = d
	}
	if len(devs) != 2 {
		t.Fatalf("Devices() = %d, want 2 (one live + one offline)", len(devs))
	}
	live, ok := byUDID[udidA]
	if !ok || live.Transports.USB == nil {
		t.Fatalf("live device udidA missing its USB transport: %+v", live)
	}
	off, ok := byUDID[udidB]
	if !ok {
		t.Fatal("offline device udidB not listed")
	}
	if off.Transports.USB != nil || off.Transports.WiFi != nil {
		t.Fatalf("offline device must have NO transports, got %+v", off.Transports)
	}
	if off.Name != "old-iphone" || off.LastSeen != "2026-07-20T10:00:00Z" {
		t.Fatalf("offline device lost its persisted identity/last_seen: %+v", off)
	}
	// Device(udid) must also resolve the offline device (deep-link to its details page).
	if d, ok := r.Device(udidB); !ok || d.Name != "old-iphone" {
		t.Fatalf("Device(offline) = %+v ok=%v", d, ok)
	}
}

// With no knownUDIDs source wired (tests/--demo), Devices() is live-only — exactly the pre-qn.6a
// behaviour, so nothing that doesn't opt in is affected.
func TestNoOfflineWithoutKnownSource(t *testing.T) {
	r, _ := newTestRegistry(t)
	r.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	if got := len(r.Devices()); got != 1 {
		t.Fatalf("Devices() = %d, want 1 (live only)", got)
	}
	if _, ok := r.Device(udidB); ok {
		t.Fatal("Device(unknown) must be false when there are no versions and no presence")
	}
}

// When the last transport of a device that HAS backups detaches, the registry keeps it on screen by
// emitting device.updated with the offline shell right after device.detached — so unplugging a phone
// mid-session turns its card offline instead of making it vanish (qn.6a).
func TestFullDetachOfBackedUpDeviceGoesOffline(t *testing.T) {
	r, sub := newTestRegistry(t)
	r.SetKnownUDIDs(func() []string { return []string{udidA} }) // udidA has backups
	r.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	drain(sub) // clear the attach event

	r.Sink(srcUSB).Apply(detach(udidA, muxd.TransportUSB))
	evs := drain(sub)
	types := typesOf(evs)
	// Expect a detached (removes the live row) followed by an updated (re-adds the offline shell).
	if len(types) != 2 || types[0] != wire.EventDeviceDetached || types[1] != wire.EventDeviceUpdated {
		t.Fatalf("events = %v, want [device.detached device.updated]", types)
	}
	updated, ok := evs[1].Data.(wire.Device)
	if !ok || updated.UDID != udidA {
		t.Fatalf("offline update payload = %+v", evs[1].Data)
	}
	if updated.Transports.USB != nil || updated.LastSeen == "" {
		t.Fatalf("offline shell must have no transports and a last_seen: %+v", updated)
	}
	// The device is still in the list, now offline.
	if got := len(r.Devices()); got != 1 {
		t.Fatalf("Devices() after detach = %d, want 1 (offline)", got)
	}
}

// A device with NO backups still just leaves the table on full detach (no offline shell) — we only
// remember devices that have something to show.
func TestFullDetachOfUnbackedDeviceVanishes(t *testing.T) {
	r, sub := newTestRegistry(t)
	r.SetKnownUDIDs(func() []string { return nil }) // nothing has backups
	r.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	drain(sub)

	r.Sink(srcUSB).Apply(detach(udidA, muxd.TransportUSB))
	if types := typesOf(drain(sub)); len(types) != 1 || types[0] != wire.EventDeviceDetached {
		t.Fatalf("events = %v, want [device.detached] only", types)
	}
	if got := len(r.Devices()); got != 0 {
		t.Fatalf("Devices() = %d, want 0 (device forgotten)", got)
	}
}

// Enrich persists identity + last_seen via the wired hook, and only advances last_seen while the
// device is present (an offline enrich must not regress it).
func TestEnrichPersistsIdentityAndLastSeen(t *testing.T) {
	r, _ := newTestRegistry(t)
	var got []struct {
		udid, lastSeen string
		id             Identity
	}
	r.SetPersist(func(udid string, id Identity, lastSeen string) {
		got = append(got, struct {
			udid, lastSeen string
			id             Identity
		}{udid, lastSeen, id})
	})

	// Present → last_seen advances to the live edge.
	r.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	r.Enrich(udidA, Identity{Name: "n", Paired: "yes"})
	if len(got) == 0 {
		t.Fatal("Enrich did not persist while present")
	}
	last := got[len(got)-1]
	if last.udid != udidA || last.id.Name != "n" || last.lastSeen == "" {
		t.Fatalf("persisted %+v, want udidA/name=n/non-empty last_seen", last)
	}
}
