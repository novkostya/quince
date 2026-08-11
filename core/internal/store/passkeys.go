package store

import (
	"database/sql"
	"errors"
	"time"
)

// Passkey storage. qn.6k slice 2 deliberately carries only what the RECOVERY path needs — count
// and clear. Registration, listing and per-credential removal arrive with the endpoints in slice 3,
// so nothing here can be used to create a credential before `quince auth reset` exists to undo one.

// Passkey is one registered credential. Slice 2 uses it only to WRITE one, so that the removal this
// slice exists for can be tested against a box that actually has credentials; the read side — list,
// rename, look up by id for an assertion — arrives with the endpoints in slice 3.
type Passkey struct {
	CredentialID string
	PublicKey    []byte
	RPID         string // the rpId this credential was registered against (spec qn.6k D2)
	SignCount    uint32
	// BackupEligible is IMMUTABLE per the spec and is compared at every assertion; BackupState can
	// change and is rewritten on each one. Pointers because NULL means "registered before quince
	// recorded these", which is not the same as false — see 0009_passkey_flags.sql.
	BackupEligible *bool
	BackupState    *bool
	AAGUID         []byte
	Transports     string // JSON array as reported at registration
	Name           string
	CreatedAt      time.Time
	// LastUsedAt is the zero time until the credential's first successful assertion. Zero means
	// NEVER USED rather than "used at the epoch", and the Settings surface has to render that
	// difference — a credential nobody has signed in with is exactly the one worth removing.
	LastUsedAt time.Time
}

// InsertPasskey records a credential.
//
// PRESENT IN SLICE 2 ONLY SO THE REMOVAL CAN BE PROVEN AGAINST A NON-EMPTY TABLE. It has no caller
// in the shipped binary — no endpoint and no CLI reaches it — so the ruled order holds: nothing can
// issue a credential before `quince auth reset` exists to clear one. Slice 3's registration
// endpoint is its first real caller.
func (s *Store) InsertPasskey(p Passkey) error {
	_, err := s.db.Exec(
		`INSERT INTO passkeys
		   (credential_id, public_key, rp_id, sign_count, aaguid, transports, name, created_at,
		    backup_eligible, backup_state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.CredentialID, p.PublicKey, p.RPID, p.SignCount, p.AAGUID, p.Transports, p.Name,
		fmtTime(p.CreatedAt), p.BackupEligible, p.BackupState)
	return err
}

// CountPasskeys returns how many passkey credentials are registered.
//
// Used by `quince auth reset` to REPORT what it removed. That is not cosmetic: a reset that prints
// "cleared 2 passkeys" tells the operator the box had credentials they may not have known about,
// and one that prints 0 tells them this reset was password-only. Silence would say neither.
func (s *Store) CountPasskeys() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM passkeys`).Scan(&n)
	return n, err
}

// DeleteAllPasskeys removes every passkey credential and returns how many rows went.
//
// EVERY one, not a selection, and that is the ruled behaviour rather than a simplification: a
// credential list the locked-out user cannot reach is not recovery. Leaving them would leave the box
// authenticatable by the phone that is, by hypothesis, the thing that was lost.
func (s *Store) DeleteAllPasskeys() (int, error) {
	res, err := s.db.Exec(`DELETE FROM passkeys`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// scanPasskey reads one row. The nullable columns are the ones with a genuine "not yet" state —
// `last_used_at` until the first assertion, and the two informational authenticator fields — so
// each is read through a Null* and left at its zero value rather than being defaulted to something
// that reads like data.
func scanPasskey(sc interface{ Scan(...any) error }) (Passkey, error) {
	var (
		p          Passkey
		aaguid     []byte
		transports sql.NullString
		created    string
		lastUsed   sql.NullString
		beligible  sql.NullBool
		bstate     sql.NullBool
	)
	if err := sc.Scan(&p.CredentialID, &p.PublicKey, &p.RPID, &p.SignCount, &aaguid,
		&transports, &p.Name, &beligible, &bstate, &created, &lastUsed); err != nil {
		return Passkey{}, err
	}
	p.AAGUID = aaguid
	p.Transports = transports.String
	// NULL stays nil rather than becoming false. A credential registered before quince recorded
	// these cannot have them reconstructed, and false is the answer that would make a synced passkey
	// fail validation for ever — the exact bug 0009 exists to fix.
	if beligible.Valid {
		p.BackupEligible = &beligible.Bool
	}
	if bstate.Valid {
		p.BackupState = &bstate.Bool
	}
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return Passkey{}, err
	}
	if lastUsed.Valid {
		if p.LastUsedAt, err = parseTime(lastUsed.String); err != nil {
			return Passkey{}, err
		}
	}
	return p, nil
}

const passkeyCols = `credential_id, public_key, rp_id, sign_count, aaguid, transports, name,
	backup_eligible, backup_state,
	created_at, last_used_at`

// GetPasskey returns one credential by its id, and whether it exists.
//
// An assertion arrives carrying a credential id and nothing else to look up by, which is why this
// is the only lookup the assertion path needs — and why `credential_id` is the primary key.
func (s *Store) GetPasskey(credentialID string) (Passkey, bool, error) {
	row := s.db.QueryRow(`SELECT `+passkeyCols+` FROM passkeys WHERE credential_id = ?`, credentialID)
	p, err := scanPasskey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Passkey{}, false, nil
	}
	if err != nil {
		return Passkey{}, false, err
	}
	return p, true, nil
}

// ListPasskeys returns every credential, oldest first.
//
// Oldest first because the list is a HISTORY the admin reads to decide what to remove — the phone
// they registered a year ago and no longer own is the interesting row, and it belongs at the top
// rather than buried under whatever was added most recently.
func (s *Store) ListPasskeys() ([]Passkey, error) {
	rows, err := s.db.Query(`SELECT ` + passkeyCols + ` FROM passkeys ORDER BY created_at, credential_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Passkey
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TouchPasskey records a successful assertion: the authenticator's new signature counter and when
// it was used.
//
// BACKUP STATE IS REWRITTEN, and that is the library's own recommendation: BackupEligible "should
// NEVER change" and is a fact about the credential, while BackupState "can change" — a passkey can
// become backed up after it was made. Recording it keeps the row agreeing with the authenticator.
//
// THE SIGN COUNT IS STORED, NOT COMPARED HERE. Clone detection is the WebAuthn library's job and it
// happens before this is called; this records the accepted value. Splitting it that way keeps the
// store free of protocol judgement — a store method that silently declined to write a lower counter
// would be a security decision hidden in a setter.
func (s *Store) TouchPasskey(credentialID string, signCount uint32, backupState *bool, usedAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE passkeys SET sign_count = ?, backup_state = ?, last_used_at = ? WHERE credential_id = ?`,
		signCount, backupState, fmtTime(usedAt), credentialID)
	return err
}

// DeletePasskey removes one credential, returning whether a row went.
//
// Absent is NOT an error: two tabs removing the same credential, or a retry after a dropped
// response, both arrive here second and both should read as "it is gone" rather than as a failure
// the user has to interpret.
func (s *Store) DeletePasskey(credentialID string) (deleted bool, err error) {
	res, err := s.db.Exec(`DELETE FROM passkeys WHERE credential_id = ?`, credentialID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RenamePasskey changes the user-chosen label, returning whether a row went.
//
// The NAME is the only mutable field, deliberately. Everything else on the row is either the
// authenticator's (the key, the counter, the AAGUID) or a fact about when it was created — none of
// it is quince's to edit, and a setter that could reach them would be a way to make the record
// disagree with the credential it describes.
func (s *Store) RenamePasskey(credentialID, name string) (renamed bool, err error) {
	res, err := s.db.Exec(`UPDATE passkeys SET name = ? WHERE credential_id = ?`, name, credentialID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
