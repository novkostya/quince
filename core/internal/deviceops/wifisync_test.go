package deviceops

import (
	"context"
	"testing"
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
