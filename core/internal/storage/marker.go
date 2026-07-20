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

// MarkerName is the on-disk version marker written into every committed version BEFORE
// promotion (design §5). It makes reconciliation deterministic: a verified artifact carrying a
// valid marker is re-adoptable; a missing or hash-failing marker is the one case roll-forward
// refuses.
const MarkerName = "quince-version.json"

// ErrMarkerCorrupt is returned when a marker's self-checksum does not match its contents.
var ErrMarkerCorrupt = errors.New("storage: quince-version.json failed its checksum")

// Marker is the quince-version.json payload. Checksum is a sha256 (hex) over the marshaled
// marker with Checksum emptied — a self-contained integrity check, no companion file.
type Marker struct {
	VersionID           string `json:"version_id"`
	JobID               string `json:"job_id"`
	UDID                string `json:"udid"`
	Backend             string `json:"backend"`
	CreatedAt           string `json:"created_at"` // RFC3339 UTC
	Kind                string `json:"kind"`
	Encrypted           bool   `json:"encrypted"`
	StructureVerifiedAt string `json:"structure_verified_at"` // RFC3339 UTC
	AppVersion          string `json:"app_version"`
	Checksum            string `json:"checksum"`
}

// checksum returns the sha256 hex of the marker with Checksum cleared (stable field order via
// struct marshaling → deterministic).
func (m Marker) checksum() (string, error) {
	c := m
	c.Checksum = ""
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// WriteMarker writes MarkerName into dir with a fresh checksum (0644 — not a secret; it holds
// no password, only version identity).
func WriteMarker(dir string, m Marker) error {
	sum, err := m.checksum()
	if err != nil {
		return err
	}
	m.Checksum = sum
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Remove any existing marker first so we never TRUNCATE a possibly-hard-linked inode: on the
	// hardlink backend a seeded work/ shares inodes with the committed latest/, and truncating
	// the shared marker would rewrite the committed version's identity. Replace, never mutate.
	path := filepath.Join(dir, MarkerName)
	_ = os.Remove(path)
	return os.WriteFile(path, b, 0o644)
}

// ReadMarker reads and checksum-verifies MarkerName from dir. A missing file returns
// os.ErrNotExist (wrapped); a checksum mismatch returns ErrMarkerCorrupt.
func ReadMarker(dir string) (Marker, error) {
	b, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if err != nil {
		return Marker{}, err
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		return Marker{}, fmt.Errorf("%w: %v", ErrMarkerCorrupt, err)
	}
	want := m.Checksum
	got, err := m.checksum()
	if err != nil {
		return Marker{}, err
	}
	if want == "" || got != want {
		return Marker{}, ErrMarkerCorrupt
	}
	return m, nil
}
