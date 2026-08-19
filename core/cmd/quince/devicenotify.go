package main

import (
	"log/slog"
	"net/http"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// deviceNotifications is the live httpapi.DeviceNotifications: it records the per-device
// notifications switch (quince#1270) and makes the change visible without a refresh.
//
// IT LIVES HERE RATHER THAN IN httpapi BECAUSE IT IS WIRING. The three things it needs — the store,
// the device registry and the bus — are exactly the three `httpapi` deliberately does not import,
// which is why every other subsystem reaches it through a consumer-defined interface. This is the
// smallest thing that can hold all three.
type deviceNotifications struct {
	log     *slog.Logger
	store   *store.Store
	devices interface {
		Device(udid string) (wire.Device, bool)
	}
	bus *bus.Bus
}

// SetNotificationsEnabled writes the preference, then announces the device.
//
// EXISTENCE IS CHECKED FIRST, and 404 is the honest answer for a UDID quince has never seen: an
// unknown device is not a device that is muted, and writing a preference row for one would leave a
// row nothing will ever read. The registry answers for offline devices too (qn.6a), so a phone that
// is in a drawer — which is the case this feature exists for — is settable.
//
// THE ANNOUNCE IS READ BACK FROM THE REGISTRY rather than assembled here. The registry resolves
// `notifications_enabled` through the source wired in live.go, so re-reading is what makes the
// published device the same object `GET /api/devices` would serve. Building one here would be a
// second construction of a device, which is the drift quince#361 was.
func (n deviceNotifications) SetNotificationsEnabled(udid string, enabled bool) (bool, int, string) {
	if _, ok := n.devices.Device(udid); !ok {
		return false, http.StatusNotFound, "no such device"
	}
	if err := n.store.SetDeviceNotificationsEnabled(udid, enabled); err != nil {
		n.log.Error("device notification preference write failed", "error", err)
		return false, http.StatusInternalServerError, "the preference could not be saved"
	}
	// A FAILURE TO ANNOUNCE IS NOT A FAILURE TO SAVE. The write has happened and the API says so;
	// an open page that missed the event is one refresh from the truth, and reporting 500 here would
	// tell the user their choice was not recorded when it was.
	// ONE RE-READ, SERVING TWO PURPOSES. The device it returns is what gets published, and the
	// value on it is what gets echoed — both resolved through the registry's preference source,
	// so the body and the event and the next GET cannot disagree with each other.
	//
	// THE FALLBACK IS THE REQUESTED VALUE, and only a device that disappeared between the write
	// and the re-read can reach it. That is not a guess: the write succeeded with exactly this
	// value one line above. Returning `false` there would report a device as muted because it
	// was unplugged.
	stored := enabled
	if dev, ok := n.devices.Device(udid); ok {
		stored = dev.NotificationsEnabled
		n.bus.PublishEvent(wire.EventDeviceUpdated, dev)
	}
	return stored, http.StatusOK, ""
}
