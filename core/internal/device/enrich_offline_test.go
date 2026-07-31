package device

import (
	"testing"

	"github.com/novkostya/quince/core/internal/muxd"
	"github.com/novkostya/quince/core/internal/wire"
)

// Turning Wi-Fi sync OFF over Wi-Fi severs the transport it runs on: the device stops announcing
// over mDNS the moment the flag flips, so it is GONE by the time the op publishes the new value.
// Enrich must still tell the UI, because the device is still on screen — as an offline shell, since
// it has committed versions (qn.6a).
//
// This is the hardware sequence, in order:
//
//	wifi attach → enrich(wifi_sync on) → SetWifiSync succeeds → wifi detach → enrich(wifi_sync off)
//
// Without the fix the last step changes the stored identity and persists it, but publishes NOTHING,
// so the UI's last word is the offline shell from the detach — which still carries `on`. The badge
// reads "Wi-Fi sync: on" for a device that is off and gone, and only a page reload corrects it.
// That is exactly what the Operator reported and screenshotted (quince#325 (2a)/(2b)).
func TestEnrichPublishesForAnOfflineDeviceThatIsStillListed(t *testing.T) {
	r, sub := newTestRegistry(t)
	r.SetKnownUDIDs(func() []string { return []string{udidA} }) // udidA has backups → stays listed

	base := Identity{Name: "n", Model: "iPhone14,2", Paired: "yes", BackupEncryption: "on"}

	// Live over Wi-Fi only, with the flag on — the state a disable starts from.
	r.Sink(srcWiFi).Apply(attach(udidA, muxd.TransportWiFi))
	on := base
	on.WifiSync = "on"
	r.Enrich(udidA, on)
	drain(sub)

	// The flag flips, the device stops announcing, the transport drops.
	r.Sink(srcWiFi).Apply(detach(udidA, muxd.TransportWiFi))
	drain(sub) // detached + updated(offline shell, still `on`) — correct, and not what this tests

	// What runWifiSync does next: publish the value SetWifiSync already read back.
	off := base
	off.WifiSync = "off"
	r.Enrich(udidA, off)

	evs := drain(sub)
	if len(evs) != 1 || evs[0].Type != wire.EventDeviceUpdated {
		t.Fatalf("events = %v, want exactly [device.updated] — a change to a LISTED device must reach the UI, "+
			"and presence is not what decides whether it is listed", typesOf(evs))
	}
	dev, ok := evs[0].Data.(wire.Device)
	if !ok {
		t.Fatalf("payload = %+v, want wire.Device", evs[0].Data)
	}
	if dev.WifiSync != "off" {
		t.Fatalf("published wifi_sync = %q, want %q — the UI would keep showing the old value", dev.WifiSync, "off")
	}
	// The rest of the identity must survive: Enrich replaces the whole thing, so a shell that
	// dropped the name would render an offline card with no name on it.
	if dev.Name != "n" || dev.Paired != "yes" || dev.BackupEncryption != "on" {
		t.Fatalf("offline update lost identity fields: %+v", dev)
	}
	if dev.Transports.USB != nil || dev.Transports.WiFi != nil {
		t.Fatalf("device is gone — the payload must carry no transports: %+v", dev.Transports)
	}
}

// The counterweight: a device with NO versions and no presence is not on any screen, so enriching it
// must stay silent. Without this, the fix above would publish updates for devices the UI has never
// heard of and cannot render — inventing rows instead of correcting one.
func TestEnrichStaysSilentForADeviceNothingCanRender(t *testing.T) {
	r, sub := newTestRegistry(t)
	// No SetKnownUDIDs → no versions → udidA is neither present nor listed.
	r.Enrich(udidA, Identity{Name: "n", Paired: "yes", BackupEncryption: "on", WifiSync: "off"})
	if evs := drain(sub); len(evs) != 0 {
		t.Fatalf("events = %v, want none — nothing renders this device, so an update is a phantom row", typesOf(evs))
	}
}
