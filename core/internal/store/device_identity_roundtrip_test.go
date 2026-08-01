package store

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// updatedAtField is compared by NAME rather than skipped by kind: it round-trips through its own
// formatting path, so it gets an explicit value above and an explicit assertion below. Naming it
// is what lets every OTHER field be handled generically without a kind-shaped hole (quince#354).
const updatedAtField = "UpdatedAt"

// fillMarker gives f a distinct, non-zero value OF ITS OWN KIND, and REFUSES on a kind it does not
// know how to fill rather than skipping it.
//
// The refusal is the point, and it is the fix for quince#354. This loop used to skip every
// non-string field, with a comment admitting it: a future int or bool would be filled with its zero
// value, compared with reflect.Value.String() — a string-KIND accessor that returns a placeholder
// like "<int Value>" for anything else, so two DIFFERENT values compare equal — and would pass
// green. That is the quince#337 defect one type over, in the test written to make it impossible,
// failing silently in exactly the case it exists for. A test that cannot cover a field must SAY SO
// on the day that field is added, which is the only day anyone can act on it.
func fillMarker(f reflect.Value, name string, i int) error {
	if !f.CanSet() {
		return fmt.Errorf("field %s is unexported, so this guard cannot fill or read it — "+
			"either export it or assert its persistence explicitly; do not leave it uncovered", name)
	}
	switch f.Kind() {
	case reflect.String:
		f.SetString("rt-" + strings.ToLower(name))
	case reflect.Bool:
		// A bool has two values, so this catches a DROPPED field (the zero false ≠ true) but
		// CANNOT catch two bool columns swapped for each other. Stated rather than implied — it is
		// the one weakness the marker scheme keeps, and it is inherent to the type.
		f.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f.SetInt(int64(100 + i)) // per-field, so a swap between two ints is caught as well as a drop
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		f.SetUint(uint64(100 + i))
	case reflect.Float32, reflect.Float64:
		f.SetFloat(float64(100+i) + 0.5)
	default:
		return fmt.Errorf("field %s is a %s, which this guard does not know how to fill — "+
			"add a marker for that kind to fillMarker, or assert the field explicitly. Do NOT "+
			"skip it: a skipped field is one that can be dropped from the SQL unwitnessed, which "+
			"is the whole defect this test exists to catch (quince#354)", name, f.Kind())
	}
	return nil
}

// TestDeviceIdentityRowRoundTripsEveryField is the guard for the CLASS of defect quince#337 was,
// not for the instance.
//
// wifi_sync reached the wire, the registry and the contract, and was simply forgotten in the
// persistence path — which is enumerated field-by-field in three places (the INSERT column list,
// the UPDATE SET list, and the SELECT/Scan pair) with nothing tying it to the struct. Adding a
// field compiles fine, every existing test passes, and the value silently fails to survive a
// restart. Nothing failed, because every layer did exactly what it promised.
//
// So this test refuses to enumerate the fields. It fills EVERY field by reflection with a distinct
// marker of that field's own kind, round-trips the row through SQLite, and compares with
// reflect.DeepEqual. A new field added to DeviceIdentityRow without touching the SQL fails here on
// the day it is added, naming itself — whatever its type, and a type this guard cannot handle is a
// loud failure rather than a silent skip (quince#354).
func TestDeviceIdentityRowRoundTripsEveryField(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	want := DeviceIdentityRow{UpdatedAt: time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)}
	v := reflect.ValueOf(&want).Elem()
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if name == updatedAtField {
			continue // set above, asserted by name below
		}
		// A per-field marker, so a mix-up between two columns is caught as well as a drop.
		if err := fillMarker(v.Field(i), name, i); err != nil {
			t.Fatalf("cannot build a round-trip marker: %v", err)
		}
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
		if name == updatedAtField {
			continue // asserted below, with time's own equality
		}
		// DeepEqual over .Interface(), never .String(): the latter is a string-KIND accessor, so on
		// an int or a bool it returns the same placeholder for every value and the comparison is
		// vacuous. Measured on quince#345 — the same mutation FAILED under DeepEqual and passed
		// green under .String() (quince#354).
		if g, w := gv.Field(i).Interface(), wv.Field(i).Interface(); !reflect.DeepEqual(g, w) {
			t.Errorf("field %s did not survive persistence: got %v, want %v\n"+
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
