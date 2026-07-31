package deviceops

import (
	"context"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/wire"
)

func TestEnrichDriverEnrichesOnAttach(t *testing.T) {
	b := bus.New()
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID)) // present, so refreshAll/opTransport can resolve it too
	d := NewEnrichDriver(fakeTools("DEVICEOPS_FAKE=paired"), devs, b, discard())
	d.debounce = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	time.Sleep(50 * time.Millisecond) // let Run() establish its subscription
	b.PublishEvent(wire.EventDeviceAttached, wire.DeviceEvent{Device: usbDevice(fakeUDID), Transport: TransportUSB})

	// Poll for the single enrichment (each spawns the fake CLIs — don't re-publish in a tight
	// loop or the reads pile up).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if id, ok := devs.lastEnrich(fakeUDID); ok && id.Model == "iPhone17,2" {
			if id.Name != "synthetic-iphone" || id.Paired != "yes" || id.BackupEncryption != "on" {
				t.Fatalf("enriched identity = %+v", id)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("driver never enriched the attached device")
}

func TestEnrichDriverIgnoresNonAttach(t *testing.T) {
	b := bus.New()
	devs := newFakeDevices()
	d := NewEnrichDriver(fakeTools("DEVICEOPS_FAKE=paired"), devs, b, discard())
	d.debounce = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// A device.updated (not attached) must not trigger an enrichment read.
	for i := 0; i < 20; i++ {
		b.PublishEvent(wire.EventDeviceUpdated, wire.DeviceEvent{Device: usbDevice(fakeUDID), Transport: TransportUSB})
		time.Sleep(2 * time.Millisecond)
	}
	if _, ok := devs.lastEnrich(fakeUDID); ok {
		t.Fatal("device.updated should not trigger enrichment (would loop)")
	}
}

// quince#350: a device already present when the driver starts must be enriched WITHOUT any
// device.attached ever being published.
//
// The muxd clients start before this driver subscribes, so a device connected at boot publishes its
// one and only attach into the void. It hid from qn.3 until qn.7 because the registry is seeded from
// SQLite — the persisted name/paired/encryption happened to be right, so a missed enrichment looked
// like a working one. `wifi_sync` was the first field with no stale value to fall back on.
//
// The test publishes NOTHING. That is the whole assertion: if this only passes because an event
// arrives, it is not testing the race.
func TestEnrichDriverEnrichesADevicePresentBeforeItStarts(t *testing.T) {
	b := bus.New()
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID)) // already connected, as at boot
	d := NewEnrichDriver(fakeTools("DEVICEOPS_FAKE=paired"), devs, b, discard())
	d.debounce = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if id, ok := devs.lastEnrich(fakeUDID); ok && id.Model == "iPhone17,2" {
			if id.Paired != "yes" || id.BackupEncryption != "on" {
				t.Fatalf("enriched identity = %+v", id)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("a device present before the driver started was never enriched — quince#350, the bug " +
		"that made the Operator press Rescan after every restart")
}
