package deviceops

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/novkostya/quince/core/internal/muxaddr"
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
	if got := NewTools(mustEP("/tmp/usbmuxd.sock"), muxaddr.Endpoint{}, nil).wifiSyncKey; got != "EnableWifiConnections" {
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

// The state fix. Disabling over Wi-Fi severs the transport the op runs on, so reEnrich's Info()
// call fails and returns WITHOUT updating — leaving the UI showing `on` for a device that is now
// off and gone. Observed on hardware 2026-07-31.
//
// The op must publish the value SetWifiSync already READ BACK, which is verified rather than
// assumed: SetWifiSync only returns nil after confirming the flag changed on the device.
//
// It asserts by REFLECTION rather than by naming fields, because Enrich replaces the stored
// identity wholesale: a field left out of runWifiSync's literal is not "unchanged", it is published
// as empty and persisted. The hand-written version of this test named three of six fields, and
// dropping `Model: dev.Model` left the whole package green — measured in review. Reflection also
// keeps working when Identity grows, which is the case a hand-written list cannot cover at all.
func TestWifiSyncOpPublishesTheVerifiedStateEvenWhenTheDeviceVanishes(t *testing.T) {
	devs := newFakeDevices()
	dev := pairedUSBDevice(fakeUDID)
	// Every field distinct, so a mix-up between two of them is caught as well as a blanking.
	dev.Name, dev.Model, dev.IOSVersion = "dev-name", "dev-model", "dev-ios"
	dev.BackupEncryption, dev.WifiSync = "on", "on"
	devs.add(dev)
	m := newWifiManager(t, devs, "wifi_on")

	opID, status, reason := m.WifiSync(context.Background(), fakeUDID, "disable")
	if status != http.StatusAccepted {
		t.Fatalf("status = %d (%s), want 202", status, reason)
	}
	if op := waitOp(t, m, opID); op.State != "succeeded" {
		t.Fatalf("op = %+v, want succeeded", op)
	}

	got, ok := devs.lastEnrich(fakeUDID)
	if !ok {
		t.Fatal("no identity was published at all — the stale-badge bug this test exists for")
	}

	gv := reflect.ValueOf(got)
	src := reflect.ValueOf(dev)
	for i := 0; i < gv.NumField(); i++ {
		name := gv.Type().Field(i).Name
		if name == "WifiSync" {
			// The one field the op is allowed to change, and the reason it ran.
			if gv.Field(i).String() != "off" {
				t.Errorf("WifiSync = %q after a successful disable, want off — a stale `on` is what the UI showed on hardware", gv.Field(i).String())
			}
			continue
		}
		// Identity's other fields are carried across from the device the registry already holds, so
		// each must equal its same-named counterpart on wire.Device.
		want := src.FieldByName(name)
		if !want.IsValid() {
			t.Fatalf("Identity.%s has no counterpart on wire.Device — a new field needs a decision "+
				"about how runWifiSync carries it, not a silent empty string", name)
		}
		// DeepEqual over .Interface(), never .String(): reflect.Value.String() is a string-KIND
		// accessor that returns a placeholder like "<int Value>" for anything else, so two DIFFERENT
		// ints compare equal and the field passes unwitnessed. Identity is all strings today, which
		// is precisely why this would have gone unnoticed until the guard was needed most — the
		// first non-string field added to it, which is the case the comment above promises to cover.
		if !reflect.DeepEqual(gv.Field(i).Interface(), want.Interface()) {
			t.Errorf("Identity.%s = %v, want %v — Enrich REPLACES the identity, so a field missing "+
				"from runWifiSync's literal is published empty", name, gv.Field(i).Interface(), want.Interface())
		}
	}
}

// THE OP MUST PUBLISH BEFORE IT ANNOUNCES, and the order is a contract rather than an implementation
// detail (quince#529). `succeeded` is what a client polls for, and it re-reads the device the moment
// it sees one — so announcing first hands that client the stale badge this whole path exists to
// retract, which is the symptom that took three hardware attempts to diagnose (quince#325/#363/#366).
//
// It is also what made TestWifiSyncOpPublishesTheVerifiedStateEvenWhenTheDeviceVanishes flaky on
// branches with no Go changes, up to reddening `main` at 1cfd50d: `waitOp` returns the instant the op
// reads `succeeded`, so that test read the registry inside this window.
//
// DETERMINISTIC WHERE THAT TEST IS NOT. It does not race the two events and hope: it HOLDS THE
// PUBLISH OPEN and asks what the op has already said. Under the old ordering the answer is
// `succeeded` every time, not one run in thirty.
func TestWifiSyncPublishesBeforeItAnnouncesSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, scenario, action string
		dev                    wire.Device
	}{
		// runWifiSync's own publish: the value SetWifiSync read back and verified.
		{"a verified disable", "wifi_on", "disable", pairedUSBDevice(fakeUDID)},
		// wifiSyncDisableUnreadable's `unknown` — the publish that RETRACTS a stale `on`, so
		// announcing before it is the worst version of this bug.
		{"a disable whose read-back is severed", "wifi_set_then_unreadable", "disable", pairedWiFiDevice(fakeUDID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			devs := newFakeDevices()
			d := tc.dev
			d.WifiSync = "on"
			devs.add(d)
			m := newWifiManager(t, devs, tc.scenario)

			// `once`, because a USB op also calls reEnrich afterwards and only the FIRST publish is
			// the one racing the announcement. Set before the op starts — see fakeDevices.onEnrich.
			var once sync.Once
			entered, release := make(chan struct{}), make(chan struct{})
			devs.onEnrich = func(string) {
				once.Do(func() {
					close(entered)
					<-release
				})
			}

			opID, status, reason := m.WifiSync(context.Background(), fakeUDID, tc.action)
			if status != http.StatusAccepted {
				t.Fatalf("status = %d (%s), want 202", status, reason)
			}

			<-entered // the publish is in flight and held open
			if op, ok := m.Op(opID); ok && op.State == "succeeded" {
				t.Fatal("the op reported SUCCEEDED while its publish was still in flight — a client " +
					"that polls for success and re-reads the device gets the stale badge this path " +
					"exists to retract (quince#529)")
			}
			close(release)

			if op := waitOp(t, m, opID); op.State != "succeeded" {
				t.Fatalf("op = %+v, want succeeded", op)
			}
			// And the publish is not merely first, it is DONE: the whole point is that a client
			// which sees `succeeded` can read the new value.
			if _, ok := devs.lastEnrich(fakeUDID); !ok {
				t.Fatal("nothing was published even after the op succeeded")
			}
		})
	}
}

// wifiDevice is the transport that matters for the disable exemption: Wi-Fi and NOT USB. A device on
// both would run the op over USB (opTransport prefers it), where nothing is severed.
func pairedWiFiDevice(udid string) wire.Device {
	now := wire.Now()
	d := wire.Device{UDID: udid, Transports: wire.Transports{WiFi: &now}}
	d.Paired = "yes"
	return d
}

// THE RULED CASE (quince#363). Disabling over Wi-Fi severs the connection the read-back would use,
// so the verification cannot run — success and unverifiability are the same event. Reporting failure
// told the Operator "the device accepted the change but did not apply it; Wi-Fi sync is unchanged"
// about a device that HAD changed, had left Wi-Fi, and needed a cable. Every clause was false.
func TestWifiSyncDisableOverWifiSucceedsWhenTheReadBackCannotRun(t *testing.T) {
	devs := newFakeDevices()
	dev := pairedWiFiDevice(fakeUDID)
	dev.Name, dev.Model, dev.IOSVersion = "dev-name", "dev-model", "dev-ios"
	dev.BackupEncryption, dev.WifiSync = "on", "on"
	devs.add(dev)
	m := newWifiManager(t, devs, "wifi_set_then_unreadable")

	opID, status, reason := m.WifiSync(context.Background(), fakeUDID, "disable")
	if status != http.StatusAccepted {
		t.Fatalf("status = %d (%s), want 202", status, reason)
	}
	op := waitOp(t, m, opID)

	if op.State != "succeeded" {
		t.Fatalf("op.State = %q, want succeeded — the write landed; only the verification could not run", op.State)
	}
	if op.Error != nil {
		t.Fatalf("op.Error = %+v, want nil", op.Error)
	}
	// The message must not assert a value quince never read, and must name the remedy.
	if !strings.Contains(op.Message, "cable") {
		t.Fatalf("message must name the cable as the remedy, got %q", op.Message)
	}
	if strings.Contains(op.Message, "unchanged") {
		t.Fatalf("message must not claim the setting is unchanged, got %q", op.Message)
	}

	// `unknown`, NEVER an inferred `off`: nothing read the flag, and a wrong `off` would PERSIST
	// through Enrich into SQLite as a confident value. `unknown` self-heals on the next USB read.
	got, ok := devs.lastEnrich(fakeUDID)
	if !ok {
		t.Fatal("nothing was published — the badge would keep showing the stale `on`")
	}
	if got.WifiSync != "unknown" {
		t.Fatalf("published wifi_sync = %q, want %q — an inferred value is a claim nobody verified", got.WifiSync, "unknown")
	}
	// The rest of the identity must survive: Enrich replaces it wholesale.
	if got.Name != "dev-name" || got.Model != "dev-model" || got.BackupEncryption != "on" {
		t.Fatalf("publishing the unknown blanked the identity: %+v", got)
	}
}

// THE EXEMPTION IS NARROW, and this is the half that keeps it honest. Each clause of the conjunction
// is dropped in turn; every one of them must still FAIL. Without this, "read-back failed" would
// become a blanket excuse and a genuinely broken write on any other path would report success.
func TestWifiSyncUnreadableIsOnlyForgivenOnTheDisableOverWifiPath(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		dev          wire.Device
	}{
		// Enabling does not sever anything, so an unreadable read-back has no causal story.
		{"enable over wifi", "enable", pairedWiFiDevice(fakeUDID)},
		// Over USB the connection survives the write, so the read-back should have worked.
		{"disable over usb", "disable", pairedUSBDevice(fakeUDID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			devs := newFakeDevices()
			d := tc.dev
			d.WifiSync = "on"
			devs.add(d)
			m := newWifiManager(t, devs, "wifi_set_then_unreadable")

			opID, status, _ := m.WifiSync(context.Background(), fakeUDID, tc.action)
			if status != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", status)
			}
			op := waitOp(t, m, opID)
			if op.State != "failed" {
				t.Fatalf("op.State = %q, want failed — only disable-over-Wi-Fi explains an unreadable read-back", op.State)
			}
			// And it carries its OWN code. `wifi_sync_failed` means the device REJECTED the write
			// and is retryable; this is the opposite — accepted, unverifiable — so anything reading
			// the generic code would draw the wrong remedy. `not_applied` is wrong for the other
			// direction: it asserts the state is UNCHANGED, which a failed read cannot establish.
			if op.Error == nil || op.Error.Code != "wifi_sync_unconfirmed" {
				t.Fatalf("op.Error = %+v, want wifi_sync_unconfirmed", op.Error)
			}
		})
	}
}

// A read-back that SUCCEEDS and reports the old value is a genuine lying write and keeps its own
// error. Pinned beside the exemption because the two are one line apart in the manager, and the
// whole point of quince#363 is that they had been conflated.
func TestWifiSyncStillFailsWhenTheDeviceReadsBackUnchanged(t *testing.T) {
	devs := newFakeDevices()
	devs.add(pairedWiFiDevice(fakeUDID))
	m := newWifiManager(t, devs, "wifi_disable_lies")

	opID, _, _ := m.WifiSync(context.Background(), fakeUDID, "disable")
	op := waitOp(t, m, opID)

	if op.State != "failed" {
		t.Fatalf("op.State = %q, want failed — the device read back and had not changed", op.State)
	}
	if op.Error == nil || op.Error.Code != "wifi_sync_not_applied" {
		t.Fatalf("op.Error = %+v, want wifi_sync_not_applied", op.Error)
	}
}
