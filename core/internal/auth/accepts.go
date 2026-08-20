package auth

// WHAT WOULD SATISFY THIS — qn.6o slice 2, D1. Operator ruling, 2026-08-14:
//
//	"Would it be feasible to make POST /api/auth/passkeys/register/begin with no credentials
//	 first, which can return specific error that challenge is required, with possible challenge
//	 types in error response body? Then web app does the challenge and repeats the request?"
//
// The refusal names the factors that WOULD work, so no client has to encode which factors an
// operation accepts. That rule now has one copy, on the side that enforces it.
//
// IT IS GUIDANCE, AND NEVER A CONTROL (D2). Every rule is still enforced where the credential is
// actually presented — `RequirePresent` for rules 1 and 3, the subject comparison in
// `RemovePassword` / `RemovePasskey` for rule 2. A client that ignores this list and offers the
// password for `remove_password` is refused exactly as before. If acceptability were DECIDED here,
// the guard would have moved to the client, which is the thing this shape exists to avoid.
//
// IT DISCLOSES NOTHING NEW (D3). `GET /api/auth/passkeys` already gives the same session
// `has_password` and the entire credential list, and the caller is the admin. Said out loud because
// an error body that describes the install invites the question.

// The factor names as they appear on the wire. Two, matching the two things `Presented` carries.
const (
	FactorPassword = "password"
	FactorPasskey  = "passkey"
)

// Accepts lists the factors that could satisfy this operation, at THIS address, on the credentials
// this install actually holds — not what the operation permits in principle.
//
// IT IS NEVER EMPTY; IT IS nil (D4). *"Nothing this install holds can authorise this at this
// address"* already has a shape — `ErrLastCredential`, carrying a sentence that names the remedy —
// and reusing it beats `accepts: []`, which would make the client responsible for turning emptiness
// back into an explanation and would put a prompt with nothing in it one bug away. So callers omit
// the field when this returns nothing, and NO CLIENT EVER MEETS AN EMPTY CHALLENGE.
//
// RULE 2's TWO EXCLUSIONS ARE APPLIED HERE, and they are the reason this cannot be a lookup table:
// the password is absent for `remove_password`, and a target passkey does not count itself for
// `remove_passkey`.
func (s *Service) Accepts(op ProofOperation, rpID, target string) ([]string, error) {
	if !op.valid() {
		// NOT AN ERROR, AND DELIBERATELY SO. This is advisory copy on a refusal that has already
		// been decided; an unknown operation means the caller is not one of the five emitters, and
		// answering "nothing" degrades to today's field-less body rather than turning a 401 into a
		// 500. The operation is validated where it is ENFORCED, which is `Mint` and `Consume`.
		return nil, nil
	}

	var out []string

	// THE PASSWORD, EXCEPT WHERE IT IS THE THING BEING REMOVED. Rule 2 says removing a credential
	// requires a DIFFERENT one, so the password can never authorise its own removal — and offering
	// it here would send a user to type a password that `RemovePassword`'s subject comparison is
	// guaranteed to reject.
	if op != OpRemovePassword {
		_, hasPassword, err := s.store.GetSetting(settingPasswordHash)
		if err != nil {
			return nil, err
		}
		if hasPassword {
			out = append(out, FactorPassword)
		}
	}

	// A PASSKEY THAT COULD ACTUALLY ASSERT HERE. Bound to the address the request arrived on,
	// because a credential registered for another rpId cannot sign for this one — the same rule
	// `provable` enforces, and G4 is the test that keeps the two from drifting.
	//
	// FOR A REMOVAL, THE TARGET DOES NOT COUNT ITSELF. `allowedForRemoval` already draws exactly
	// this line for the ceremony's allow-list; asking it here means one function decides which
	// credentials may prove a removal, rather than two spellings that can disagree.
	if op == OpRemovePasskey {
		allowed, err := allowedForRemoval(s.store, rpID, target)
		if err != nil {
			return nil, err
		}
		if len(allowed) > 0 {
			out = append(out, FactorPasskey)
		}
		return out, nil
	}

	// ADMIN CREDENTIALS ONLY, and this is the passwordless lockout (spec D6). This predicate
	// decides whether a passkey can stand in for the password — an ADMIN question. Counting
	// every row would let an install with one scoped credential and no admin passkey remove
	// its admin password, after which the admin is locked out, the scoped holder cannot
	// administer anything by construction, and only `quince auth reset` gets back in.
	creds, err := adminCredentials(s.store, rpID)
	if err != nil {
		return nil, err
	}
	if len(creds) > 0 {
		out = append(out, FactorPasskey)
	}
	return out, nil
}
