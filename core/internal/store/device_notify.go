package store

import (
	"database/sql"
	"errors"
	"time"
)

// The per-device notifications switch — quince#1270.
//
// THE THIRD AXIS OF NOTIFICATION POLICY, and the product has already confused two of them once.
// `push_subscriptions` says which browser RECEIVES; `notifications:` in `config.yml` says which
// KINDS are sent at all; this says which backed-up DEVICE generates them. Keeping the three apart
// in the schema is what keeps them apart on screen.

// DeviceNotificationsEnabled reports whether quince should notify about this device.
//
// ABSENCE MEANS ENABLED, and that is the default this feature ships with rather than an accident of
// SQL: a device that appears and is silently silent is a silent fallback, which the hard rules
// forbid. Four of the five categories default on; a newly discovered device inherits that, and the
// user turns it off deliberately.
// OWNER SINCE qn.13 slice 10b: "" is the admin, a udid is that device's scoped principal.
// The same device has one row per principal with an opinion about it, and they do not
// interact — the admin muting a device is the admin choosing not to hear about it, and says
// nothing about its holder (Operator, 2026-08-21).
func (s *Store) DeviceNotificationsEnabled(udid, owner string) (bool, error) {
	var enabled int
	err := s.db.QueryRow(
		`SELECT enabled FROM device_notification_prefs WHERE udid = ? AND owner_udid = ?`,
		udid, owner).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return enabled != 0, nil
}

// SetDeviceNotificationsEnabled records the user's choice for one device.
//
// IT WRITES A ROW IN BOTH DIRECTIONS rather than deleting the row when the answer matches today's
// default. A row that says `enabled=1` is the user having chosen it; an absent row is quince never
// having been asked. They behave identically now and would diverge the moment the default changes,
// and reconstructing which was which afterwards is impossible.
func (s *Store) SetDeviceNotificationsEnabled(udid, owner string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO device_notification_prefs (udid, owner_udid, enabled, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(udid, owner_udid) DO UPDATE SET
		   enabled = excluded.enabled, updated_at = excluded.updated_at`,
		udid, owner, v, time.Now().UTC().Format(time.RFC3339))
	return err
}
