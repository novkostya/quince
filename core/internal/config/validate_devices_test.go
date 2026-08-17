package config

import (
	"strings"
	"testing"
)

// Validate is what answers a PUT (contracts §1: 422 {errors:[{path,message}]}), so it must accept
// every form muxaddr does and refuse the rest BY PATH. The serve-path parse in buildLiveStack is
// the other half — see validateDevices for why one check is not enough.
func TestValidateAcceptsEveryMuxerAddressForm(t *testing.T) {
	for _, form := range []string{
		"/run/mux/usbmuxd",      // a unix socket path
		"UNIX:/run/mux/usbmuxd", // libusbmuxd's own spelling — REGRESSION (quince#897 item 1)
		"127.0.0.1:27015",       // TCP
		"",                      // no muxer for this transport
	} {
		c := Default()
		c.Devices.UsbmuxdSocket, c.Devices.NetmuxdAddr = form, form
		for _, e := range Validate(c) {
			if e.Path == "devices.usbmuxd_socket" || e.Path == "devices.netmuxd_addr" {
				t.Errorf("Validate refused %q at %s: %s", form, e.Path, e.Message)
			}
		}
	}
}

// A malformed address is reported against the KEY the operator typed it into. Both keys are
// checked, because netmuxd_addr was the one that accepted a narrower grammar than it documented.
func TestValidateRefusesAMalformedMuxerAddressByPath(t *testing.T) {
	for _, tc := range []struct{ path, bad string }{
		{"devices.usbmuxd_socket", "usbmuxd"},
		{"devices.netmuxd_addr", "not-an-address"},
	} {
		c := Default()
		switch tc.path {
		case "devices.usbmuxd_socket":
			c.Devices.UsbmuxdSocket = tc.bad
		default:
			c.Devices.NetmuxdAddr = tc.bad
		}
		found := false
		for _, e := range Validate(c) {
			if e.Path == tc.path {
				found = true
			}
		}
		if !found {
			t.Errorf("Validate accepted %q at %s; want a 422 error on that path", tc.bad, tc.path)
		}
	}
}

// qn.6p D2. `manage_muxer: true` asks for a profile v0.1 does not ship. It is refused rather than
// ignored — yaml.Unmarshal drops unknown keys silently, so deleting the key would have turned an
// existing all-in-one install into a muxerless one with nothing said about it
// (storages_validate_test.go:162 is this repo's record of that class).
//
// VALIDATE MUST NOT BE WHERE THAT HAPPENS, which is what this test pins. A validation error
// discards the config to Default(), which has no storage, so an operator with a working install
// would be told to add their first storage while the real reason sat in `GET /api/config`
// warnings — rendered by no surface a user can reach (quince#849). Architect ruling, quince#1059,
// on the same grounds `tls:` already records in contracts.
func TestValidateDoesNotRefuseManageMuxerTrue(t *testing.T) {
	c := Default()
	c.Devices.ManageMuxer = true
	for _, e := range Validate(c) {
		if e.Path == "devices.manage_muxer" {
			t.Fatalf("Validate refused manage_muxer at %s (%q) — that discards the whole config "+
				"to Default() and reports it as a missing storage; it is a serve-path refusal",
				e.Path, e.Message)
		}
	}
}

// CheckMuxerProfile IS where it is refused: fatal, on the serve path, leaving the config intact.
func TestCheckMuxerProfileRefusesTheManagedProfile(t *testing.T) {
	if err := CheckMuxerProfile(Default().Devices); err != nil {
		t.Fatalf("CheckMuxerProfile refused the shipped defaults: %v", err)
	}

	d := Default().Devices
	d.ManageMuxer = true
	err := CheckMuxerProfile(d)
	if err == nil {
		t.Fatal("CheckMuxerProfile accepted manage_muxer: true; want a refusal")
	}
	// The refusal is read by somebody whose working install just stopped starting, so it must name
	// the key, what to do instead, and — because the Validate route's whole failure mode was an
	// operator believing their config was gone — that nothing was discarded.
	for _, want := range []string{
		"manage_muxer", "usbmuxd_socket", "quince#897", "has been changed or discarded",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q:\n%v", want, err)
		}
	}
}

// Default() must pass its own validation: Load() DISCARDS an invalid config and falls
// back to Default(), so a Default that failed validation would make that fallback
// invalid too — a loop with no honest answer.
func TestDefaultPassesItsOwnValidation(t *testing.T) {
	if errs := Validate(Default()); len(errs) > 0 {
		t.Fatalf("Default() fails Validate: %+v", errs)
	}
}

// The dead `127.0.0.1:27015` default is gone (quince#897 item 3): it made quince dial a port
// nothing listened on forever, and made "no Wi-Fi muxer" inexpressible except by knowing that an
// explicit "" differs from an absent key.
func TestDefaultHasNoPhantomWifiMuxer(t *testing.T) {
	if got := Default().Devices.NetmuxdAddr; got != "" {
		t.Errorf("default netmuxd_addr = %q; want empty — a default here is a dead port dialed forever", got)
	}
	// The USB default stays, and stays usbmuxd's OWN default path, so a host already running
	// usbmuxd works with no config.yml at all — the case quince#897 was filed about.
	if got := Default().Devices.UsbmuxdSocket; got != "/var/run/usbmuxd" {
		t.Errorf("default usbmuxd_socket = %q; want /var/run/usbmuxd (usbmuxd's own default)", got)
	}
}

// qn.12 — OVERDUE CANNOT PRECEDE STALE.
//
// The reminder track ranks by these two, so an inverted pair makes every first reminder a
// `backup_overdue`: a device one day past its threshold greeted as a reproach rather than invited.
// Refused rather than clamped — a clamp honours neither number and says nothing.
func TestOverdueDaysMustNotPrecedeStalenessDays(t *testing.T) {
	c := Default()
	c.Notifications.StalenessDays = 10
	c.Notifications.OverdueDays = 3
	errs := Validate(c)
	msg := ""
	for _, e := range errs {
		if e.Path == "notifications.overdue_days" {
			msg = e.Message
		}
	}
	if msg == "" {
		t.Fatalf("an overdue threshold before the staleness threshold was accepted: %+v", errs)
	}
	// The message must name the OTHER number, because "must be >= staleness_days" alone leaves the
	// reader to go and look up what they set it to.
	if !strings.Contains(msg, "10") {
		t.Errorf("the refusal does not echo the staleness threshold it is measured against: %q", msg)
	}
}

// EQUAL IS LEGAL. "Stale and overdue on the same day" is a coherent thing to want — it collapses the
// two ranks into one — and refusing it would be the validator inventing a policy.
func TestOverdueDaysMayEqualStalenessDays(t *testing.T) {
	c := Default()
	c.Notifications.StalenessDays = 5
	c.Notifications.OverdueDays = 5
	for _, e := range Validate(c) {
		if strings.HasPrefix(e.Path, "notifications.") {
			t.Errorf("equal thresholds were refused: %+v", e)
		}
	}
}

// ONE MISTAKE, ONE ERROR. A negative overdue_days is already refused on its own; it must not ALSO
// trip the ordering check, or a single typo reports twice and the second message is nonsense.
func TestANegativeOverdueDaysReportsOnce(t *testing.T) {
	c := Default()
	c.Notifications.OverdueDays = -1
	n := 0
	for _, e := range Validate(c) {
		if e.Path == "notifications.overdue_days" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("a negative overdue_days produced %d errors, want exactly 1", n)
	}
}

// THE DEFAULTS ARE THE RULING'S. quince#1124: backup_completed OFF, the rest ON. Asserted because
// they are a product decision rather than an implementation detail, and a silent flip of
// backup_completed is the noise that teaches people to swipe without reading.
func TestNotificationDefaultsMatchTheRuling(t *testing.T) {
	d := Default().Notifications
	if d.BackupCompleted {
		t.Errorf("backup_completed defaults ON; the ruling says OFF")
	}
	for name, on := range map[string]bool{
		"backup_available": d.BackupAvailable,
		"backup_overdue":   d.BackupOverdue,
		"action_required":  d.ActionRequired,
		"backup_failed":    d.BackupFailed,
	} {
		if !on {
			t.Errorf("%s defaults OFF; the ruling says ON", name)
		}
	}
}
