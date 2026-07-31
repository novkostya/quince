package deviceops

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/device"
	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Devices is the slice of the registry the manager needs: read a device's presence
// (transport selection + existence) and overlay refreshed identity after an op. *device.Registry
// satisfies it.
type Devices interface {
	Device(udid string) (wire.Device, bool)
	Enrich(udid string, id device.Identity)
}

// AuditSink records security-audit rows (design §6). *store.Store satisfies it. Details never
// carry a secret.
type AuditSink interface {
	AppendAudit(store.AuditEntry) error
}

// Manager owns the async Op lifecycle for pair/encryption (contracts §2) and serves
// GET /api/ops/{id}. It implements httpapi.DeviceOps structurally (primitive returns → no
// httpapi import). Op goroutines run under baseCtx (the serve context), so shutdown cancels
// them and the group-kill reaps the CLI child.
type Manager struct {
	baseCtx context.Context
	tools   *Tools
	devs    Devices
	bus     *bus.Bus
	audit   AuditSink
	log     *slog.Logger
	newID   func() string

	pairTimeout     time.Duration
	pairPoll        time.Duration
	opTimeout       time.Duration
	enrichWait      time.Duration
	validateTimeout time.Duration // amendment A: bound the non-interactive validate read

	lockdown *LockdownStore // optional: persists pairing records after a successful pair

	mu  sync.Mutex
	ops map[string]wire.Op
}

// SetLockdown attaches a LockdownStore so a successful pair's records are backed up to
// persistent storage (amendment 1). Optional — nil means no persistence (e.g. tests).
func (m *Manager) SetLockdown(l *LockdownStore) { m.lockdown = l }

const opsSoftCap = 200 // prune terminal ops beyond this to bound the map

// deviceOpTimeout bounds a fast, non-interactive device READ (idevicepair validate) Go-side
// (qn.6b amendment A). Patch 0001 raised libimobiledevice's default receive timeout to 15min for
// the backup path, and that default leaks into EVERY libimobiledevice binary — so without a
// Go-side deadline a wedged lockdown read here would sit 15min instead of failing in ~30s as it
// did before the patch. The INTERACTIVE ops keep their longer intentional bounds (pair 2m,
// encryption 5m): they wait for on-device Trust / passcode entry, and 2m/5m already cap the child
// well under the 15min receive patience. The event-driven enrichment reads are already bounded
// (enrichWait / EnrichDriver.timeout, both 20s).
const deviceOpTimeout = 30 * time.Second

// NewManager wires the ops manager. baseCtx is the serve context; audit may be nil (skipped).
func NewManager(baseCtx context.Context, tools *Tools, devs Devices, b *bus.Bus, audit AuditSink, log *slog.Logger) *Manager {
	return &Manager{
		baseCtx:         baseCtx,
		tools:           tools,
		devs:            devs,
		bus:             b,
		audit:           audit,
		log:             log,
		newID:           id.New,
		pairTimeout:     2 * time.Minute,
		pairPoll:        2 * time.Second,
		opTimeout:       5 * time.Minute,
		enrichWait:      20 * time.Second,
		validateTimeout: deviceOpTimeout,
		ops:             map[string]wire.Op{},
	}
}

// Op returns the current state of an op (GET /api/ops/{id} poll/refresh fallback).
func (m *Manager) Op(id string) (wire.Op, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.ops[id]
	return op, ok
}

// --- op state transitions (mutate under lock, publish after unlock) ---

func (m *Manager) startOp(kind, udid, msg string) wire.Op {
	op := wire.Op{ID: m.newID(), UDID: udid, Kind: kind, State: "running", Message: msg}
	m.mu.Lock()
	m.pruneLocked()
	m.ops[op.ID] = op
	m.mu.Unlock()
	m.bus.PublishEvent(wire.EventOpUpdated, op)
	return op
}

func (m *Manager) setOp(id, state, msg string, opErr *wire.JobError) {
	m.mu.Lock()
	op, ok := m.ops[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	op.State = state
	if msg != "" {
		op.Message = msg
	}
	if opErr != nil {
		op.Error = opErr
	}
	m.ops[id] = op
	m.mu.Unlock()
	m.bus.PublishEvent(wire.EventOpUpdated, op)
}

// pruneLocked drops terminal ops once the map grows past the soft cap (bounded memory).
func (m *Manager) pruneLocked() {
	if len(m.ops) < opsSoftCap {
		return
	}
	for id, op := range m.ops {
		if op.State == "succeeded" || op.State == "failed" {
			delete(m.ops, id)
		}
	}
}

// --- pairing ---

// Pair starts an async pairing op. Returns (opID, 202, "") on accept, else ("", status,
// reason): 404 unknown device, 409 not on USB (pairing is USB-only, stack D2), 400 bad udid.
func (m *Manager) Pair(_ context.Context, udid string) (string, int, string) {
	if !validUDID(udid) {
		return "", http.StatusBadRequest, "invalid udid"
	}
	dev, ok := m.devs.Device(udid)
	if !ok {
		return "", http.StatusNotFound, "no such device"
	}
	if dev.Transports.USB == nil {
		return "", http.StatusConflict, "pairing needs a USB connection — connect the device by cable"
	}
	op := m.startOp("pair", udid, "Starting pairing…")
	go m.runPair(op.ID, udid)
	return op.ID, http.StatusAccepted, ""
}

func (m *Manager) runPair(opID, udid string) {
	ctx, cancel := context.WithTimeout(m.baseCtx, m.pairTimeout)
	defer cancel()
	lastMsg := ""
	for {
		outcome, msg, err := m.tools.pairAttempt(ctx, udid, TransportUSB)
		switch outcome {
		case pairPaired:
			m.setOp(opID, "succeeded", msg, nil)
			if m.lockdown != nil {
				m.lockdown.Backup() // persist the new pairing record (amendment 1)
			}
			m.reEnrich(udid, TransportUSB)
			m.auditEvent("device.pair", udid, "paired")
			return
		case pairNeedTrust, pairNeedPasscode:
			if msg != lastMsg { // narrate the wait once (and again if the ask changes)
				m.setOp(opID, "waiting_for_user", msg, nil)
				lastMsg = msg
			}
			select {
			case <-ctx.Done():
				m.setOp(opID, "failed", "", &wire.JobError{Code: "timeout", Message: "Pairing timed out waiting for Trust/passcode on the device."})
				m.auditEvent("device.pair", udid, "timeout")
				return
			case <-time.After(m.pairPoll):
			}
		case pairDenied:
			m.setOp(opID, "failed", "", &wire.JobError{Code: "trust_denied", Message: msg})
			m.auditEvent("device.pair", udid, "denied")
			return
		case pairNotUSB:
			m.setOp(opID, "failed", "", &wire.JobError{Code: "needs_usb", Message: msg})
			return
		default: // pairFailed
			m.setOp(opID, "failed", "", &wire.JobError{Code: "pair_failed", Message: opErrMsg(err)})
			m.auditEvent("device.pair", udid, "error")
			return
		}
	}
}

// Validate reports current pairing state (contracts §1 POST .../pair/validate → {paired}).
// Returns (paired, 200, "") on success, else (false, status, reason): 400 bad udid, 404
// unknown device, 409 not connected, 502 the device query failed.
func (m *Manager) Validate(ctx context.Context, udid string) (bool, int, string) {
	if !validUDID(udid) {
		return false, http.StatusBadRequest, "invalid udid"
	}
	dev, ok := m.devs.Device(udid)
	if !ok {
		return false, http.StatusNotFound, "no such device"
	}
	transport, ok := opTransport(dev)
	if !ok {
		return false, http.StatusConflict, "device is not connected"
	}
	// Bound the read Go-side (amendment A): otherwise a wedged validate would inherit the patched
	// 15-min receive timeout. WithTimeout takes the earlier of this and any caller deadline.
	ctx, cancel := context.WithTimeout(ctx, m.validateTimeout)
	defer cancel()
	paired, err := m.tools.Validate(ctx, udid, transport)
	if err != nil {
		return false, http.StatusBadGateway, "could not query the device"
	}
	return paired, http.StatusOK, ""
}

// --- encryption ---

// Encryption starts an async encryption op (enable/disable/change_password). Returns (opID,
// 202, "") on accept, else ("", status, reason): 404 unknown device, 409 not connected, 422
// bad action / missing password.
func (m *Manager) Encryption(_ context.Context, udid, action, password, oldPassword, newPassword string) (string, int, string) {
	if !validUDID(udid) {
		return "", http.StatusBadRequest, "invalid udid"
	}
	dev, ok := m.devs.Device(udid)
	if !ok {
		return "", http.StatusNotFound, "no such device"
	}
	transport, ok := opTransport(dev)
	if !ok {
		return "", http.StatusConflict, "device is not connected"
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
	op := m.startOp("encryption", udid, encStartMsg(action))
	go m.runEncryption(op.ID, udid, transport, action, password, oldPassword, newPassword)
	return op.ID, http.StatusAccepted, ""
}

// WifiSync enables or disables the device's Wi-Fi-sync flag (contracts §1
// POST /api/devices/{udid}/wifi-sync). Same validation ladder as Encryption minus every password
// branch — the value is a boolean, so there is no secret to require or to protect.
func (m *Manager) WifiSync(_ context.Context, udid, action string) (string, int, string) {
	if !validUDID(udid) {
		return "", http.StatusBadRequest, "invalid udid"
	}
	dev, ok := m.devs.Device(udid)
	if !ok {
		return "", http.StatusNotFound, "no such device"
	}
	// Refuse on a device that is not CONFIRMED paired rather than letting the write fail deeper in:
	// setting a lockdown value needs a trusted session, and "not paired" is a state the user can act
	// on where "the device rejected it" is not.
	if dev.Paired != "yes" {
		return "", http.StatusConflict, "device is not paired with this host"
	}
	transport, ok := opTransport(dev)
	if !ok {
		return "", http.StatusConflict, "device is not connected"
	}
	if action != "enable" && action != "disable" {
		return "", http.StatusUnprocessableEntity, "unknown action: " + action
	}
	op := m.startOp("wifi_sync", udid, wifiSyncStartMsg(action))
	go m.runWifiSync(op.ID, udid, transport, action)
	return op.ID, http.StatusAccepted, ""
}

func (m *Manager) runWifiSync(opID, udid, transport, action string) {
	ctx, cancel := context.WithTimeout(m.baseCtx, deviceOpTimeout)
	defer cancel()

	// A Wi-Fi-sync op is the one op whose effect nobody can go back and check: disabling severs the
	// transport it ran on, so the device is gone before anything could re-read it, and the value
	// this function publishes is the ONLY record that the write happened. A silent success is
	// therefore indistinguishable from a silent no-op when a report arrives saying the badge did
	// not move — which is exactly what happened on hardware (quince#325), leaving nothing in the
	// log to tell the two apart.
	m.log.Info("deviceops: wifi_sync starting", "op", opID, "udid", udid, "action", action, "transport", transport)

	if err := m.tools.SetWifiSync(ctx, udid, transport, action == "enable"); err != nil {
		// DISABLING OVER WI-FI SEVERS THE READ-BACK, AND THAT IS SUCCESS, NOT FAILURE.
		//
		// The write removes the device's ability to answer over the transport the write ran on, so
		// the verification cannot run — success and unverifiability are the same event, on this one
		// path. Reporting failure told the Operator "the device accepted the change but did not
		// apply it; Wi-Fi sync is unchanged" about a device that HAD changed and had left Wi-Fi,
		// and could only be recovered with a cable. Every clause was false (quince#363).
		//
		// RECOGNISED, NOT GUESSED — the conjunction is deliberately narrow, and each clause carries
		// weight. Only a disable, only over Wi-Fi, only after a clean set, and only when the
		// read-back returned NO VALUE. A read-back that succeeds and reports the old value is a
		// genuine lying write and keeps its error; a read-back that fails on any other path has no
		// causal story explaining it and must not borrow this exemption.
		if errors.Is(err, ErrWifiSyncUnreadable) && action == "disable" && transport == TransportWiFi {
			m.wifiSyncDisableUnreadable(opID, udid, transport)
			return
		}
		m.log.Warn("deviceops: wifi_sync failed",
			"op", opID, "udid", udid, "action", action, "transport", transport, "error", err)
		// Three failures the user must be able to tell apart, because the remedy differs and only
		// one of them is "try again".
		code, msg := "wifi_sync_failed", opErrMsg(err)
		switch {
		case errors.Is(err, ErrWifiSyncUnverifiable):
			code = "wifi_sync_unavailable"
			msg = "This build does not know which lockdown key holds the setting, so quince will not guess."
		case errors.Is(err, ErrWifiSyncNotApplied):
			code = "wifi_sync_not_applied"
			msg = "The device accepted the change but did not apply it. Wi-Fi sync is unchanged."
		case errors.Is(err, ErrWifiSyncUnreadable):
			// Reached only on the paths the exemption above does NOT forgive — an enable, or over
			// USB — where nothing about the write explains why the device stopped answering.
			//
			// Its own code rather than the generic one: `wifi_sync_failed` means "the device
			// REJECTED it", which is retryable, and this is the opposite — the device accepted the
			// write and the verification could not run. The state is UNKNOWN, which is neither
			// "rejected" nor "unchanged" (quince#363).
			code = "wifi_sync_unconfirmed"
			msg = "The device accepted the change, but quince could not read the setting back to " +
				"confirm it. Wi-Fi sync may or may not have changed."
		}
		m.setOp(opID, "failed", "", &wire.JobError{Code: code, Message: msg})
		m.auditEvent("device.wifi_sync."+action, udid, "failed")
		return
	}
	m.setOp(opID, "succeeded", wifiSyncDoneMsg(action), nil)

	// Publish the value SetWifiSync already read back, rather than re-reading it.
	//
	// reEnrich would be the obvious call and it is the wrong one here: disabling Wi-Fi sync on a
	// Wi-Fi-connected device severs the transport, so by the time reEnrich runs, Info() fails, logs
	// a warning and returns WITHOUT updating — leaving the UI showing `on` for a device that is now
	// off and gone. Observed on hardware 2026-07-31.
	//
	// The value here is not a guess: SetWifiSync only returns nil after reading the flag back from
	// the device and confirming it changed. Enrich replaces the whole identity, so the rest is
	// carried over from what the registry already holds.
	newState := "off"
	if action == "enable" {
		newState = "on"
	}
	if dev, ok := m.devs.Device(udid); ok {
		m.devs.Enrich(udid, device.Identity{
			Name: dev.Name, Model: dev.Model, IOSVersion: dev.IOSVersion,
			Paired: dev.Paired, BackupEncryption: dev.BackupEncryption,
			WifiSync: newState,
		})
		m.log.Info("deviceops: wifi_sync applied and published",
			"op", opID, "udid", udid, "action", action, "transport", transport, "wifi_sync", newState)
	} else {
		// The write SUCCEEDED on the device and the registry has already forgotten it, so there is
		// nothing to publish onto and the new value is lost — the UI keeps whatever it last saw.
		// Device() is false only when the UDID is neither present nor holding a committed version,
		// which a disable-over-Wi-Fi can reach by severing its own transport. Loud, because the
		// device is now in a state quince asked for and cannot show.
		m.log.Warn("deviceops: wifi_sync applied but NOT published — the registry no longer holds this device",
			"op", opID, "udid", udid, "action", action, "transport", transport, "wifi_sync", newState)
	}
	// Still refresh the rest of the identity when the device is reachable — a USB-connected device
	// stays put, and this keeps the op's behaviour identical to the other device ops there.
	if action == "enable" || transport == TransportUSB {
		m.reEnrich(udid, transport)
	}
	m.auditEvent("device.wifi_sync."+action, udid, "ok")
}

// wifiSyncDisableUnreadable handles the one path where a failed read-back means the write WORKED:
// disabling over Wi-Fi, which severs the connection the read-back would use.
//
// THE OP SUCCEEDS AND THE VALUE BECOMES `unknown`, and those are not in tension — they are different
// facts. The operation was sent, accepted, and produced its expected consequence; the value is
// simply unread. Ruled 2026-07-31 (quince#363).
//
// `unknown` rather than an inferred `off`, which was the tempting answer and is the wrong one:
//
//   - nobody has confirmed by cable that the flag is false — the evidence is the device leaving
//     Wi-Fi, which is strong but indirect, so `off` is a claim that a pending measurement could
//     falsify while `unknown` is correct either way;
//   - Enrich writes through to SQLite, so a wrong inference would PERSIST as a confident value with
//     nothing to contradict it — the shape that hid quince#350 for four rungs;
//   - `unknown` self-heals. The next enrichment over USB reads the truth and the badge corrects
//     itself; a wrong `off` is only ever corrected by someone noticing.
//
// The tri-state exists for "quince has not read the flag". This is exactly that.
func (m *Manager) wifiSyncDisableUnreadable(opID, udid, transport string) {
	m.log.Info("deviceops: wifi_sync disable accepted, read-back unreachable — reporting success with an unknown value",
		"op", opID, "udid", udid, "action", "disable", "transport", transport)

	m.setOp(opID, "succeeded", wifiSyncDisconnectedMsg(), nil)

	// Publish `unknown` rather than leaving the stale `on` standing: the badge then hides itself
	// instead of asserting a value quince has not read.
	if dev, ok := m.devs.Device(udid); ok {
		m.devs.Enrich(udid, device.Identity{
			Name: dev.Name, Model: dev.Model, IOSVersion: dev.IOSVersion,
			Paired: dev.Paired, BackupEncryption: dev.BackupEncryption,
			WifiSync: "unknown",
		})
	}
	m.auditEvent("device.wifi_sync.disable", udid, "ok_unreadable")
}

// wifiSyncDisconnectedMsg narrates the case above. Three clauses and no more, and NOT ONE of them
// claims a read that did not happen: what quince did, why the device is gone, and what the user
// does next. "as far as quince could tell" is load-bearing — the alternative wordings all quietly
// assert the value was verified.
func wifiSyncDisconnectedMsg() string {
	return "Wi-Fi sync is off on the device as far as quince could tell — the change was accepted " +
		"and the device then left Wi-Fi, which is what turning this off does. quince cannot read the " +
		"setting back over a connection that no longer exists. Reconnect by cable to confirm it or " +
		"turn it back on."
}

func wifiSyncStartMsg(action string) string {
	if action == "enable" {
		return "Turning on Wi-Fi sync so this device can back up without a cable…"
	}
	return "Turning off Wi-Fi sync…"
}

func wifiSyncDoneMsg(action string) string {
	if action == "enable" {
		return "Wi-Fi sync is on. This device can now back up over Wi-Fi."
	}
	return "Wi-Fi sync is off. This device will only back up over USB."
}

func (m *Manager) runEncryption(opID, udid, transport, action, password, oldPassword, newPassword string) {
	ctx, cancel := context.WithTimeout(m.baseCtx, m.opTimeout)
	defer cancel()
	onConfirm := func() {
		m.setOp(opID, "waiting_for_user", "Confirm the change on the device by entering its passcode.", nil)
	}

	var err error
	switch action {
	case "enable":
		err = m.tools.Encryption(ctx, udid, transport, true, password, onConfirm)
	case "disable":
		err = m.tools.Encryption(ctx, udid, transport, false, password, onConfirm)
	case "change_password":
		err = m.tools.ChangePassword(ctx, udid, transport, oldPassword, newPassword, onConfirm)
	}

	if err != nil {
		m.setOp(opID, "failed", "", &wire.JobError{Code: "encryption_failed", Message: opErrMsg(err)})
		m.auditEvent("device.encryption."+action, udid, "failed")
		return
	}
	m.setOp(opID, "succeeded", encDoneMsg(action), nil)
	m.reEnrich(udid, transport)
	m.auditEvent("device.encryption."+action, udid, "ok")
}

// --- helpers ---

// reEnrich refreshes a device's identity after a successful op (paired / encryption flip),
// bounded by enrichWait, off the request path.
// RefreshEncryption re-reads a device's backup-encryption state from lockdown RIGHT NOW and
// overlays the whole fresh identity onto the registry (so the UI's badge self-corrects too).
// It is the backup engine's preflight prober: the registry's cached value can read `unknown`
// merely because enrichment ran while lockdown was still cold, which used to hard-fail a
// legitimately-encrypted device's backup ((bw), qn.4a finding (i)-B).
//
// It reuses Info, so it inherits qn.3's hardware-learned safety: a device that is not CONFIRMED
// paired gets the simple (-s) read, which cannot auto-pair — a preflight probe can never surface
// an unexpected Trust prompt. ok=false means the read failed; the caller must not infer a state
// from that (state honesty).
func (m *Manager) RefreshEncryption(ctx context.Context, udid, transport string) (string, bool) {
	id, err := m.tools.Info(ctx, udid, transport)
	if err != nil {
		m.log.Warn("deviceops: live encryption probe failed", "udid", udid, "error", err)
		return "", false
	}
	m.devs.Enrich(udid, id)
	if id.BackupEncryption == "" {
		return "unknown", true
	}
	return id.BackupEncryption, true
}

func (m *Manager) reEnrich(udid, transport string) {
	ctx, cancel := context.WithTimeout(m.baseCtx, m.enrichWait)
	defer cancel()
	id, err := m.tools.Info(ctx, udid, transport)
	if err != nil {
		m.log.Warn("deviceops: re-enrich after op failed", "error", err)
		return
	}
	m.devs.Enrich(udid, id)
}

// auditEvent appends an audit row for a device op. detail is a short outcome word ("paired",
// "ok", "failed", …) — NEVER a password (design §6). udid identifies the user's own device.
func (m *Manager) auditEvent(event, udid, outcome string) {
	if m.audit == nil {
		return
	}
	if err := m.audit.AppendAudit(store.AuditEntry{
		ID:     m.newID(),
		TS:     time.Now().UTC(),
		Event:  event,
		Detail: udid + " " + outcome,
	}); err != nil {
		m.log.Warn("deviceops: audit append failed", "event", event, "error", err)
	}
}

func opTransport(dev wire.Device) (string, bool) {
	if dev.Transports.USB != nil {
		return TransportUSB, true
	}
	if dev.Transports.WiFi != nil {
		return TransportWiFi, true
	}
	return "", false
}

func opErrMsg(err error) string {
	if err == nil {
		return "operation failed"
	}
	return err.Error()
}

func encStartMsg(action string) string {
	switch action {
	case "enable":
		return "Enabling backup encryption…"
	case "disable":
		return "Disabling backup encryption…"
	case "change_password":
		return "Changing the backup password…"
	default:
		return "Working…"
	}
}

func encDoneMsg(action string) string {
	switch action {
	case "enable":
		return "Backup encryption is on."
	case "disable":
		return "Backup encryption is off."
	case "change_password":
		return "Backup password changed."
	default:
		return "Done."
	}
}
