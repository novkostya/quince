package backup

import (
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/wire"
)

// setTransports sets a fake device present on the chosen transports (the harness's set() only ever
// sets one) — for the qn.4b transport-auto resolution stories.
func setTransports(f *fakeDevices, udid string, usb, wifi bool, enc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := "2026-07-20T00:00:00Z"
	tr := wire.Transports{}
	if usb {
		tr.USB = &now
	}
	if wifi {
		tr.WiFi = &now
	}
	f.devs[udid] = wire.Device{UDID: udid, Name: "test-iphone", Transports: tr, Paired: "yes",
		BackupEncryption: enc, LastSeen: now}
}

// Story 1: transport auto resolves against CURRENT presence and stores the CONCRETE transport on the
// job (never "auto"). With BOTH transports present it prefers USB (design §4/(bp)).
func TestAutoResolvesToUSBWhenBothPresent(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), TransportUSB)
	setTransports(h.dev, testUDID, true, true, "on") // both present → prefer USB
	job := h.start(t, TransportAuto, "")
	if job.Transport != TransportUSB {
		t.Fatalf("auto with both transports resolved to %q, want usb", job.Transport)
	}
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateSucceeded || final.Transport != TransportUSB {
		t.Fatalf("state=%s transport=%s, want succeeded/usb", final.State, final.Transport)
	}
}

// Story 3: a Wi-Fi incremental replays end-to-end to a committed, verified version, AND auto resolves
// to wifi when that is the only present transport. This retires the qn.4a handoff-review coverage
// finding — the Wi-Fi SUCCESS path (transcript wifi-incremental-success) now has a test that fails if
// it breaks.
func TestAutoResolvesToWifiAndWifiSucceeds(t *testing.T) {
	m := loadMeta(t, "wifi-incremental-success")
	h := newHarness(t, m.params(t), TransportWiFi) // present on Wi-Fi only
	job := h.start(t, TransportAuto, "")
	if job.Transport != TransportWiFi {
		t.Fatalf("auto with Wi-Fi only resolved to %q, want wifi", job.Transport)
	}
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s error=%v — Wi-Fi success path", final.State, final.Error)
	}
	if final.VersionID == nil {
		t.Fatal("a Wi-Fi success committed no version")
	}
	vs := h.mgr.Versions(testUDID)
	if len(vs) != 1 || vs[0].Kind != "incremental" || !vs[0].Encrypted {
		t.Fatalf("want 1 encrypted incremental version, got %+v", vs)
	}
}

// Story 2: auto when the device is present on NO transport → actionable 422, and NO job is minted
// (design §4/(bp): a guessed transport would persist a dishonest Job.transport).
func TestAutoWhenAbsentRefusesWithNoJob(t *testing.T) {
	h := newHarness(t, fakeParams{}, TransportUSB)
	h.dev.remove(testUDID)
	_, status, reason := h.eng.StartBackup(testUDID, TransportAuto, "")
	if status != 422 {
		t.Fatalf("auto with an absent device = %d, want 422", status)
	}
	if reason == "" {
		t.Fatal("the 422 must carry an actionable reason")
	}
	if list, _ := h.eng.Jobs(testUDID, "", 10); len(list) != 0 {
		t.Fatalf("auto-absent must mint no job row, got %d", len(list))
	}
}

// Explicit usb|wifi does NOT require presence at Start (the start-then-connect waiting_for_device
// flow is preserved): a job is minted even with the device absent, then it waits.
func TestExplicitTransportDoesNotRequirePresenceAtStart(t *testing.T) {
	h := newHarness(t, fakeParams{}, TransportUSB)
	h.dev.remove(testUDID)
	job, status, reason := h.eng.StartBackup(testUDID, TransportUSB, "")
	if status != 202 {
		t.Fatalf("explicit usb with an absent device = %d (%s), want 202 (it waits)", status, reason)
	}
	// It fails after the wait window (no device appears) — honestly, not a start-time refusal.
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != ErrDeviceNotVisible {
		t.Fatalf("absent explicit-usb job = %s error=%v, want failed/%s", final.State, final.Error, ErrDeviceNotVisible)
	}
}
