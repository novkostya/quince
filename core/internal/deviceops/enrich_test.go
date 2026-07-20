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
