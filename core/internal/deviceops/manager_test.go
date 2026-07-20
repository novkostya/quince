package deviceops

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/device"
	"github.com/novkostya/quince/core/internal/wire"
)

// fakeDevices is an in-memory stand-in for the registry (Device/Enrich/Devices).
type fakeDevices struct {
	mu       sync.Mutex
	devs     map[string]wire.Device
	enriched map[string]device.Identity
}

func newFakeDevices() *fakeDevices {
	return &fakeDevices{devs: map[string]wire.Device{}, enriched: map[string]device.Identity{}}
}

func (f *fakeDevices) add(dev wire.Device) {
	f.mu.Lock()
	f.devs[dev.UDID] = dev
	f.mu.Unlock()
}
func (f *fakeDevices) Device(udid string) (wire.Device, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devs[udid]
	return d, ok
}
func (f *fakeDevices) Devices() []wire.Device {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wire.Device, 0, len(f.devs))
	for _, d := range f.devs {
		out = append(out, d)
	}
	return out
}
func (f *fakeDevices) Enrich(udid string, id device.Identity) {
	f.mu.Lock()
	f.enriched[udid] = id
	f.mu.Unlock()
}
func (f *fakeDevices) lastEnrich(udid string) (device.Identity, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.enriched[udid]
	return id, ok
}

func usbDevice(udid string) wire.Device {
	now := wire.Now()
	return wire.Device{UDID: udid, Transports: wire.Transports{USB: &now}}
}

func newTestManager(t *testing.T, devs Devices, env ...string) *Manager {
	t.Helper()
	m := NewManager(context.Background(), fakeTools(env...), devs, bus.New(), nil, discard())
	m.pairPoll = 5 * time.Millisecond // don't slow the poll loop in tests
	return m
}

// waitEnrich polls until the device is re-enriched to satisfy ok (enrichment is a follow-up
// refresh after the op succeeds — it spawns subprocesses, so it lands a beat later).
func waitEnrich(t *testing.T, devs *fakeDevices, udid string, ok func(device.Identity) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if id, has := devs.lastEnrich(udid); has && ok(id) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("device %s never re-enriched as expected", udid)
}

// waitOp polls until the op reaches a terminal state (or times out).
func waitOp(t *testing.T, m *Manager, opID string) wire.Op {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if op, ok := m.Op(opID); ok && (op.State == "succeeded" || op.State == "failed") {
			return op
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("op %s never reached a terminal state", opID)
	return wire.Op{}
}

func TestPairSuccess(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	opID, status, reason := m.Pair(context.Background(), fakeUDID)
	if status != 202 || opID == "" {
		t.Fatalf("Pair = %d %q (want 202 + op_id)", status, reason)
	}
	op := waitOp(t, m, opID)
	if op.State != "succeeded" || op.Kind != "pair" {
		t.Fatalf("pair op = %+v (want succeeded)", op)
	}
	// Success re-enriches the device (follow-up refresh, lands after the op succeeds).
	waitEnrich(t, devs, fakeUDID, func(id device.Identity) bool { return id.Paired == "yes" })
}

func TestPairWaitsForTrustThenSucceeds(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := NewManager(context.Background(), fakeTools(
		"DEVICEOPS_FAKE=trust_then_success",
		"DEVICEOPS_COUNTER="+t.TempDir()+"/c",
		"DEVICEOPS_TRUST_UNTIL=2",
	), devs, bus.New(), nil, discard())
	m.pairPoll = 5 * time.Millisecond

	// Observe op.updated to confirm a waiting_for_user was narrated before succeeding.
	sub := m.bus.Subscribe(64)
	defer m.bus.Unsubscribe(sub)

	opID, status, _ := m.Pair(context.Background(), fakeUDID)
	if status != 202 {
		t.Fatalf("Pair status = %d", status)
	}
	op := waitOp(t, m, opID)
	if op.State != "succeeded" {
		t.Fatalf("pair op final = %+v (want succeeded after retries)", op)
	}
	sawWaiting := false
	for {
		select {
		case env := <-sub.C():
			if o, ok := env.Data.(wire.Op); ok && o.State == "waiting_for_user" {
				sawWaiting = true
			}
			continue
		default:
		}
		break
	}
	if !sawWaiting {
		t.Fatal("expected a waiting_for_user op.updated during the trust retry loop")
	}
}

func TestPairRejectedWhenNotOnUSB(t *testing.T) {
	devs := newFakeDevices()
	now := wire.Now()
	devs.add(wire.Device{UDID: fakeUDID, Transports: wire.Transports{WiFi: &now}}) // wifi only
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	_, status, reason := m.Pair(context.Background(), fakeUDID)
	if status != 409 || reason == "" {
		t.Fatalf("wifi-only pair = %d %q (want 409 with reason)", status, reason)
	}
}

func TestPairUnknownDevice(t *testing.T) {
	m := newTestManager(t, newFakeDevices(), "DEVICEOPS_FAKE=paired")
	if _, status, _ := m.Pair(context.Background(), fakeUDID); status != 404 {
		t.Fatalf("unknown-device pair status = %d (want 404)", status)
	}
}

func TestEncryptionEnableSucceeds(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	opID, status, reason := m.Encryption(context.Background(), fakeUDID, "enable", "s3cret", "", "")
	if status != 202 || opID == "" {
		t.Fatalf("enable = %d %q (want 202 + op_id)", status, reason)
	}
	op := waitOp(t, m, opID)
	if op.State != "succeeded" || op.Kind != "encryption" {
		t.Fatalf("encryption op = %+v (want succeeded)", op)
	}
	waitEnrich(t, devs, fakeUDID, func(id device.Identity) bool { return id.BackupEncryption == "on" })
}

func TestEncryptionChangePasswordSucceeds(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	opID, status, _ := m.Encryption(context.Background(), fakeUDID, "change_password", "", "oldpw", "newpw")
	if status != 202 {
		t.Fatalf("change_password status = %d", status)
	}
	if op := waitOp(t, m, opID); op.State != "succeeded" {
		t.Fatalf("change_password op = %+v", op)
	}
}

func TestEncryptionValidation(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	cases := []struct {
		name                     string
		action, pw, oldPw, newPw string
		wantStatus               int
	}{
		{"enable-missing-pw", "enable", "", "", "", 422},
		{"disable-missing-pw", "disable", "", "", "", 422},
		{"change-missing-both", "change_password", "", "", "", 422},
		{"unknown-action", "frobnicate", "x", "", "", 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, status, _ := m.Encryption(context.Background(), fakeUDID, c.action, c.pw, c.oldPw, c.newPw); status != c.wantStatus {
				t.Fatalf("%s status = %d (want %d)", c.name, status, c.wantStatus)
			}
		})
	}
}

func TestEncryptionUnknownDevice(t *testing.T) {
	m := newTestManager(t, newFakeDevices(), "DEVICEOPS_FAKE=paired")
	if _, status, _ := m.Encryption(context.Background(), fakeUDID, "enable", "pw", "", ""); status != 404 {
		t.Fatalf("unknown-device encryption status = %d (want 404)", status)
	}
}

func TestEncryptionFailureIsHonest(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=enc_fail")

	opID, _, _ := m.Encryption(context.Background(), fakeUDID, "enable", "s3cret", "", "")
	op := waitOp(t, m, opID)
	if op.State != "failed" || op.Error == nil {
		t.Fatalf("failed encryption op = %+v (want failed + error)", op)
	}
}

func TestValidateEndpoint(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=unpaired")
	paired, status, _ := m.Validate(context.Background(), fakeUDID)
	if status != 200 || paired {
		t.Fatalf("validate = %v status %d (want false, 200)", paired, status)
	}
}
