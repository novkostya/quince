package store

import "testing"

// The per-device notifications switch round-trips, and ABSENCE MEANS ENABLED (quince#1270).
//
// The default is the load-bearing half. A device quince has just discovered has no row, and the
// answer for it must be *notify* — a device that appears and is silently silent is a silent
// fallback, which the hard rules forbid.
func TestDeviceNotificationsEnabledRoundTrip(t *testing.T) {
	st := openTemp(t)

	enabled, err := st.DeviceNotificationsEnabled("UDID-NEVER-ASKED", "")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatalf("a device with no stored preference reads muted; absence must mean enabled")
	}

	if err := st.SetDeviceNotificationsEnabled("UDID-A", "", false); err != nil {
		t.Fatal(err)
	}
	enabled, err = st.DeviceNotificationsEnabled("UDID-A", "")
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatalf("UDID-A was muted and reads enabled")
	}

	// The switch goes back, and it goes back on the SAME row — an upsert, not a second insert.
	if err := st.SetDeviceNotificationsEnabled("UDID-A", "", true); err != nil {
		t.Fatal(err)
	}
	enabled, err = st.DeviceNotificationsEnabled("UDID-A", "")
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatalf("UDID-A was unmuted and reads muted")
	}

	// MUTING ONE DEVICE MUTES ONE DEVICE. The row is keyed by UDID and this is what says so.
	if err := st.SetDeviceNotificationsEnabled("UDID-B", "", false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := st.DeviceNotificationsEnabled("UDID-A", ""); err != nil || !enabled {
		t.Fatalf("muting UDID-B changed UDID-A: enabled=%v err=%v", enabled, err)
	}
}

// An explicit `enabled = 1` is a row, not a deletion.
//
// It behaves identically to an absent row TODAY and would diverge the moment the default changes,
// and there is no reconstructing afterwards which devices the user had actually been asked about.
func TestUnmutingWritesARowRatherThanDeletingOne(t *testing.T) {
	st := openTemp(t)
	if err := st.SetDeviceNotificationsEnabled("UDID-A", "", true); err != nil {
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

// THE ENTIRE POINT OF 0018: (device, owner) is the key, so two principals hold independent
// opinions about one device. Nothing asserted this — `device_notify_test.go` was updated
// mechanically with "" throughout, which passes just as readily against the old single-row key
// (quince#1409 review, finding 5).
func TestTwoOwnersHoldIndependentRowsForOneDevice(t *testing.T) {
	st := openTemp(t)

	if err := st.SetDeviceNotificationsEnabled("DEV-1", "", false); err != nil {
		t.Fatalf("admin mute: %v", err)
	}
	if err := st.SetDeviceNotificationsEnabled("DEV-1", "HOLDER", true); err != nil {
		t.Fatalf("holder unmute: %v", err)
	}

	admin, err := st.DeviceNotificationsEnabled("DEV-1", "")
	if err != nil {
		t.Fatalf("read admin: %v", err)
	}
	holder, err := st.DeviceNotificationsEnabled("DEV-1", "HOLDER")
	if err != nil {
		t.Fatalf("read holder: %v", err)
	}
	if admin {
		t.Fatal("the admin's own mute did not survive the holder's write — one row, not two")
	}
	if !holder {
		t.Fatal("the holder's setting did not survive the admin's — one row, not two")
	}

	// AND THE UPSERT IS STILL AN UPSERT WITHIN ONE OWNER. If the key were wrong in the other
	// direction, a second write for the same owner would APPEND rather than replace, and the
	// read would return whichever row came first. This is the half that `''`-instead-of-NULL
	// buys, since SQLite treats NULLs as distinct in a unique index.
	if err := st.SetDeviceNotificationsEnabled("DEV-1", "", true); err != nil {
		t.Fatalf("admin unmute: %v", err)
	}
	admin, err = st.DeviceNotificationsEnabled("DEV-1", "")
	if err != nil {
		t.Fatalf("re-read admin: %v", err)
	}
	if !admin {
		t.Fatal("a second write for the same owner did not replace the first")
	}
}
