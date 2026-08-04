package backup

import (
	"sync"
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
	// qn.5b / finding #9(a): kind is AUTHORITATIVE from the seed decision, not the transcript's
	// IsFullBackup. This is a FIRST backup (no prior latest/ to seed from) → honestly "full", even
	// though the transcript is an "incremental" TRANSFER — every quince version is complete.
	if len(vs) != 1 || vs[0].Kind != "full" || !vs[0].Encrypted {
		t.Fatalf("want 1 encrypted full version (first backup, seed-derived kind), got %+v", vs)
	}
}

// Story 2: auto when the device is present on NO transport → actionable 422, and NO job is minted
// (design §4/(bp): a guessed transport would persist a dishonest Job.transport).
func TestAutoWhenAbsentRefusesWithNoJob(t *testing.T) {
	h := newHarness(t, fakeParams{}, TransportUSB)
	h.dev.remove(testUDID)
	_, status, reason := h.eng.StartBackup(testUDID, TransportAuto, "", "")
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
	job, status, reason := h.eng.StartBackup(testUDID, TransportUSB, "", "")
	if status != 202 {
		t.Fatalf("explicit usb with an absent device = %d (%s), want 202 (it waits)", status, reason)
	}
	// It fails after the wait window (no device appears) — honestly, not a start-time refusal.
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != ErrDeviceNotVisible {
		t.Fatalf("absent explicit-usb job = %s error=%v, want failed/%s", final.State, final.Error, ErrDeviceNotVisible)
	}
}

// quince#654 — `backup.preferred_transport` DECIDES WHICH TRANSPORT `auto` PICKS WHEN BOTH ARE
// PRESENT, and is IGNORED everywhere else.
//
// The key it replaces, `backup.transport`, was validated, documented, editable in Settings and read
// by nobody — so setting it to `usb` still backed up over Wi-Fi. These pin the four rows of the
// ruled resolution table (design §4), and the one that matters most is the third: a device on ONE
// transport is backed up over that one WHATEVER the preference says. A preference that could
// restrict would make a Wi-Fi-only device silently unbackupable through a setting whose name does
// not say so — and Wi-Fi is the primary transport under the assisted model.
//
// TestAutoResolvesToUSBWhenBothPresent above is the behaviour-preservation half and is deliberately
// left untouched: `testCfg()` sets no preference, an empty preference reads as `usb`, and that test
// still passes — which is the assertion that this change altered nothing for a config built before
// the key existed.

func TestPreferredTransportWifiWinsWhenBothPresent(t *testing.T) {
	m := loadMeta(t, "wifi-incremental-success")
	h := newHarness(t, m.params(t), TransportWiFi, func(o *Options, _ *fakeDevices) {
		o.Config.PreferredTransport = TransportWiFi
	})
	setTransports(h.dev, testUDID, true, true, "on") // BOTH present — the only case the key decides

	job := h.start(t, TransportAuto, "")
	if job.Transport != TransportWiFi {
		t.Fatalf("auto with both present and preferred_transport=wifi resolved to %q, want wifi — "+
			"the preference is the whole point of the key", job.Transport)
	}
}

// THE ROW THAT MAKES IT A PREFERENCE RATHER THAN A RESTRICTION. Preference usb, device on Wi-Fi
// only: it backs up over Wi-Fi. If this ever returns usb (or refuses), a user who set `usb` has
// silently lost the ability to back up every Wi-Fi-only device they own.
func TestPreferredTransportIsIgnoredWhenOnlyTheOtherIsPresent(t *testing.T) {
	m := loadMeta(t, "wifi-incremental-success")
	h := newHarness(t, m.params(t), TransportWiFi, func(o *Options, _ *fakeDevices) {
		o.Config.PreferredTransport = TransportUSB
	})
	setTransports(h.dev, testUDID, false, true, "on") // Wi-Fi only

	job := h.start(t, TransportAuto, "")
	if job.Transport != TransportWiFi {
		t.Fatalf("preferred_transport=usb on a Wi-Fi-ONLY device resolved to %q, want wifi — a "+
			"preference must never restrict, or this device becomes unbackupable", job.Transport)
	}
}

// The mirror of the above, so the asymmetry cannot be introduced on one side only.
func TestPreferredWifiIsIgnoredOnAUSBOnlyDevice(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), TransportUSB, func(o *Options, _ *fakeDevices) {
		o.Config.PreferredTransport = TransportWiFi
	})
	setTransports(h.dev, testUDID, true, false, "on") // USB only

	job := h.start(t, TransportAuto, "")
	if job.Transport != TransportUSB {
		t.Fatalf("preferred_transport=wifi on a USB-ONLY device resolved to %q, want usb", job.Transport)
	}
}

// A CONCRETE REQUEST OUTRANKS THE PREFERENCE — row 1 of the table. The key answers "which one when
// there is a choice", and an explicit request has already made that choice.
func TestAConcreteRequestOutranksThePreference(t *testing.T) {
	m := loadMeta(t, "wifi-incremental-success")
	h := newHarness(t, m.params(t), TransportWiFi, func(o *Options, _ *fakeDevices) {
		o.Config.PreferredTransport = TransportUSB
	})
	setTransports(h.dev, testUDID, true, true, "on")

	job := h.start(t, TransportWiFi, "")
	if job.Transport != TransportWiFi {
		t.Fatalf("an explicit wifi request under preferred_transport=usb resolved to %q, want wifi",
			job.Transport)
	}
}

// An UNSET preference reads as usb, so every Config built by hand — the tests, both CLIs — keeps the
// behaviour it had before the key existed. Asserted rather than left to the zero value's good
// manners, because the alternative reading ("" is neither, so refuse) would break every one of them.
func TestAnUnsetPreferenceReadsAsUSB(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), TransportUSB, func(o *Options, _ *fakeDevices) {
		o.Config.PreferredTransport = ""
	})
	setTransports(h.dev, testUDID, true, true, "on")

	job := h.start(t, TransportAuto, "")
	if job.Transport != TransportUSB {
		t.Fatalf("an unset preference resolved to %q, want usb — an empty value must preserve the "+
			"pre-quince#654 behaviour", job.Transport)
	}
}

// qn.6g PR 5 — THE `backup:` SETTINGS APPLY WITHOUT A RESTART, and this is the consumer that proves
// the seam is GENERAL rather than a storage hook wearing a general name: a different package, a
// different lock, a different shape of state.
//
// There is no config.Service here — that wiring is asserted in cmd/quince. What these pin is the
// Engine's half of the contract: SetLiveConfig changes what the NEXT job sees, and a job already
// past the relevant decision keeps the answer it got.

func TestSetLiveConfigChangesThePreferenceForTheNextJob(t *testing.T) {
	m := loadMeta(t, "wifi-incremental-success")
	h := newHarness(t, m.params(t), TransportWiFi, func(o *Options, _ *fakeDevices) {
		o.Config.PreferredTransport = TransportUSB
	})
	setTransports(h.dev, testUDID, true, true, "on") // both present — the only case the key decides

	// Before: the configured preference wins.
	job := h.start(t, TransportAuto, "")
	if job.Transport != TransportUSB {
		t.Fatalf("setup: auto resolved to %q, want usb", job.Transport)
	}
	h.drain(t)

	// A config write lands. No restart.
	h.eng.SetLiveConfig(true, TransportWiFi)

	job2 := h.start(t, TransportAuto, "")
	if job2.Transport != TransportWiFi {
		t.Errorf("after SetLiveConfig the next job resolved to %q, want wifi — the setting did not "+
			"reach the engine, which is the whole defect this rung closes", job2.Transport)
	}
}

// require_encryption, the other half. Turning it ON must stop an unencrypted device's next backup.
//
// ASSERTED AT THE JOB'S TERMINAL STATE, NOT AT StartBackup — and that correction is the test
// teaching me where the check is. `require_encryption` is enforced in PREFLIGHT, which runs inside
// the job goroutine, so `StartBackup` returns 202 either way and the job fails afterwards with
// `encryption_required`. Two earlier versions of this test asserted on the status code and were
// meaningless: the first compared against `status == 0`, which is a value StartBackup never
// returns, so it could not fail at all.
func TestSetLiveConfigTurnsEncryptionEnforcementOnForTheNextJob(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), TransportUSB, func(o *Options, _ *fakeDevices) {
		o.Config.RequireEncryption = false
	})
	setTransports(h.dev, testUDID, true, false, "off") // unencrypted device

	// Off: the backup runs to success.
	first := h.start(t, TransportUSB, "")
	if got := waitTerminal(t, h.eng, first.ID, 10*time.Second); got.State != StateSucceeded {
		t.Fatalf("setup: with require_encryption off the job ended %s (%v), want succeeded",
			got.State, got.Error)
	}

	// The user turns it on. No restart.
	h.eng.SetLiveConfig(true, TransportUSB)

	second := h.start(t, TransportUSB, "")
	final := waitTerminal(t, h.eng, second.ID, 10*time.Second)
	if final.State == StateSucceeded {
		t.Fatalf("after turning require_encryption ON the next backup still succeeded — the setting " +
			"did not reach preflight, which is the whole defect this rung closes")
	}
	if final.Error == nil || final.Error.Code != ErrEncryptionRequired {
		t.Errorf("the job failed with %v, want %s — it must refuse for the RIGHT reason, or this "+
			"test would pass on any failure at all", final.Error, ErrEncryptionRequired)
	}
}

// AND THE REVERSE, because a one-directional test would pass on an implementation that only ever
// tightened. Turning it OFF must let an unencrypted device through.
func TestSetLiveConfigTurnsEncryptionEnforcementOffForTheNextJob(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), TransportUSB, func(o *Options, _ *fakeDevices) {
		o.Config.RequireEncryption = true
	})
	setTransports(h.dev, testUDID, true, false, "off")

	first := h.start(t, TransportUSB, "")
	blocked := waitTerminal(t, h.eng, first.ID, 10*time.Second)
	if blocked.Error == nil || blocked.Error.Code != ErrEncryptionRequired {
		t.Fatalf("setup: an unencrypted device should fail with %s while require_encryption is on, "+
			"got state=%s error=%v", ErrEncryptionRequired, blocked.State, blocked.Error)
	}

	h.eng.SetLiveConfig(false, TransportUSB)

	second := h.start(t, TransportUSB, "")
	final := waitTerminal(t, h.eng, second.ID, 10*time.Second)
	if final.State != StateSucceeded {
		t.Errorf("after turning require_encryption OFF the backup still ended %s (%v), want succeeded",
			final.State, final.Error)
	}
}

// CONCURRENT READS AND WRITES. The engine reads these per job from its own goroutines while the
// config applier writes them from an HTTP handler goroutine — the same shape as storage's slot list,
// and the reason this is a lock rather than a plain field on `cfg`. Under `-race` this is the
// assertion.
func TestLiveConfigIsSafeUnderConcurrentReadsAndWrites(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), TransportUSB)
	setTransports(h.dev, testUDID, true, true, "on")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				h.eng.SetLiveConfig(true, TransportWiFi)
			} else {
				h.eng.SetLiveConfig(false, TransportUSB)
			}
		}
	}()
	for i := 0; i < 3000; i++ {
		_, _, _ = h.eng.resolveTransport(testUDID, TransportAuto)
	}
	close(stop)
	wg.Wait()
}
