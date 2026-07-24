package store

import (
	"testing"
	"time"
)

// UDIDsWithVersions returns the distinct backed-up UDIDs, newest-activity first — the offline-device
// set the registry unions with live presence (qn.6a).
func TestUDIDsWithVersions(t *testing.T) {
	st := openTemp(t)
	base := time.Date(2026, 7, 18, 2, 30, 0, 0, time.UTC)
	rows := []VersionRow{
		{ID: "01A1", UDID: "UDID-A", Backend: "reflink", CreatedAt: base},
		{ID: "01A2", UDID: "UDID-A", Backend: "reflink", CreatedAt: base.Add(2 * time.Hour)},
		{ID: "01B1", UDID: "UDID-B", Backend: "reflink", CreatedAt: base.Add(time.Hour)},
	}
	for _, v := range rows {
		if err := st.InsertVersion(v); err != nil {
			t.Fatalf("insert %s: %v", v.ID, err)
		}
	}
	got, err := st.UDIDsWithVersions()
	if err != nil {
		t.Fatal(err)
	}
	// UDID-A's newest version (base+2h) is newer than UDID-B's (base+1h) → A first, and each UDID once.
	if len(got) != 2 || got[0] != "UDID-A" || got[1] != "UDID-B" {
		t.Fatalf("UDIDsWithVersions() = %v, want [UDID-A UDID-B]", got)
	}
}

// device_identity round-trips through upsert (name/last_seen updated on conflict) and list.
func TestDeviceIdentityRoundTrip(t *testing.T) {
	st := openTemp(t)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertDeviceIdentity(DeviceIdentityRow{
		UDID: "UDID-A", Name: "phone", Model: "iPhone16,1", IOSVersion: "26.0",
		Paired: "yes", BackupEncryption: "on", LastSeen: "2026-07-20T10:00:00Z", UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Upsert again with a fresher name + last_seen → same row, updated.
	if err := st.UpsertDeviceIdentity(DeviceIdentityRow{
		UDID: "UDID-A", Name: "renamed-phone", LastSeen: "2026-07-20T11:00:00Z", UpdatedAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListDeviceIdentities()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListDeviceIdentities() = %d rows, want 1 (upsert, not insert)", len(rows))
	}
	r := rows[0]
	if r.UDID != "UDID-A" || r.Name != "renamed-phone" || r.LastSeen != "2026-07-20T11:00:00Z" {
		t.Fatalf("row = %+v, want the upserted name + last_seen", r)
	}
}
