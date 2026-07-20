package deviceops

import (
	"context"
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

	pairTimeout time.Duration
	pairPoll    time.Duration
	opTimeout   time.Duration
	enrichWait  time.Duration

	lockdown *LockdownStore // optional: persists pairing records after a successful pair

	mu  sync.Mutex
	ops map[string]wire.Op
}

// SetLockdown attaches a LockdownStore so a successful pair's records are backed up to
// persistent storage (amendment 1). Optional — nil means no persistence (e.g. tests).
func (m *Manager) SetLockdown(l *LockdownStore) { m.lockdown = l }

const opsSoftCap = 200 // prune terminal ops beyond this to bound the map

// NewManager wires the ops manager. baseCtx is the serve context; audit may be nil (skipped).
func NewManager(baseCtx context.Context, tools *Tools, devs Devices, b *bus.Bus, audit AuditSink, log *slog.Logger) *Manager {
	return &Manager{
		baseCtx:     baseCtx,
		tools:       tools,
		devs:        devs,
		bus:         b,
		audit:       audit,
		log:         log,
		newID:       id.New,
		pairTimeout: 2 * time.Minute,
		pairPoll:    2 * time.Second,
		opTimeout:   5 * time.Minute,
		enrichWait:  20 * time.Second,
		ops:         map[string]wire.Op{},
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
