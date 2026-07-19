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

// DeleteAllAuthSessions clears every session — used to rotate on login (single admin: a
// fresh login supersedes any prior session, defeating fixation).
func (s *Store) DeleteAllAuthSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions_auth`)
	return err
}
