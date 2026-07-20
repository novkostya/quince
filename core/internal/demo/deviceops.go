package demo

import (
	"context"
	"net/http"
	"time"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/wire"
)

// The demo Provider implements httpapi.DeviceOps so `quince serve --demo` (and the UI e2e)
// exercise the full pair/encryption Op lifecycle — running → waiting_for_user → succeeded,
// narrated over op.updated, with the device's paired/encryption state flipping on success —
// entirely without hardware. Same primitive signatures the real deviceops.Manager satisfies.

func (p *Provider) setOp(op wire.Op) {
	p.mu.Lock()
	p.ops[op.ID] = op
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventOpUpdated, op)
}

// Op returns a pair/encryption op (GET /api/ops/{id}).
func (p *Provider) Op(opID string) (wire.Op, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	op, ok := p.ops[opID]
	return op, ok
}

// Pair scripts a pairing op. Pairing is USB-only, so a Wi-Fi-only device is refused with 409
// (matching the real manager), an unknown device with 404.
func (p *Provider) Pair(_ context.Context, udid string) (string, int, string) {
	p.mu.RLock()
	dev, ok := p.devices[udid]
	p.mu.RUnlock()
	if !ok {
		return "", http.StatusNotFound, "no such device"
	}
	if dev.Transports.USB == nil {
		return "", http.StatusConflict, "pairing needs a USB connection — connect the device by cable"
	}
	opID := id.New()
	p.setOp(wire.Op{ID: opID, UDID: udid, Kind: "pair", State: "running", Message: "Starting pairing…"})
	go p.scriptPair(opID, udid)
	return opID, http.StatusAccepted, ""
}

func (p *Provider) scriptPair(opID, udid string) {
	time.Sleep(700 * time.Millisecond)
	p.setOp(wire.Op{ID: opID, UDID: udid, Kind: "pair", State: "waiting_for_user",
		Message: "Tap Trust on the device, then enter its passcode."})
	time.Sleep(2 * time.Second)
	p.setOp(wire.Op{ID: opID, UDID: udid, Kind: "pair", State: "succeeded", Message: "Paired with this computer."})
	p.flipDevice(udid, func(d *wire.Device) { d.Paired = "yes" })
}

// Validate reports the demo device's paired state.
func (p *Provider) Validate(_ context.Context, udid string) (bool, int, string) {
	p.mu.RLock()
	dev, ok := p.devices[udid]
	p.mu.RUnlock()
	if !ok {
		return false, http.StatusNotFound, "no such device"
	}
	return dev.Paired == "yes", http.StatusOK, ""
}

// Encryption scripts an encryption op (enable/disable/change_password) with the same
// validation the real manager applies.
func (p *Provider) Encryption(_ context.Context, udid, action, password, oldPassword, newPassword string) (string, int, string) {
	p.mu.RLock()
	_, ok := p.devices[udid]
	p.mu.RUnlock()
	if !ok {
		return "", http.StatusNotFound, "no such device"
	}
	switch action {
	case "enable":
		if password == "" {
			return "", http.StatusUnprocessableEntity, "password is required to enable encryption"
		}
	case "disable":
		if password == "" {
			return "", http.StatusUnprocessableEntity, "the current backup password is required to disable encryption"
		}
	case "change_password":
		if oldPassword == "" || newPassword == "" {
			return "", http.StatusUnprocessableEntity, "old_password and new_password are required"
		}
	default:
		return "", http.StatusUnprocessableEntity, "unknown action: " + action
	}
	opID := id.New()
	p.setOp(wire.Op{ID: opID, UDID: udid, Kind: "encryption", State: "running", Message: "Applying encryption change…"})
	go p.scriptEncryption(opID, udid, action)
	return opID, http.StatusAccepted, ""
}

func (p *Provider) scriptEncryption(opID, udid, action string) {
	time.Sleep(700 * time.Millisecond)
	p.setOp(wire.Op{ID: opID, UDID: udid, Kind: "encryption", State: "waiting_for_user",
		Message: "Confirm the change on the device by entering its passcode."})
	time.Sleep(2 * time.Second)
	p.setOp(wire.Op{ID: opID, UDID: udid, Kind: "encryption", State: "succeeded", Message: "Done."})
	switch action {
	case "enable":
		p.flipDevice(udid, func(d *wire.Device) { d.BackupEncryption = "on" })
	case "disable":
		p.flipDevice(udid, func(d *wire.Device) { d.BackupEncryption = "off" })
	}
}

// flipDevice mutates the device under lock and announces device.updated.
func (p *Provider) flipDevice(udid string, mutate func(*wire.Device)) {
	p.mu.Lock()
	dev, ok := p.devices[udid]
	if !ok {
		p.mu.Unlock()
		return
	}
	mutate(&dev)
	p.devices[udid] = dev
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventDeviceUpdated, dev)
}
