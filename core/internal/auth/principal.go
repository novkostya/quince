package auth

import "github.com/novkostya/quince/core/internal/store"

// Principal is WHO a request is acting as.
//
// THIS TYPE IS THE POINT OF qn.13'S FIRST SLICE. Until it existed, quince had authentication and
// no notion of a caller: `sessions_auth` recorded a session's lifetime and nothing about its
// origin, so "authenticated" was the whole of what any handler could know. Every capability check
// this rung goes on to add is downstream of there being something to check.
//
// IT CARRIES NO SCOPE YET, DELIBERATELY. Scope lives on the credential (spec D2) and arrives in
// slice 4. This slice establishes only that a request has an identity and that quince can name it;
// nothing reads it yet, and no behaviour changes.
type Principal struct {
	// CredentialID is the passkey this session was created by, or "" for a password login.
	//
	// "" MEANS THE ADMIN, which is the hazard 0014_session_principal.sql names: a default that
	// GRANTS. It is honest today — a password login genuinely is the admin, because a password is
	// the admin's and there is nothing else to be. It stops being sufficient the moment slice 4
	// gives credentials a scope, which is why that slice and this one are ordered.
	CredentialID string
}

// PrincipalOf reads the principal a session was created by.
//
// A FUNCTION RATHER THAN A FIELD, so there is exactly one place that turns "no credential" into
// "the admin". A reader looking for how that default is applied finds it here and nowhere else.
func PrincipalOf(sess store.AuthSession) Principal {
	if sess.CredentialID == nil {
		return Principal{}
	}
	return Principal{CredentialID: *sess.CredentialID}
}

// IsPasswordLogin reports whether this principal arrived without a credential.
//
// NAMED FOR WHAT IS TRUE rather than for what it implies. "IsAdmin" would be the tempting spelling
// and would be a claim this slice cannot support: it is a fact about how the session was created,
// and only slice 4's scope column makes it a fact about authority.
func (p Principal) IsPasswordLogin() bool { return p.CredentialID == "" }
