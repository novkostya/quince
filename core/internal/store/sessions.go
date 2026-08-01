package store

import (
	"database/sql"
	"errors"
	"time"
)

// AuthSession is an admin cookie session.
type AuthSession struct {
	ID         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateAuthSession inserts a new session.
func (s *Store) CreateAuthSession(sess AuthSession) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions_auth (id, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?)`,
		sess.ID, fmtTime(sess.CreatedAt), fmtTime(sess.LastSeenAt), fmtTime(sess.ExpiresAt))
	return err
}

// GetAuthSession fetches a session by id.
func (s *Store) GetAuthSession(id string) (AuthSession, bool, error) {
	var created, lastSeen, expires string
	err := s.db.QueryRow(
		`SELECT created_at, last_seen_at, expires_at FROM sessions_auth WHERE id = ?`, id).
		Scan(&created, &lastSeen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthSession{}, false, nil
	}
	if err != nil {
		return AuthSession{}, false, err
	}
	sess := AuthSession{ID: id}
	if sess.CreatedAt, err = parseTime(created); err != nil {
		return AuthSession{}, false, err
	}
	if sess.LastSeenAt, err = parseTime(lastSeen); err != nil {
		return AuthSession{}, false, err
	}
	if sess.ExpiresAt, err = parseTime(expires); err != nil {
		return AuthSession{}, false, err
	}
	return sess, true, nil
}

// TouchAuthSession updates last_seen_at (idle-timeout tracking).
func (s *Store) TouchAuthSession(id string, lastSeen time.Time) error {
	_, err := s.db.Exec(`UPDATE sessions_auth SET last_seen_at = ? WHERE id = ?`, fmtTime(lastSeen), id)
	return err
}

// DeleteAuthSession removes one session (logout, expiry).
func (s *Store) DeleteAuthSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions_auth WHERE id = ?`, id)
	return err
}

// DeleteAllAuthSessions is DELIBERATELY ABSENT. It existed to rotate on login by evicting every
// other device, and the Operator ruled that policy out (quince#373): a login now supersedes only
// the caller's own session, through DeleteAuthSession above.
//
// Removed rather than left unused, because a "clear every session" primitive sitting beside the
// per-client one is a footgun — it reads like the rotation helper and would silently restore the
// behaviour that was ruled against. A future deliberate "sign out other devices" action (option 3
// on that issue) should re-add it with its own caller and its own control, which is the point: the
// eviction becomes something the user chooses rather than a side effect of logging in.
