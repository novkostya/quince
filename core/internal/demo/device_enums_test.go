package demo

import (
	"io"
	"log/slog"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/wire"
)

// The contract enums (contracts §2). "" is in none of them, which is the whole point: it is what
// Go hands you for a field you forgot, and it is indistinguishable at the type level from a value
// somebody meant.
var deviceEnums = []struct {
	field   string
	get     func(wire.Device) string
	allowed []string
}{
	{"paired", func(d wire.Device) string { return d.Paired }, []string{"yes", "no", "unknown"}},
	{"backup_encryption", func(d wire.Device) string { return d.BackupEncryption }, []string{"on", "off", "unknown"}},
	{"wifi_sync", func(d wire.Device) string { return d.WifiSync }, []string{"on", "off", "unknown"}},
}

func oneOf(got string, allowed []string) bool {
	for _, a := range allowed {
		if got == a {
			return true
		}
	}
	return false
}

func assertLegalEnums(t *testing.T, d wire.Device, where string) {
	t.Helper()
	for _, e := range deviceEnums {
		if got := e.get(d); !oneOf(got, e.allowed) {
			t.Errorf("%s: device %q serves %s=%q, which is not in the contract enum %v\n"+
				"  → construct it through demoDevice() so an unset field lands on \"unknown\" "+
				"rather than the \"\" zero value (quince#361)", where, d.Name, e.field, got, e.allowed)
		}
	}
}

// TestEveryServedDeviceCarriesContractLegalEnums is the guard for the CLASS quince#361 was, not for
// the instance.
//
// wifi_sync was typed in the contract, defended in the registry, and rendered by the UI — and the
// demo provider constructed wire.Device values directly, inheriting "" for a field nobody set.
// Nothing failed: every layer did what it promised, the UI's `!== "unknown"` guards passed the empty
// string straight through, and four of five demo devices shipped a badge with no value.
//
// So this walks every device the provider actually SERVES, over the full Run() path — static seed
// plus the on-demand devices — rather than asserting against a list of names a future device would
// not be added to.
func TestEveryServedDeviceCarriesContractLegalEnums(t *testing.T) {
	p := newRunningProvider(t)
	devs := p.Devices()
	if len(devs) < 5 {
		t.Fatalf("demo world served %d devices, want at least the 5 fixtures — "+
			"if a device was removed, this guard is now weaker than it reads", len(devs))
	}
	for _, d := range devs {
		assertLegalEnums(t, d, "Provider.Devices()")
	}
}

// The churn path rebuilds studio-ipad on a timer, and it is where the seeded value used to be lost:
// the pad was constructed in TWO places and the copies drifted. Asserting the shared builder covers
// the rebuild without waiting out a ~20 s tick.
func TestChurnRebuiltPadKeepsItsEnums(t *testing.T) {
	pad := padDevice("2026-07-17T20:00:00Z", "2026-07-17T20:00:00Z")
	assertLegalEnums(t, pad, "padDevice (deviceChurn re-attach)")
	if pad.WifiSync != "off" {
		t.Errorf("churn-rebuilt studio-ipad has wifi_sync=%q, want off — the re-attach must not "+
			"drop what the seed established (quince#361)", pad.WifiSync)
	}
}

// The choke point itself, in both directions: it must fill an empty field and must NOT overwrite a
// value somebody set. A version that only did the first would quietly flatten the demo world to
// "unknown"; one that only did the second would be the bug.
func TestDemoDeviceDefaultsOnlyWhatIsUnset(t *testing.T) {
	got := demoDevice(wire.Device{Name: "bare"})
	for _, e := range deviceEnums {
		if v := e.get(got); v != "unknown" {
			t.Errorf("demoDevice left %s=%q on an unset field, want unknown", e.field, v)
		}
	}
	set := demoDevice(wire.Device{Name: "set", Paired: "no", BackupEncryption: "off", WifiSync: "on"})
	if set.Paired != "no" || set.BackupEncryption != "off" || set.WifiSync != "on" {
		t.Errorf("demoDevice overwrote values that were set: %+v", set)
	}
}

// The demo WORLD claim, and the reason quince#361 was filed at all: QA of quince#358 — "a device
// that is not there cannot be turned off" — was impossible on a dev deploy, because no demo device
// was wifi_sync "on" AND absent. WifiSyncControl's `unreachable` branch is exactly `on && !present`,
// so without such a device that branch cannot be reached by clicking.
func TestDemoWorldCoversWifiSyncOnAndOffline(t *testing.T) {
	p := newRunningProvider(t)
	for _, d := range p.Devices() {
		if d.WifiSync == "on" && d.Transports.USB == nil && d.Transports.WiFi == nil {
			return
		}
	}
	t.Error("no demo device is wifi_sync=on with no transports — WifiSyncControl's unreachable " +
		"branch (quince#358) is unreachable on a dev deploy, which is what quince#361 reported")
}

// A second pair of eyes on the seed, independent of the provider: NewProvider without Run().
// Run() is what adds the on-demand devices, so a guard that only ever looks at a running provider
// would not notice the static seed regressing on its own.
func TestStaticSeedCarriesContractLegalEnums(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	p := NewProvider(bus.New(), log)
	devs := p.Devices()
	if len(devs) != 2 {
		t.Fatalf("static seed served %d devices, want 2", len(devs))
	}
	for _, d := range devs {
		assertLegalEnums(t, d, "static seed")
	}
}

// The same guard for the per-device notifications switch (quince#1270), which needs its own because
// it is NOT an enum: it is a bool, so `deviceEnums` cannot cover it and the "" tell that catches an
// unset enum does not exist. An unset bool is `false`, and `false` here means MUTED — a demo device
// that silently stopped producing notifications, which is precisely the class quince#361 was.
//
// The exception map is how a DELIBERATELY muted demo device gets added: name it here, with why. A
// device that is muted and not listed is the bug this catches; a device listed and not muted is
// caught too, so the list cannot rot into a blanket exemption.
func TestEveryServedDeviceIsNotifiedAboutUnlessDeliberatelyMuted(t *testing.T) {
	deliberatelyMuted := map[string]string{}

	p := newRunningProvider(t)
	for _, d := range p.Devices() {
		reason, listed := deliberatelyMuted[d.Name]
		if listed && d.NotificationsEnabled {
			t.Errorf("demo device %q is listed as deliberately muted (%s) and serves "+
				"notifications_enabled=true — one of the two is wrong", d.Name, reason)
		}
		if !listed && !d.NotificationsEnabled {
			t.Errorf("demo device %q serves notifications_enabled=false and is not listed as "+
				"deliberately muted\n  → construct it through demoDevice(), which applies the "+
				"default the registry applies (quince#1270)", d.Name)
		}
	}
}

// The demo provider's half of PUT /api/devices/{udid}/notifications (quince#1270): the flip is
// visible on the very next read, and an unknown UDID is refused rather than silently accepted.
func TestDemoSetNotificationsEnabled(t *testing.T) {
	p := newRunningProvider(t)
	devs := p.Devices()
	if len(devs) == 0 {
		t.Fatal("demo world served no devices")
	}
	udid := devs[0].UDID

	// The RETURNED value is asserted, not only the read-back: the handler echoes it, so a
	// implementation that wrote correctly and reported the wrong thing would put a wrong
	// answer in the response body (quince#1281 review).
	if stored, status, reason := p.SetNotificationsEnabled(udid, false); status != 200 || stored {
		t.Fatalf("muting a known device = stored:%v %d %q, want stored:false 200", stored, status, reason)
	}
	if d, ok := p.Device(udid); !ok || d.NotificationsEnabled {
		t.Fatalf("device reads notifications_enabled=%v after being muted", d.NotificationsEnabled)
	}
	if stored, status, _ := p.SetNotificationsEnabled(udid, true); status != 200 || !stored {
		t.Fatalf("unmuting = stored:%v %d, want stored:true 200", stored, status)
	}
	if d, ok := p.Device(udid); !ok || !d.NotificationsEnabled {
		t.Fatalf("device reads notifications_enabled=%v after being unmuted", d.NotificationsEnabled)
	}

	// AN UNKNOWN DEVICE IS NOT A MUTED ONE. 200 here would be a write against nothing.
	if _, status, _ := p.SetNotificationsEnabled("NO-SUCH-UDID", false); status != 404 {
		t.Fatalf("unknown udid = %d, want 404", status)
	}
}
