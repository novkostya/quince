package store

import "time"

// DeviceIdentityRow is the persisted lockdown identity + last-seen for one device (qn.6a offline
// devices). It exists so a device that has backups but is not currently connected can still be
// listed by name with a real "last seen"; presence itself is never persisted (the live muxd table
// owns that). Empty strings mean "not determined" — the registry leaves its honest default.
type DeviceIdentityRow struct {
	UDID             string
	Name             string
	Model            string
	IOSVersion       string
	Paired           string
	BackupEncryption string
	LastSeen         string // RFC3339 UTC; "" = never seen present since this row was written
	UpdatedAt        time.Time
}

// UpsertDeviceIdentity records (or refreshes) a device's persisted identity. LastSeen is only
// advanced by the caller when the device is actually present, so it never regresses to a stale value.
func (s *Store) UpsertDeviceIdentity(r DeviceIdentityRow) error {
	_, err := s.db.Exec(`INSERT INTO device_identity
		(udid, name, model, ios_version, paired, backup_encryption, last_seen, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(udid) DO UPDATE SET
			name=excluded.name, model=excluded.model, ios_version=excluded.ios_version,
			paired=excluded.paired, backup_encryption=excluded.backup_encryption,
			last_seen=excluded.last_seen, updated_at=excluded.updated_at`,
		r.UDID, r.Name, r.Model, r.IOSVersion, r.Paired, r.BackupEncryption,
		r.LastSeen, fmtTime(r.UpdatedAt))
	return err
}

// ListDeviceIdentities returns every persisted identity (read once at startup to seed the registry).
func (s *Store) ListDeviceIdentities() ([]DeviceIdentityRow, error) {
	rows, err := s.db.Query(`SELECT udid, name, model, ios_version, paired, backup_encryption,
		last_seen, updated_at FROM device_identity`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []DeviceIdentityRow
	for rows.Next() {
		var (
			r       DeviceIdentityRow
			updated string
		)
		if err := rows.Scan(&r.UDID, &r.Name, &r.Model, &r.IOSVersion, &r.Paired,
			&r.BackupEncryption, &r.LastSeen, &updated); err != nil {
			return nil, err
		}
		if t, err := parseTime(updated); err == nil {
			r.UpdatedAt = t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
