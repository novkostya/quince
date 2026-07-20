package deviceops

import (
	"context"
	"testing"

	"github.com/novkostya/quince/core/internal/wire"
)

// TestPairDenied: the user declining the Trust dialog ends the op failed with an honest code.
func TestPairDenied(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=denied")

	opID, status, _ := m.Pair(context.Background(), fakeUDID)
	if status != 202 {
		t.Fatalf("pair start = %d", status)
	}
	op := waitOp(t, m, opID)
	if op.State != "failed" || op.Error == nil || op.Error.Code != "trust_denied" {
		t.Fatalf("denied pair op = %+v (want failed/trust_denied)", op)
	}
}

func TestPairBadUDID(t *testing.T) {
	m := newTestManager(t, newFakeDevices(), "DEVICEOPS_FAKE=paired")
	if _, status, _ := m.Pair(context.Background(), "bad udid; rm -rf /"); status != 400 {
		t.Fatalf("bad-udid pair status = %d (want 400)", status)
	}
}

func TestValidateUnknownDevice(t *testing.T) {
	m := newTestManager(t, newFakeDevices(), "DEVICEOPS_FAKE=paired")
	if _, status, _ := m.Validate(context.Background(), fakeUDID); status != 404 {
		t.Fatalf("validate unknown = %d (want 404)", status)
	}
}

func TestValidateNotConnected(t *testing.T) {
	devs := newFakeDevices()
	devs.add(wire.Device{UDID: fakeUDID}) // present in table but no transport
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")
	if _, status, _ := m.Validate(context.Background(), fakeUDID); status != 409 {
		t.Fatalf("validate not-connected = %d (want 409)", status)
	}
}

// TestEncryptionOverWiFi exercises the Wi-Fi transport path (netmuxd socket + -n): encryption
// is allowed over Wi-Fi (only pairing is USB-only).
func TestEncryptionOverWiFi(t *testing.T) {
	devs := newFakeDevices()
	now := wire.Now()
	devs.add(wire.Device{UDID: fakeUDID, Transports: wire.Transports{WiFi: &now}})
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")

	opID, status, _ := m.Encryption(context.Background(), fakeUDID, "enable", "pw", "", "")
	if status != 202 {
		t.Fatalf("wifi encryption start = %d", status)
	}
	if op := waitOp(t, m, opID); op.State != "succeeded" {
		t.Fatalf("wifi encryption op = %+v", op)
	}
}

func TestEncryptionNotConnected(t *testing.T) {
	devs := newFakeDevices()
	devs.add(wire.Device{UDID: fakeUDID}) // no transport
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=paired")
	if _, status, _ := m.Encryption(context.Background(), fakeUDID, "enable", "pw", "", ""); status != 409 {
		t.Fatalf("encryption not-connected = %d (want 409)", status)
	}
}
