package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDeviceIdentityRowRoundTripsEveryField is the guard for the CLASS of defect quince#337 was,
// not for the instance.
//
// wifi_sync reached the wire, the registry and the contract, and was simply forgotten in the
// persistence path — which is enumerated field-by-field in three places (the INSERT column list,
// the UPDATE SET list, and the SELECT/Scan pair) with nothing tying it to the struct. Adding a
// field compiles fine, every existing test passes, and the value silently fails to survive a
// restart. Nothing failed, because every layer did exactly what it promised.
//
// So this test refuses to enumerate the fields. It fills EVERY string field by reflection with a
// distinct marker, round-trips the row through SQLite, and compares. A new field added to
// DeviceIdentityRow without touching the SQL fails here on the day it is added, naming itself.
func TestDeviceIdentityRowRoundTripsEveryField(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	want := DeviceIdentityRow{UpdatedAt: time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)}
	v := reflect.ValueOf(&want).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.String {
			continue // UpdatedAt is set above; it has its own formatting path
		}
		// A per-field marker, so a mix-up between two columns is caught as well as a drop.
		f.SetString("rt-" + strings.ToLower(v.Type().Field(i).Name))
	}
	// The primary key has to be a plausible UDID rather than a marker, but every other field
	// keeps its marker so the comparison below is exact.
	want.UDID = "SYNTHETIC-UDID-AAAA-0001"

	if err := st.UpsertDeviceIdentity(want); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, err := st.ListDeviceIdentities()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]

	gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
	for i := 0; i < wv.NumField(); i++ {
		name := wv.Type().Field(i).Name
		if wv.Field(i).Kind() != reflect.String {
			continue
		}
		if g, w := gv.Field(i).String(), wv.Field(i).String(); g != w {
			t.Errorf("field %s did not survive persistence: got %q, want %q\n"+
				"  → add it to the INSERT columns, the ON CONFLICT SET list, and the SELECT/Scan "+
				"in device_identity.go, plus a migration for the column (quince#337)", name, g, w)
		}
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
}

// The instance quince#337 reported, kept beside the general guard: a real Identity value must come
// back, not the zero string, because an offline device rendering `unknown` beside a persisted
// `paired` and `encryption` reads as a regression rather than as "not read yet".
func TestWifiSyncSurvivesARestart(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.UpsertDeviceIdentity(DeviceIdentityRow{
		UDID: "SYNTHETIC-UDID-AAAA-0001", Paired: "yes",
		BackupEncryption: "on", WifiSync: "on", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, err := st.ListDeviceIdentities()
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %v rows=%d", err, len(rows))
	}
	if rows[0].WifiSync != "on" {
		t.Fatalf("WifiSync = %q after a round trip, want on", rows[0].WifiSync)
	}
}
