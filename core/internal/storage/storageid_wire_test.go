package storage

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/store"
)

// qn.6c gap 1's second half: Version.storage_id crosses to the wire, NULLABLE, with null meaning
// "not yet attributed" (Operator ruling 2026-08-01).
//
// The two assertions worth making are opposite in shape: an unattributed version must serialise
// `null` — NOT omitted, NOT "" — because a client distinguishing "not yet known" from "no storage"
// can only do so if the key is present and null. And an attributed one must carry its id through
// unchanged.

func TestVersionStorageIDSerialisesNullWhenUnattributed(t *testing.T) {
	m := &Manager{backups: "/backups"}
	row := store.VersionRow{
		ID: "01J0V0000000000000000000", UDID: "00008140-000A1B2C3D4E5F60",
		Backend: BackendZFS, Kind: "full",
	}
	if row.StorageID != nil {
		t.Fatal("precondition: the row under test must be unattributed")
	}

	b, err := json.Marshal(m.toWire(row))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)

	// Present-and-null, which is the whole point: `omitempty` here would make "not yet attributed"
	// indistinguishable from an older server that does not know the field.
	if !strings.Contains(got, `"storage_id":null`) {
		t.Errorf(`want "storage_id":null in the payload, got: %s`, got)
	}
	if strings.Contains(got, `"storage_id":""`) {
		t.Errorf(`storage_id must never serialise as the empty string — "" is a value, null is not: %s`, got)
	}
}

func TestVersionStorageIDPassesThroughWhenAttributed(t *testing.T) {
	m := &Manager{backups: "/backups"}
	id := "01JQZX000000000000000000"
	row := store.VersionRow{
		ID: "01J0V0000000000000000000", UDID: "00008140-000A1B2C3D4E5F60",
		Backend: BackendZFS, Kind: "full", StorageID: &id,
	}

	v := m.toWire(row)
	if v.StorageID == nil || *v.StorageID != id {
		t.Fatalf("storage_id must pass through unchanged, got %v", v.StorageID)
	}

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"storage_id":"`+id+`"`) {
		t.Errorf("want the id on the wire, got: %s", b)
	}
}

// Version.backend is NOT derived from the storage — it is "how this version was made", a distinct
// fact that survives its storage (contracts §2, ruled 2026-08-01). This pins that the mapping
// reads it from the version's own row, so a later refactor that "helpfully" resolves it through
// the storage fails here rather than silently on a version whose storage is gone.
func TestVersionBackendComesFromTheVersionNotItsStorage(t *testing.T) {
	m := &Manager{backups: "/backups"}
	id := "01JQZX000000000000000000"
	row := store.VersionRow{
		ID: "01J0V0000000000000000000", UDID: "00008140-000A1B2C3D4E5F60",
		Backend: BackendHardlink, Kind: "full", StorageID: &id,
	}
	if got := m.toWire(row).Backend; got != BackendHardlink {
		t.Errorf("backend must be the version's own, got %q want %q", got, BackendHardlink)
	}
}
