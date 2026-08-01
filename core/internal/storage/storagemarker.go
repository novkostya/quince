package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// StorageMarkerName is the on-disk STORAGE identity marker, written at a storage's root — one
// tier above the per-device dirs, and two above the per-version quince-version.json (design §5).
//
// It exists because a removable disk's PATH changes on replug, so a storage identified by where
// it is mounted cannot answer "is this the same storage?". Only a UUID written INTO the storage
// can. Every later capability that needs storage identity — incremental scoped to
// (device, storage), missing-medium detection, migration — is unimplementable without it, and
// retrofitting identity onto storages already holding backups is the expensive version.
//
// SCOPE (qn.6c PR 3a): this file is the marker AS AN ARTIFACT — its format, its self-checksum,
// its round-trip, and the comparison that reports a disagreement. It deliberately implements
// NONE of the creation-moment lifecycle (when a marker may be written, what an absent one means,
// the missing-medium refusal). That is a decision requiring the `storages` DB row and lands with
// PR 3b, after the migration. Ruled on quince#378: story 2 became two claims when the creation
// rule gained a DB dependency, and this is the half that has no use for it.
const StorageMarkerName = "quince-storage.json"

// ErrStorageMarkerCorrupt is returned when a storage marker's self-checksum does not match its
// contents. Mirrors ErrMarkerCorrupt: a marker that cannot vouch for itself is not a marker.
var ErrStorageMarkerCorrupt = errors.New("storage: quince-storage.json failed its checksum")

// StorageMarker is the quince-storage.json payload. Checksum is a sha256 (hex) over the
// marshaled marker with Checksum emptied — self-contained, no companion file, exactly as
// quince-version.json does it.
//
// Backend is recorded at the storage's creation moment and is IMMUTABLE thereafter: a storage's
// backend is a property of the storage, which is the modeling correction this whole rung exists
// for. A later probe that disagrees with this field is a remount, not a re-selection — see
// StorageMarker.Mismatch.
type StorageMarker struct {
	StorageID  string `json:"storage_id"`
	Backend    string `json:"backend"`
	CreatedAt  string `json:"created_at"` // RFC3339 UTC
	AppVersion string `json:"app_version"`
	Checksum   string `json:"checksum"`
}

func (m StorageMarker) checksum() (string, error) {
	c := m
	c.Checksum = ""
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// WriteStorageMarker writes StorageMarkerName into root with a fresh checksum (0644 — it holds
// no secret, only storage identity).
//
// It removes any existing marker first rather than truncating, for the same reason WriteMarker
// does: never mutate a file whose inode might be shared. A storage root is not seeded or
// hardlink-cloned today, so this is defence rather than a live hazard — but the two markers
// should not differ in a safety property, because the next reader will assume they do not.
func WriteStorageMarker(root string, m StorageMarker) error {
	sum, err := m.checksum()
	if err != nil {
		return err
	}
	m.Checksum = sum
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, StorageMarkerName)
	_ = os.Remove(path)
	return os.WriteFile(path, b, 0o644)
}

// ReadStorageMarker reads and checksum-verifies StorageMarkerName from root. A missing file
// returns os.ErrNotExist (wrapped) — which the CALLER interprets, because "no marker here" means
// different things depending on whether quince has seen this storage before, and that judgement
// is PR 3b's, not this function's.
func ReadStorageMarker(root string) (StorageMarker, error) {
	b, err := os.ReadFile(filepath.Join(root, StorageMarkerName))
	if err != nil {
		return StorageMarker{}, err
	}
	var m StorageMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return StorageMarker{}, fmt.Errorf("%w: %v", ErrStorageMarkerCorrupt, err)
	}
	want := m.Checksum
	got, err := m.checksum()
	if err != nil {
		return StorageMarker{}, err
	}
	if want == "" || got != want {
		return StorageMarker{}, ErrStorageMarkerCorrupt
	}
	return m, nil
}

// Mismatch reports whether a freshly probed backend disagrees with what this marker records, and
// returns a sentence naming both sides.
//
// This is the REMOUNT guard's comparison half. A disagreement is never a re-selection: the
// backend was chosen once, at creation, and accepting a new one would write versions the marker
// misdescribes. The caller refuses; silently adopting the probed backend would be exactly the
// downgrade the "no silent caps or fallbacks" rule forbids.
//
// An EMPTY probed backend is not a mismatch. "I could not determine the backend" and "the backend
// changed" are different states with different remedies, and collapsing them would report a
// confident disagreement on the strength of a failed probe.
func (m StorageMarker) Mismatch(probed string) (bool, string) {
	if probed == "" || probed == m.Backend {
		return false, ""
	}
	return true, fmt.Sprintf(
		"storage %s records backend %q but this path now probes as %q — refusing rather than adopting the new backend, "+
			"because versions already committed here were made with %q",
		m.StorageID, m.Backend, probed, m.Backend)
}
