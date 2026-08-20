package store

import (
	"database/sql"
	"errors"
	"time"
)

// AuthSession is a cookie session, and it records WHAT AUTHENTICATED IT.
type AuthSession struct {
	ID         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time

	// CredentialID is the passkey that minted this session, or nil for a password login.
	//
	// NIL MEANS THE ADMIN — see 0014_session_principal.sql, which carries the reasoning and
	// the hazard. A POINTER rather than a string, because "" and "no credential" must not be
	// the same value: the second is a fact about how this session was created and the first
	// would be an empty credential id, which is not a thing that exists.
	CredentialID *string
}

// CreateAuthSession inserts a new session.
func (s *Store) CreateAuthSession(sess AuthSession) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions_auth (id, created_at, last_seen_at, expires_at, credential_id)
		 VALUES (?, ?, ?, ?, ?)`,
		sess.ID, fmtTime(sess.CreatedAt), fmtTime(sess.LastSeenAt), fmtTime(sess.ExpiresAt),
		sess.CredentialID)
	return err
}

// GetAuthSession fetches a session by id.
func (s *Store) GetAuthSession(id string) (AuthSession, bool, error) {
	var created, lastSeen, expires string
	// NULL is the ordinary case for a password login, so this scans into a nullable rather
	// than defaulting — a default here would invent a credential that was never presented.
	var credential sql.NullString
	err := s.db.QueryRow(
		`SELECT created_at, last_seen_at, expires_at, credential_id FROM sessions_auth WHERE id = ?`, id).
		Scan(&created, &lastSeen, &expires, &credential)
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
	if credential.Valid {
		sess.CredentialID = &credential.String
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

// DeleteAllAuthSessions removes every session and returns how many went.
//
// IT WAS DELIBERATELY ABSENT UNTIL qn.6k, AND THE REASON IT IS BACK IS THE ONE THAT PARAGRAPH
// NAMED. It existed to rotate on login by evicting every other device; the Operator ruled that
// policy out (quince#373), and it was removed rather than left unused because a "clear every
// session" primitive sitting beside the per-client one reads like the rotation helper and would
// silently restore the ruled-against behaviour. The tombstone said a future deliberate action
// "should re-add it with its own caller and its own control, which is the point: the eviction
// becomes something the user chooses rather than a side effect of logging in."
//
// `quince auth reset` is exactly that: one caller, run by hand on the host, doing nothing else.
// IT IS NOT A LOGIN PATH AND MUST NOT ACQUIRE ONE. If a second caller ever appears, re-read
// quince#373 before adding it — the ruling is about login, and this function is one `Login()` call
// away from breaking it again.
//
// WHY RESET NEEDS IT, measured rather than assumed: `Service.Authenticate` never consults the
// password. It checks the session row and its expiry, nothing else. So clearing the password alone
// leaves a live cookie passing `authGuard` and reaching every protected route, while the UI reads
// `needs_setup` off `Status()` — which DOES short-circuit on the missing password. Reset would
// otherwise look complete and leave the box authenticated.
func (s *Store) DeleteAllAuthSessions() (int, error) {
	res, err := s.db.Exec(`DELETE FROM sessions_auth`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// DeleteAuthSessionsFor ends every session created by one credential, except the caller's own.
//
// `credentialID` nil means THE PASSWORD — the sessions a password login created, which are exactly
// the rows whose `credential_id` is NULL (0014_session_principal.sql). A credential id names one
// passkey.
//
// WHY THIS EXISTS: quince#1001. Changing a password left every other signed-in session alive, and
// the UI said so out loud. People change a password because they think it leaked, and the sessions
// a leaked password created are the ones that must not survive it.
//
// IT IS NOT quince#373 COMING BACK. That ruling is about LOGGING IN: a login supersedes only the
// calling client's own prior session, so signing in on a phone does not sign you out on a laptop.
// A credential CHANGE is a different event, and it inherited the login behaviour by default rather
// than by decision. This function has no login caller and must never acquire one — the same
// standing warning `DeleteAllAuthSessions` carries, for the same reason.
//
// `except` KEEPS THE ACTING SESSION. Signing the user out of the tab they are changing their
// password in would be a hostile way to confirm success, and it is not what the norm this
// implements means.
//
// SELECTIVE, NOT WHOLESALE, WHICH IS ONLY POSSIBLE BECAUSE OF 0014. quince#1001 offered "end every
// session except the current one" as a cheap fallback if per-credential was disproportionate. It is
// not needed: the session now records what authenticated it, so removing a passkey ends that
// passkey's sessions and leaves a password login on another device alone.
func (s *Store) DeleteAuthSessionsFor(credentialID *string, except string) (int, error) {
	var (
		res sql.Result
		err error
	)
	if credentialID == nil {
		// `IS NULL` rather than `= ?`, because SQL equality against NULL is never true — a
		// parameterised version of this would silently delete nothing, which is the failure mode
		// this whole function exists to prevent, arriving as a no-op.
		res, err = s.db.Exec(
			`DELETE FROM sessions_auth WHERE credential_id IS NULL AND id != ?`, except)
	} else {
		res, err = s.db.Exec(
			`DELETE FROM sessions_auth WHERE credential_id = ? AND id != ?`, *credentialID, except)
	}
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
