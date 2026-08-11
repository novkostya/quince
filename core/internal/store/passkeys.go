package store

import "time"

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
	AAGUID       []byte
	Transports   string // JSON array as reported at registration
	Name         string
	CreatedAt    time.Time
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
		   (credential_id, public_key, rp_id, sign_count, aaguid, transports, name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.CredentialID, p.PublicKey, p.RPID, p.SignCount, p.AAGUID, p.Transports, p.Name,
		fmtTime(p.CreatedAt))
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
