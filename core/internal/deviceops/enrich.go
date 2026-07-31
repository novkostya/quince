package deviceops

import (
	"context"
	"log/slog"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/device"
	"github.com/novkostya/quince/core/internal/wire"
)

// enrichTargets is the slice of the registry the driver writes to (and re-reads on a
// subscription overflow). *device.Registry satisfies it.
type enrichTargets interface {
	Devices() []wire.Device
	Enrich(udid string, id device.Identity)
}

// EnrichDriver overlays lockdown identity onto the device table in response to attach events
// (design §3: enrichment is event-driven, never polled; off the request path). Attaches are
// per-UDID debounced so a burst of edges coalesces into one ideviceinfo/idevicepair read.
type EnrichDriver struct {
	tools    *Tools
	targets  enrichTargets
	bus      *bus.Bus
	log      *slog.Logger
	debounce time.Duration
	timeout  time.Duration
}

// NewEnrichDriver wires the driver. Run it under the serve context.
func NewEnrichDriver(tools *Tools, targets enrichTargets, b *bus.Bus, log *slog.Logger) *EnrichDriver {
	return &EnrichDriver{
		tools:    tools,
		targets:  targets,
		bus:      b,
		log:      log,
		debounce: 250 * time.Millisecond,
		timeout:  20 * time.Second,
	}
}

// Run consumes device.attached events until ctx is cancelled, scheduling a debounced
// enrichment per UDID. A subscription overflow is surfaced (no silent drop) and recovered by
// resubscribing and refreshing every currently-present device.
func (d *EnrichDriver) Run(ctx context.Context) {
	sub := d.bus.Subscribe(256)
	defer func() { d.bus.Unsubscribe(sub) }()

	timers := map[string]*time.Timer{}
	defer func() {
		for _, t := range timers {
			t.Stop()
		}
	}()

	// Enrich whatever is ALREADY present, once, before waiting for events (quince#350).
	//
	// The muxd clients start before this driver does (cmd/quince/live.go), so a device that was
	// already connected when quince started publishes its one and only device.attached before there
	// is a subscriber, and is then never enriched. A device plugged in later always wins that race;
	// a device present at boot loses it almost every time.
	//
	// It stayed invisible from qn.3 until qn.7 because the registry is seeded from SQLite, so a
	// missed enrichment still produced a device with a name, `paired: yes` and `encryption: on` —
	// the persisted values happened to be right. `wifi_sync` was the first field with no stale value
	// to hide behind, so it rendered `unknown`, which hid its badge and its control. Found on
	// hardware, by the Operator, 2026-07-31.
	//
	// A positive refresh rather than an ordering fix: refreshAll is idempotent and debounced, so a
	// device attaching DURING startup is simply enriched twice. Reordering live.go would also close
	// it — and would stay closed only until someone moved those lines, with nothing failing if they
	// did.
	d.refreshAll(ctx, timers)

	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Dropped():
			d.log.Warn("deviceops: enrichment subscription overflowed — resubscribing and refreshing all devices")
			d.bus.Unsubscribe(sub)
			sub = d.bus.Subscribe(256)
			d.refreshAll(ctx, timers)
		case env, ok := <-sub.C():
			if !ok {
				return
			}
			if env.Type != wire.EventDeviceAttached {
				continue
			}
			if de, ok := env.Data.(wire.DeviceEvent); ok {
				d.schedule(ctx, timers, de.UDID, de.Transport)
			}
		}
	}
}

func (d *EnrichDriver) schedule(ctx context.Context, timers map[string]*time.Timer, udid, transport string) {
	if t := timers[udid]; t != nil {
		t.Stop()
	}
	timers[udid] = time.AfterFunc(d.debounce, func() { d.enrichOne(ctx, udid, transport) })
}

func (d *EnrichDriver) enrichOne(ctx context.Context, udid, transport string) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	id, err := d.tools.Info(ctx, udid, transport)
	if err != nil {
		d.log.Warn("deviceops: enrichment read failed", "error", err)
		return
	}
	d.targets.Enrich(udid, id)
}

func (d *EnrichDriver) refreshAll(ctx context.Context, timers map[string]*time.Timer) {
	for _, dev := range d.targets.Devices() {
		if tr, ok := opTransport(dev); ok {
			d.schedule(ctx, timers, dev.UDID, tr)
		}
	}
}
