package deviceops

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/wire"
)

// withKey sets the lockdown key on ONE Tools value. Deliberately per-instance rather than a
// package-level override: enrichment reads this field from a background goroutine, so a mutable
// package var would be a data race — and `go test -race` reported it as one before this moved onto
// Tools. A test helper that reintroduces shared state would put the race straight back.
func withKey(t *Tools, key string) *Tools {
	t.wifiSyncKey = key
	return t
}

const syntheticWifiKey = "SyntheticWifiSyncKey"

// The key is now MEASURED (qn.7 story 3, 2026-07-31), so this pins the measured value rather than
// the empty string it used to pin. It is not a tautology: it fails if someone edits the constant
// without a hardware measurement to justify it, which is the same guard as before pointed the other
// way. The value came from dumping com.apple.mobile.wireless_lockdown on a real device.
func TestWifiSyncKeyIsTheMeasuredOne(t *testing.T) {
	if wifiSyncKey != "EnableWifiConnections" {
		t.Fatalf("wifiSyncKey = %q — story 3 measured EnableWifiConnections on hardware; changing it needs another measurement, not a guess", wifiSyncKey)
	}
}

// The empty-key branch is STILL the honest answer when quince does not know the key, and it stays
// reachable and tested even though production no longer takes it. Deleting it because the constant
// is now populated would remove the behaviour that made shipping the unmeasured state safe — and
// the same situation recurs the moment anyone reads a second, unmeasured lockdown value.
func TestWifiSyncWithNoKeyReadsUnknownWithoutQueryingTheDevice(t *testing.T) {
	// The scenario would answer "on" if a query were made at all, so a regression that starts
	// querying shows up as "on" rather than as a silent pass.
	tools := withKey(fakeTools("DEVICEOPS_FAKE=wifi_on"), "")
	if got := tools.wifiSync(context.Background(), fakeUDID, TransportUSB); got != "unknown" {
		t.Fatalf("wifiSync with no key = %q, want unknown", got)
	}
}

// NewTools must carry the measured key into production, not leave it to a caller to remember.
func TestNewToolsCarriesTheMeasuredWifiSyncKey(t *testing.T) {
	if got := NewTools("/tmp/usbmuxd.sock", "", nil).wifiSyncKey; got != "EnableWifiConnections" {
		t.Fatalf("NewTools wifiSyncKey = %q, want EnableWifiConnections", got)
	}
}

func TestWifiSyncTriState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		want     string
	}{
		{"enabled", "wifi_on", "on"},
		{"disabled", "wifi_off", "off"},
		// The qn.4a finding (i)-A rule, carried across: an ABSENT key is the device saying "no",
		// not "I don't know". Getting this wrong on encryption shipped a real bug.
		{"never set is off, not unknown", "wifi_never_set", "off"},
		{"a failed read is unknown", "wifi_read_failed", "unknown"},
		{"an unparseable value is unknown", "wifi_garbage", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := withKey(fakeTools("DEVICEOPS_FAKE="+tc.scenario), syntheticWifiKey)
			if got := tools.wifiSync(context.Background(), fakeUDID, TransportUSB); got != tc.want {
				t.Fatalf("wifiSync = %q, want %q", got, tc.want)
			}
		})
	}
}

// The fake used to dispatch a scalar read on the bare presence of -k, so a second domain would
// have been answered by fakeWillEncrypt — a green test exercising the wrong code path. This pins
// the narrowing: the two domains must give independent answers in the same run.
func TestScalarReadsDispatchByDomainNotByFlag(t *testing.T) {
	// One scenario name cannot mean two things, so pick one that would collide under the old
	// dispatch: enc_off makes willEncrypt "off" while the wifi fake falls through to "on".
	tools := withKey(fakeTools("DEVICEOPS_FAKE=enc_off"), syntheticWifiKey)
	ctx := context.Background()

	if got := tools.willEncrypt(ctx, fakeUDID, TransportUSB); got != "off" {
		t.Fatalf("willEncrypt = %q, want off", got)
	}
	if got := tools.wifiSync(ctx, fakeUDID, TransportUSB); got != "on" {
		t.Fatalf("wifiSync = %q, want on — the wifi read was answered by the encryption fake", got)
	}
}

// Info must not query Wi-Fi sync on a device that is not confirmed paired, for the same reason it
// does not query encryption there: the read needs a trusted session, and attempting one in the
// background is how an unexpected Trust prompt reaches a user who asked for nothing.
func TestInfoLeavesWifiSyncUnknownWhenNotPaired(t *testing.T) {
	tools := withKey(fakeTools("DEVICEOPS_FAKE=unpaired"), syntheticWifiKey)
	id, err := tools.Info(context.Background(), fakeUDID, TransportUSB)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if id.WifiSync != "" {
		t.Fatalf("WifiSync = %q on an unpaired device, want the undetermined empty string", id.WifiSync)
	}
}

func TestInfoCarriesWifiSyncWhenPaired(t *testing.T) {
	tools := withKey(fakeTools("DEVICEOPS_FAKE=wifi_off"), syntheticWifiKey)
	id, err := tools.Info(context.Background(), fakeUDID, TransportUSB)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if id.WifiSync != "off" {
		t.Fatalf("WifiSync = %q, want off", id.WifiSync)
	}
}

// --- the write (story 4 wrapper + story 7 verification) ---

// setTools builds Tools with the measured key and a state file, so a write and the read-back that
// follows it see the same device.
func setTools(t *testing.T, scenario string) *Tools {
	t.Helper()
	state := filepath.Join(t.TempDir(), "wifi-state")
	return withKey(fakeTools("DEVICEOPS_FAKE="+scenario, "DEVICEOPS_WIFI_STATE="+state), syntheticWifiKey)
}

func TestSetWifiSyncRoundTrips(t *testing.T) {
	for _, enable := range []bool{true, false} {
		tools := setTools(t, "wifi_off")
		if err := tools.SetWifiSync(context.Background(), fakeUDID, TransportUSB, enable); err != nil {
			t.Fatalf("SetWifiSync(%v): %v", enable, err)
		}
		want := "off"
		if enable {
			want = "on"
		}
		if got := tools.wifiSync(context.Background(), fakeUDID, TransportUSB); got != want {
			t.Fatalf("after SetWifiSync(%v) the device reads %q, want %q", enable, got, want)
		}
	}
}

// THE STORY-7 CASE, and the reason SetWifiSync re-reads at all: the tool exits 0 and the device
// does not change. Without the read-back quince would report "Wi-Fi sync is on" on the strength of
// having asked — the exact class of quince#313, where a component announced a state it had never
// established.
func TestSetWifiSyncDetectsASilentlyIgnoredWrite(t *testing.T) {
	// ENABLE against a device stuck at `false`. The direction is deliberate: this test first
	// disabled, which passes even against a fake that reports a constant `true` — a test that only
	// fails when it happens to write the opposite of the default is passing by luck, and the
	// manager-level version of it caught that.
	tools := setTools(t, "wifi_set_lies")
	err := tools.SetWifiSync(context.Background(), fakeUDID, TransportUSB, true)
	if err == nil {
		t.Fatal("SetWifiSync returned nil for a write the device ignored — it must not trust the exit code")
	}
	if !errors.Is(err, ErrWifiSyncNotApplied) {
		t.Fatalf("error = %v, want ErrWifiSyncNotApplied so the caller can tell it from a retryable failure", err)
	}
}

func TestSetWifiSyncSurfacesARejectedWrite(t *testing.T) {
	tools := setTools(t, "wifi_set_rejected")
	err := tools.SetWifiSync(context.Background(), fakeUDID, TransportUSB, true)
	if err == nil {
		t.Fatal("SetWifiSync returned nil for a rejected write")
	}
	if errors.Is(err, ErrWifiSyncNotApplied) {
		t.Fatalf("a REJECTED write must not be reported as not-applied: %v", err)
	}
}

// Refusing to write an unmeasured key is a distinct error, because the remedy is a hardware
// measurement rather than a retry — and a caller that cannot tell those apart retries forever.
func TestSetWifiSyncRefusesWithoutAMeasuredKey(t *testing.T) {
	tools := withKey(fakeTools("DEVICEOPS_FAKE=wifi_on"), "")
	err := tools.SetWifiSync(context.Background(), fakeUDID, TransportUSB, true)
	if !errors.Is(err, ErrWifiSyncUnverifiable) {
		t.Fatalf("error = %v, want ErrWifiSyncUnverifiable", err)
	}
}

// --- the op (story 5) ---

// pairedUSBDevice is what the WifiSync ladder requires: a lockdown write needs a trusted session,
// so an unpaired device is refused up front rather than left to fail deeper in.
func pairedUSBDevice(udid string) wire.Device {
	d := usbDevice(udid)
	d.Paired = "yes"
	return d
}

func newWifiManager(t *testing.T, devs Devices, scenario string) *Manager {
	t.Helper()
	state := filepath.Join(t.TempDir(), "wifi-state")
	m := newTestManager(t, devs, "DEVICEOPS_FAKE="+scenario, "DEVICEOPS_WIFI_STATE="+state)
	m.tools.wifiSyncKey = syntheticWifiKey
	return m
}

func TestWifiSyncOpSucceedsAndReEnriches(t *testing.T) {
	devs := newFakeDevices()
	devs.add(pairedUSBDevice(fakeUDID))
	m := newWifiManager(t, devs, "wifi_off")

	opID, status, reason := m.WifiSync(context.Background(), fakeUDID, "enable")
	if status != http.StatusAccepted {
		t.Fatalf("status = %d (%s), want 202", status, reason)
	}
	op := waitOp(t, m, opID)
	if op.State != "succeeded" {
		t.Fatalf("op = %+v, want succeeded", op)
	}
	if op.Kind != "wifi_sync" {
		t.Fatalf("op.Kind = %q, want wifi_sync", op.Kind)
	}
}

func TestWifiSyncOpValidation(t *testing.T) {
	devs := newFakeDevices()
	devs.add(pairedUSBDevice(fakeUDID))
	unpaired := "SYNTHETIC-UDID-AAAA-0002"
	devs.add(usbDevice(unpaired)) // Paired defaults to "" — not a confirmation
	m := newWifiManager(t, devs, "wifi_off")

	for _, tc := range []struct {
		name, udid, action string
		want               int
	}{
		{"bad udid", "!!", "enable", http.StatusBadRequest},
		{"unknown device", "SYNTHETIC-UDID-AAAA-0009", "enable", http.StatusNotFound},
		{"not paired", unpaired, "enable", http.StatusConflict},
		{"unknown action", fakeUDID, "toggle", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, got, _ := m.WifiSync(context.Background(), tc.udid, tc.action); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

// The three failures must be distinguishable in the op's error CODE, not just its prose, because
// only one of them ("failed") is worth retrying: an unmeasured key needs a hardware session, and a
// silently-ignored write means the device declined and the state is unchanged.
func TestWifiSyncOpDistinguishesItsFailures(t *testing.T) {
	for _, tc := range []struct {
		name, scenario, key, wantCode string
	}{
		{"device ignored the write", "wifi_set_lies", syntheticWifiKey, "wifi_sync_not_applied"},
		{"device rejected the write", "wifi_set_rejected", syntheticWifiKey, "wifi_sync_failed"},
		{"key was never measured", "wifi_off", "", "wifi_sync_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			devs := newFakeDevices()
			devs.add(pairedUSBDevice(fakeUDID))
			m := newWifiManager(t, devs, tc.scenario)
			m.tools.wifiSyncKey = tc.key

			opID, status, _ := m.WifiSync(context.Background(), fakeUDID, "enable")
			if status != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", status)
			}
			op := waitOp(t, m, opID)
			if op.State != "failed" {
				t.Fatalf("op.State = %q, want failed", op.State)
			}
			if op.Error == nil || op.Error.Code != tc.wantCode {
				t.Fatalf("op.Error = %+v, want code %q", op.Error, tc.wantCode)
			}
		})
	}
}
