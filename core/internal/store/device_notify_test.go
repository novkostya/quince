package store

import "testing"

// The per-device notifications switch round-trips, and ABSENCE MEANS ENABLED (quince#1270).
//
// The default is the load-bearing half. A device quince has just discovered has no row, and the
// answer for it must be *notify* — a device that appears and is silently silent is a silent
// fallback, which the hard rules forbid.
func TestDeviceNotificationsEnabledRoundTrip(t *testing.T) {
	st := openTemp(t)

	enabled, err := st.DeviceNotificationsEnabled("UDID-NEVER-ASKED")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatalf("a device with no stored preference reads muted; absence must mean enabled")
	}

	if err := st.SetDeviceNotificationsEnabled("UDID-A", false); err != nil {
		t.Fatal(err)
	}
	enabled, err = st.DeviceNotificationsEnabled("UDID-A")
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatalf("UDID-A was muted and reads enabled")
	}

	// The switch goes back, and it goes back on the SAME row — an upsert, not a second insert.
	if err := st.SetDeviceNotificationsEnabled("UDID-A", true); err != nil {
		t.Fatal(err)
	}
	enabled, err = st.DeviceNotificationsEnabled("UDID-A")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatalf("UDID-A was unmuted and reads muted")
	}

	// MUTING ONE DEVICE MUTES ONE DEVICE. The row is keyed by UDID and this is what says so.
	if err := st.SetDeviceNotificationsEnabled("UDID-B", false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := st.DeviceNotificationsEnabled("UDID-A"); err != nil || !enabled {
		t.Fatalf("muting UDID-B changed UDID-A: enabled=%v err=%v", enabled, err)
	}
}

// An explicit `enabled = 1` is a row, not a deletion.
//
// It behaves identically to an absent row TODAY and would diverge the moment the default changes,
// and there is no reconstructing afterwards which devices the user had actually been asked about.
func TestUnmutingWritesARowRatherThanDeletingOne(t *testing.T) {
	st := openTemp(t)
	if err := st.SetDeviceNotificationsEnabled("UDID-A", true); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM device_notification_prefs WHERE udid = ?`, "UDID-A").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows for an explicitly-enabled device = %d, want 1 — the choice was not recorded", n)
	}
}
