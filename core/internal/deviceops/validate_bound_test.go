package deviceops

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestValidateBoundedGoSide is qn.6b amendment A (story 8): the non-interactive validate read must
// be cut by the Go-side deviceOpTimeout, NOT inherit the patched 15-min libimobiledevice receive
// timeout. The fake validate blocks 15s; with a 100ms bound, Validate must return promptly with a
// bad-gateway, and — critically — NOT wait out the block. If the ctx were passed through unbounded,
// this returns only after 15s (and, with the real patched build, up to 15min).
func TestValidateBoundedGoSide(t *testing.T) {
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=slow")
	m.validateTimeout = 100 * time.Millisecond

	start := time.Now()
	paired, status, _ := m.Validate(context.Background(), fakeUDID)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("Validate took %s on a wedged read — the tool call is not bounded Go-side (amendment A)", elapsed)
	}
	if paired {
		t.Errorf("wedged validate reported paired=true; want false")
	}
	if status != http.StatusBadGateway {
		t.Errorf("wedged validate status = %d; want %d (could not query the device)", status, http.StatusBadGateway)
	}
}

// TestValidateDefaultBoundIsConfigured guards that the production manager actually carries the
// deviceOpTimeout bound (a struct-level check complementing the behavioural one), and that it stays
// comfortably under the tool's 15-min patience.
func TestValidateDefaultBoundIsConfigured(t *testing.T) {
	m := newTestManager(t, newFakeDevices())
	if m.validateTimeout <= 0 {
		t.Fatalf("validateTimeout not configured (%s) — the validate read would be unbounded", m.validateTimeout)
	}
	if m.validateTimeout > time.Minute {
		t.Errorf("validateTimeout = %s, want <= 60s (a non-interactive read must fast-fail)", m.validateTimeout)
	}
}
