package auth

import "github.com/novkostya/quince/core/internal/store"

// The console escape hatch — qn.6k slice 2, Operator ruling on quince#657.
//
// WHY IT SHIPS BEFORE PASSKEYS DO, and this is the ruling rather than an ordering preference: a
// passkey that is the only way in, on a phone that is lost, locks the user out of THEIR OWN BACKUPS.
// So the thing that undoes a credential exists before anything can create one. Root on the box is
// already total access, so this adds no attack surface; it mirrors the break-glass shape canon uses
// for the Operator's Mac.
//
// It lives in this package rather than in the CLI because `settingPasswordHash` does, and a reset
// that names the key from the outside is a second place to update when it moves.

// ResetResult is what a reset actually did, so the caller can report it rather than assert it.
// Every field is a COUNT OR A FACT, never a bool standing in for one: "cleared 2 passkeys" tells an
// operator the box had credentials they may not have known about, where "done" tells them nothing.
type ResetResult struct {
	HadPassword bool // a password existed and was removed
	Passkeys    int  // credentials removed
	Sessions    int  // live sessions invalidated
}

// Reset clears the admin password, every passkey, and every live session, returning what went.
//
// ALL THREE, AND THE THIRD IS NOT OPTIONAL. `Service.Authenticate` never consults the password — it
// checks the session row and its expiry and nothing else — so clearing the password alone leaves a
// live cookie passing `authGuard` and reaching every protected route, while the UI reads
// `needs_setup` off `Status()`, which does short-circuit. The reset would look complete and leave
// the box authenticated.
//
// PASSKEYS GO WHOLESALE, which is ruled: a credential list the locked-out user cannot reach is not
// recovery, and leaving them would leave the box authenticatable by the phone that is, by
// hypothesis, the thing that was lost.
//
// NOT ATOMIC ACROSS THE THREE, and that is a deliberate accepted limit rather than an oversight.
// They are three statements on one local SQLite file, run by hand on the host with the daemon
// typically stopped; wrapping them in a transaction would make a partial failure invisible rather
// than impossible. As written, a failure stops at the first error and the caller reports what had
// already gone — which is the recoverable shape, because re-running is a no-op on what is already
// clear. Revisit if this ever gains a caller that is not a human at a console.
func Reset(st *store.Store) (ResetResult, error) {
	var out ResetResult

	had, err := st.DeleteSetting(settingPasswordHash)
	if err != nil {
		return out, err
	}
	out.HadPassword = had

	// Order matters for the failure story, not for correctness: password first, so that a failure
	// in either step below still leaves the box unable to accept the old password.
	n, err := st.DeleteAllPasskeys()
	if err != nil {
		return out, err
	}
	out.Passkeys = n

	n, err = st.DeleteAllAuthSessions()
	if err != nil {
		return out, err
	}
	out.Sessions = n

	return out, nil
}
