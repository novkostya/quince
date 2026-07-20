package device

import (
	"io"
	"log/slog"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/muxd"
	"github.com/novkostya/quince/core/internal/wire"
)

// Synthetic UDIDs only (a real SerialNumber is the device UDID — personal data). Source IDs
// mirror the default topology's muxer addresses.
const (
	udidA   = "SYNTHETIC-UDID-AAAA-0001"
	udidB   = "SYNTHETIC-UDID-BBBB-0002"
	srcUSB  = "/var/run/usbmuxd"
	srcWiFi = "127.0.0.1:27015"
)

func newTestRegistry(t *testing.T) (*Registry, *bus.Subscription) {
	t.Helper()
	b := bus.New()
	sub := b.Subscribe(64)
	t.Cleanup(func() { b.Unsubscribe(sub) })
	return NewRegistry(b, slog.New(slog.NewTextHandler(io.Discard, nil))), sub
}

func attach(udid, transport string) muxd.Event {
	return muxd.Event{Kind: muxd.Attached, UDID: udid, Transport: transport}
}
func detach(udid, transport string) muxd.Event {
	return muxd.Event{Kind: muxd.Detached, UDID: udid, Transport: transport}
}

// drain returns every envelope buffered so far. Publish is synchronous within Apply/Reset
// (same goroutine as the test), so everything emitted before this call is already queued.
func drain(sub *bus.Subscription) []wire.Envelope {
	var out []wire.Envelope
	for {
		select {
		case e := <-sub.C():
			out = append(out, e)
		default:
			return out
		}
	}
}

func typesOf(evs []wire.Envelope) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

func detachedUDIDs(evs []wire.Envelope) map[string]bool {
	out := map[string]bool{}
	for _, e := range evs {
		if e.Type == wire.EventDeviceDetached {
			if de, ok := e.Data.(wire.DeviceEvent); ok {
				out[de.UDID] = true
			}
		}
	}
	return out
}

func TestAttachThenDetach(t *testing.T) {
	reg, sub := newTestRegistry(t)
	s := reg.Sink(srcUSB)

	s.Apply(attach(udidA, muxd.TransportUSB))
	if got := typesOf(drain(sub)); len(got) != 1 || got[0] != wire.EventDeviceAttached {
		t.Fatalf("attach events = %v", got)
	}
	if devs := reg.Devices(); len(devs) != 1 || devs[0].UDID != udidA || devs[0].Transports.USB == nil {
		t.Fatalf("devices after attach = %+v", devs)
	}

	s.Apply(detach(udidA, muxd.TransportUSB))
	if got := typesOf(drain(sub)); len(got) != 1 || got[0] != wire.EventDeviceDetached {
		t.Fatalf("detach events = %v", got)
	}
	if devs := reg.Devices(); len(devs) != 0 {
		t.Fatalf("devices after detach = %+v (want empty)", devs)
	}
}

func TestPerTransportMergeTwoSources(t *testing.T) {
	reg, sub := newTestRegistry(t)
	sUSB := reg.Sink(srcUSB)
	sWiFi := reg.Sink(srcWiFi)

	sUSB.Apply(attach(udidA, muxd.TransportUSB))
	sWiFi.Apply(attach(udidA, muxd.TransportWiFi))
	if got := typesOf(drain(sub)); len(got) != 2 {
		t.Fatalf("merge attach events = %v (want 2 attached)", got)
	}
	dev, ok := reg.Device(udidA)
	if !ok || dev.Transports.USB == nil || dev.Transports.WiFi == nil {
		t.Fatalf("merged device = %+v ok=%v (want both transports)", dev, ok)
	}

	// dropping USB keeps the device on Wi-Fi (device.detached for the usb edge only)
	sUSB.Apply(detach(udidA, muxd.TransportUSB))
	if got := typesOf(drain(sub)); len(got) != 1 || got[0] != wire.EventDeviceDetached {
		t.Fatalf("usb detach events = %v", got)
	}
	dev, ok = reg.Device(udidA)
	if !ok || dev.Transports.USB != nil || dev.Transports.WiFi == nil {
		t.Fatalf("device after usb detach = %+v (want wifi only, still present)", dev)
	}

	// dropping Wi-Fi removes the device entirely
	sWiFi.Apply(detach(udidA, muxd.TransportWiFi))
	if _, ok := reg.Device(udidA); ok {
		t.Fatal("device still present after its last transport dropped")
	}
}

func TestDuplicateAttachSuppressed(t *testing.T) {
	reg, sub := newTestRegistry(t)
	s := reg.Sink(srcWiFi)
	s.Apply(attach(udidA, muxd.TransportWiFi))
	_ = drain(sub)
	// same source re-attaches the same transport (a replay / keepalive) → last_seen refresh
	// only, no new WS event.
	s.Apply(attach(udidA, muxd.TransportWiFi))
	if got := drain(sub); len(got) != 0 {
		t.Fatalf("refresh attach emitted %v (want none)", typesOf(got))
	}
}

func TestResetReconcileClearsPhantom(t *testing.T) {
	reg, sub := newTestRegistry(t)
	sUSB := reg.Sink(srcUSB)
	sWiFi := reg.Sink(srcWiFi)

	// A on both transports, B on wifi only.
	sUSB.Apply(attach(udidA, muxd.TransportUSB))
	sWiFi.Apply(attach(udidA, muxd.TransportWiFi))
	sWiFi.Apply(attach(udidB, muxd.TransportWiFi))
	_ = drain(sub)

	// The wifi muxer reconnects: Reset, then the replay carries ONLY A (B detached while we
	// were disconnected). B must be cleared (no phantom); A survives (usb from the other
	// source, wifi re-added).
	sWiFi.Reset()
	sWiFi.Apply(attach(udidA, muxd.TransportWiFi))
	got := drain(sub)

	if _, ok := reg.Device(udidB); ok {
		t.Fatal("phantom: B still present though the reconnect replay omitted it")
	}
	if dev, ok := reg.Device(udidA); !ok || dev.Transports.USB == nil || dev.Transports.WiFi == nil {
		t.Fatalf("A after reconnect = %+v ok=%v (want both transports)", dev, ok)
	}
	if !detachedUDIDs(got)[udidB] {
		t.Fatalf("expected a device.detached for the phantom B; events = %v", typesOf(got))
	}
}

func TestPerSourceIsolationOnReset(t *testing.T) {
	reg, sub := newTestRegistry(t)
	sUSB := reg.Sink(srcUSB)
	sWiFi := reg.Sink(srcWiFi)
	sUSB.Apply(attach(udidA, muxd.TransportUSB))
	sWiFi.Apply(attach(udidA, muxd.TransportWiFi))
	_ = drain(sub)

	// Resetting the USB source must not touch the Wi-Fi source's edge.
	sUSB.Reset()
	dev, ok := reg.Device(udidA)
	if !ok || dev.Transports.USB != nil || dev.Transports.WiFi == nil {
		t.Fatalf("after usb source reset: %+v ok=%v (want wifi-only, still present)", dev, ok)
	}
	if !detachedUDIDs(drain(sub))[udidA] {
		t.Fatal("expected device.detached for the dropped usb edge")
	}
}

func TestDevicesHygiene(t *testing.T) {
	reg, sub := newTestRegistry(t)
	if devs := reg.Devices(); devs == nil || len(devs) != 0 {
		t.Fatalf("empty registry Devices() = %v (want non-nil empty slice)", devs)
	}
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	_ = drain(sub)
	dev, _ := reg.Device(udidA)
	if dev.Paired != "unknown" || dev.BackupEncryption != "unknown" {
		t.Fatalf("muxd-minimal defaults = paired %q enc %q (want \"unknown\")", dev.Paired, dev.BackupEncryption)
	}
	if dev.Name != "" || dev.Model != "" || dev.IOSVersion != "" || dev.LastBackup != nil {
		t.Fatalf("identity fields must be empty this rung: %+v", dev)
	}
	if dev.LastSeen == "" || dev.Transports.USB == nil {
		t.Fatalf("expected usb transport + last_seen: %+v", dev)
	}
}

func TestEnrichOverlaysIdentityAndEmitsUpdate(t *testing.T) {
	reg, sub := newTestRegistry(t)
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	_ = drain(sub)

	reg.Enrich(udidA, Identity{
		Name: "synthetic-iphone", Model: "iPhone17,2", IOSVersion: "26.0.1",
		Paired: "yes", BackupEncryption: "on",
	})
	if got := typesOf(drain(sub)); len(got) != 1 || got[0] != wire.EventDeviceUpdated {
		t.Fatalf("enrich events = %v (want one device.updated)", got)
	}
	dev, _ := reg.Device(udidA)
	if dev.Name != "synthetic-iphone" || dev.Model != "iPhone17,2" || dev.IOSVersion != "26.0.1" ||
		dev.Paired != "yes" || dev.BackupEncryption != "on" {
		t.Fatalf("overlaid identity = %+v", dev)
	}
	// The muxer-derived fields survive the overlay.
	if dev.Transports.USB == nil || dev.LastSeen == "" {
		t.Fatalf("presence lost after enrich: %+v", dev)
	}
}

func TestEnrichEmptyFieldsLeaveHonestDefault(t *testing.T) {
	reg, sub := newTestRegistry(t)
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	_ = drain(sub)

	// Name known, but pairing/encryption not determined ("" → keep "unknown", never guess).
	reg.Enrich(udidA, Identity{Name: "synthetic-iphone"})
	dev, _ := reg.Device(udidA)
	if dev.Name != "synthetic-iphone" {
		t.Fatalf("name not overlaid: %+v", dev)
	}
	if dev.Paired != "unknown" || dev.BackupEncryption != "unknown" {
		t.Fatalf("empty identity fields must leave defaults: paired %q enc %q", dev.Paired, dev.BackupEncryption)
	}
}

func TestEnrichNoChangeSuppressesUpdate(t *testing.T) {
	reg, sub := newTestRegistry(t)
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	id := Identity{Name: "synthetic-iphone", Paired: "yes", BackupEncryption: "on"}
	reg.Enrich(udidA, id)
	_ = drain(sub)

	// Re-enriching with the identical identity emits nothing (keep the WS quiet).
	reg.Enrich(udidA, id)
	if got := drain(sub); len(got) != 0 {
		t.Fatalf("no-op enrich emitted %v (want none)", typesOf(got))
	}
}

func TestEnrichAbsentDeviceRetainedNoEmit(t *testing.T) {
	reg, sub := newTestRegistry(t)

	// Enrich a UDID with no live transport: retained, but nothing to update yet.
	reg.Enrich(udidA, Identity{Name: "synthetic-iphone", Paired: "yes"})
	if got := drain(sub); len(got) != 0 {
		t.Fatalf("enrich of an absent device emitted %v (want none)", typesOf(got))
	}
	if _, ok := reg.Device(udidA); ok {
		t.Fatal("absent device should not appear in the table from enrich alone")
	}
	// A later attach carries the retained identity immediately (device.attached shell).
	reg.Sink(srcUSB).Apply(attach(udidA, muxd.TransportUSB))
	evs := drain(sub)
	if len(evs) != 1 || evs[0].Type != wire.EventDeviceAttached {
		t.Fatalf("attach events = %v", typesOf(evs))
	}
	de, ok := evs[0].Data.(wire.DeviceEvent)
	if !ok || de.Name != "synthetic-iphone" || de.Paired != "yes" {
		t.Fatalf("attach shell missing retained identity: %+v", evs[0].Data)
	}
}
