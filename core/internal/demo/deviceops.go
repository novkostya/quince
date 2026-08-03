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

// inFlightMsg mirrors deviceops.inFlightMsg VERBATIM. The demo exists to script what the real
// manager does, so a message that drifted would make the demo teach a refusal the product does not
// give — and the e2e stories read this text.
const inFlightMsg = "another operation is already running on this device — wait for it to finish"

// startGuardedOp is the demo's copy of deviceops.Manager.startGuardedOp: ONE in-flight device op
// per UDID whatever its kind (quince#465, ruled 2026-08-02), released however the script ends.
//
// A copy rather than shared code because the two providers share no types — `demo.Provider`
// satisfies `httpapi.DeviceOps` structurally, which is the whole arrangement. What must not drift
// is the RULE and the message, so both are stated here against the real one.
//
// The demo's slot is separate from `p.running`, the backup slot, on purpose: whether a device op
// should also be refused while a BACKUP runs is explicitly NOT ruled (quince#465), and sharing one
// map would decide it by construction.
func (p *Provider) startGuardedOp(kind, udid, msg string, script func(opID string)) (string, bool) {
	opID := id.New()
	p.mu.Lock()
	if _, busy := p.opInflight[udid]; busy {
		p.mu.Unlock()
		return "", false
	}
	p.opInflight[udid] = opID
	p.ops[opID] = wire.Op{ID: opID, UDID: udid, Kind: kind, State: "running", Message: msg}
	op := p.ops[opID]
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventOpUpdated, op)
	go func() {
		defer p.releaseUDID(udid)
		script(opID)
	}()
	return opID, true
}

// releaseUDID frees the demo's per-UDID op slot on every exit path the script can take.
func (p *Provider) releaseUDID(udid string) {
	p.mu.Lock()
	delete(p.opInflight, udid)
	p.mu.Unlock()
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
	opID, free := p.startGuardedOp("pair", udid, "Starting pairing…", func(opID string) {
		p.scriptPair(opID, udid)
	})
	if !free {
		return "", http.StatusConflict, inFlightMsg
	}
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
	opID, free := p.startGuardedOp("encryption", udid, "Applying encryption change…", func(opID string) {
		p.scriptEncryption(opID, udid, action)
	})
	if !free {
		return "", http.StatusConflict, inFlightMsg
	}
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

// WifiSync scripts a Wi-Fi-sync op with the same validation the real manager applies.
func (p *Provider) WifiSync(_ context.Context, udid, action string) (string, int, string) {
	p.mu.RLock()
	dev, ok := p.devices[udid]
	p.mu.RUnlock()
	if !ok {
		return "", http.StatusNotFound, "no such device"
	}
	if dev.Paired != "yes" {
		return "", http.StatusConflict, "device is not paired with this host"
	}
	if action != "enable" && action != "disable" {
		return "", http.StatusUnprocessableEntity, "unknown action: " + action
	}
	msg := "Turning off Wi-Fi sync…"
	if action == "enable" {
		msg = "Turning on Wi-Fi sync so this device can back up without a cable…"
	}
	opID, free := p.startGuardedOp("wifi_sync", udid, msg, func(opID string) {
		p.scriptWifiSync(opID, udid, action)
	})
	if !free {
		return "", http.StatusConflict, inFlightMsg
	}
	return opID, http.StatusAccepted, ""
}

// scriptWifiSync deliberately has NO waiting_for_user step. Whether iOS demands an on-device
// confirmation for a lockdown write is unmeasured — it is one of qn.7's open questions — and
// scripting a passcode prompt the real op may never emit would make the demo teach a flow that
// does not exist. If hardware shows a confirmation, this gains one then.
func (p *Provider) scriptWifiSync(opID, udid, action string) {
	time.Sleep(900 * time.Millisecond)
	on := action == "enable"
	msg := "Wi-Fi sync is off. This device will only back up over USB."
	if on {
		msg = "Wi-Fi sync is on. This device can now back up over Wi-Fi."
	}
	p.setOp(wire.Op{ID: opID, UDID: udid, Kind: "wifi_sync", State: "succeeded", Message: msg})
	p.flipDevice(udid, func(d *wire.Device) {
		if on {
			d.WifiSync = "on"
		} else {
			d.WifiSync = "off"
		}
	})
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
