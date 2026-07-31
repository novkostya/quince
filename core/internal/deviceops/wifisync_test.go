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

// The load-bearing one: with no measured key, quince must NOT ask the device anything, because
// `ideviceinfo -k <wrong-key>` is expected to exit 0 printing nothing — which scalarTriState reads
// as "off". A guessed key would therefore make quince assert that every device has Wi-Fi sync
// disabled. This is the test that stops a future session "finishing" the feature by filling in a
// plausible constant.
func TestWifiSyncUnmeasuredKeyReadsUnknownWithoutQueryingTheDevice(t *testing.T) {
	if wifiSyncKeyUnmeasured != "" {
		t.Fatalf("wifiSyncKeyUnmeasured is %q — it must ship empty until qn.7 story 3 measures it on hardware", wifiSyncKeyUnmeasured)
	}
	// The scenario would answer "on" if a query were made at all, so a regression that starts
	// querying shows up as "on" rather than as a silent pass.
	tools := fakeTools("DEVICEOPS_FAKE=wifi_on")
	if got := tools.wifiSync(context.Background(), fakeUDID, TransportUSB); got != "unknown" {
		t.Fatalf("wifiSync with no key = %q, want unknown", got)
	}
}

// NewTools must carry the unmeasured key into production, not leave it to a caller to remember.
func TestNewToolsShipsWithNoWifiSyncKey(t *testing.T) {
	if got := NewTools("/tmp/usbmuxd.sock", "", nil).wifiSyncKey; got != "" {
		t.Fatalf("NewTools wifiSyncKey = %q, want empty", got)
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
