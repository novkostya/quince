package httpapi

import (
	"net/http/httptest"
	"testing"
)

// THE FAILURE THE qn.6k SPEC CAUGHT, PINNED — and one it did not.
//
// The assertion pair is pre-auth by definition, so it must appear in every exact-path list that
// gates a pre-auth request. The spec named TWO — `authExempt` and `setupAllowed`. There are THREE:
// `csrfExempt` matters because no CSRF cookie exists before login.
//
// Each omission fails differently and none of them fails obviously:
//
//   - out of `authExempt`   → 401 on the endpoint whose whole job is to get you a session;
//   - out of `setupAllowed` → 503 storage_required on a storageless install, which is EXACTLY the
//     state where onboarding offers a passkey;
//   - out of `csrfExempt`   → a double-submit refusal talking about CSRF rather than about anything
//     the user did.
//
// Asserted BY EXACT PATH in both directions, because the lists are exact-path on purpose: a prefix
// would silently widen them every time a route is added.
func TestPasskeyAssertionIsInAllThreeExactPathLists(t *testing.T) {
	assertion := []string{
		"/api/auth/passkeys/login/begin",
		"/api/auth/passkeys/login/finish",
	}
	registration := []string{
		"/api/auth/passkeys/register/begin",
		"/api/auth/passkeys/register/finish",
	}

	for _, p := range assertion {
		r := httptest.NewRequest("POST", p, nil)
		if !authExempt(r) {
			t.Errorf("%s is not in authExempt — the endpoint that hands out sessions would 401", p)
		}
		if !setupAllowed(r) {
			t.Errorf("%s is not in setupAllowed — passkey sign-in would 503 on a storageless "+
				"install, which is where onboarding offers one", p)
		}
		if !csrfExempt(r) {
			t.Errorf("%s is not in csrfExempt — there is no CSRF cookie before login", p)
		}
	}

	// THE OTHER DIRECTION MATTERS AS MUCH. Registration needs a session by definition; if it ever
	// drifted into one of these lists it would become a pre-auth endpoint that writes a credential,
	// which is the one thing this rung must never allow.
	for _, p := range registration {
		r := httptest.NewRequest("POST", p, nil)
		if authExempt(r) {
			t.Errorf("%s IS in authExempt — registration would become pre-auth credential creation", p)
		}
		if setupAllowed(r) {
			t.Errorf("%s IS in setupAllowed", p)
		}
		if csrfExempt(r) {
			t.Errorf("%s IS in csrfExempt — registration is a mutation with a session, so it needs "+
				"the double-submit token", p)
		}
	}
}

// THE PASSWORD-MUTATION PAIR MUST BE IN NONE OF THE THREE — qn.6m D4, asserted here beside the
// passkey routes because it is the same mistake with a worse blast radius.
//
// Both need a session by definition. `PUT` in `authExempt` would be an unauthenticated password
// change — the exact thing contracts §1 promises `POST /api/auth/setup` can never be, arriving
// through a different door. `DELETE` in `authExempt` would let anyone make the install passwordless
// and then register their own credential.
//
// `csrfExempt` is the one that looks harmless and is not: these are state-changing requests made by
// an authenticated browser, which is precisely what the double-submit token exists for. Exempting
// them would make a password change reachable from any page the admin happens to visit.
//
// METHOD-SENSITIVE, and asserted that way. The lists key on METHOD + PATH, so `PUT` and `DELETE` on
// `/api/auth/password` are different entries from each other and from anything else on that path.
func TestPasswordMutationIsInNoneOfTheExactPathLists(t *testing.T) {
	for _, m := range []string{"PUT", "DELETE"} {
		r := httptest.NewRequest(m, "/api/auth/password", nil)
		if authExempt(r) {
			t.Errorf("%s /api/auth/password IS in authExempt — an unauthenticated caller could "+
				"change or remove the admin password", m)
		}
		if setupAllowed(r) {
			t.Errorf("%s /api/auth/password IS in setupAllowed — reachable on a storageless "+
				"install, which is a first-run state where no password may be mutated", m)
		}
		if csrfExempt(r) {
			t.Errorf("%s /api/auth/password IS in csrfExempt — a state-changing request from an "+
				"authenticated browser is exactly what double-submit protects", m)
		}
	}
}

// THE FIRST-RUN PAIR IS IN ALL THREE, AND THE AUTHENTICATED PAIR STILL IN NONE — qn.6m D5.
//
// These two look almost identical to the registration pair and belong on the opposite side of every
// list, which is exactly the confusion an exact-path assertion exists to prevent. Each omission
// fails differently and none fails obviously:
//
//   - out of `authExempt`   → 401 on the endpoint whose whole job is to work with no session;
//   - out of `setupAllowed` → 503 storage_required on a storageless install, which IS the
//     onboarding state where the passwordless option is offered;
//   - out of `csrfExempt`   → a double-submit refusal, because no CSRF cookie exists before a
//     session does.
//
// What makes them safe to exempt is NOT the list — it is the one-shot `Configured()` guard in the
// handler, which closes them the instant the install is claimed. The list only decides whether the
// guard is ever reached.
func TestFirstRunPasskeyPairIsInAllThreeExactPathLists(t *testing.T) {
	firstRun := []string{
		"/api/auth/setup/passkey/begin",
		"/api/auth/setup/passkey/finish",
	}
	authenticated := []string{
		"/api/auth/passkeys/register/begin",
		"/api/auth/passkeys/register/finish",
	}

	for _, p := range firstRun {
		r := httptest.NewRequest("POST", p, nil)
		if !authExempt(r) {
			t.Errorf("%s is not in authExempt — first run has no session to present", p)
		}
		if !setupAllowed(r) {
			t.Errorf("%s is not in setupAllowed — it would 503 on a storageless install, which is "+
				"the onboarding state this endpoint exists for", p)
		}
		if !csrfExempt(r) {
			t.Errorf("%s is not in csrfExempt — no CSRF cookie exists before a session does", p)
		}
	}

	// UNCHANGED AND RE-ASSERTED HERE. Adding a pre-auth registration path is precisely the edit that
	// could tempt somebody to "tidy" the authenticated pair in beside it.
	for _, p := range authenticated {
		r := httptest.NewRequest("POST", p, nil)
		if authExempt(r) || setupAllowed(r) || csrfExempt(r) {
			t.Errorf("%s drifted into an exact-path list — authenticated registration must stay out "+
				"of all three", p)
		}
	}
}

// THE REAUTH PAIR IS IN NONE OF THE THREE — qn.6n D3, gate G1.
//
// This is the assertion the whole of D3 rests on. The pair could have reused
// `passkeys/login/begin|finish`, which is in ALL three lists; the spec refused because giving the
// least-guarded routes in the system a second job whose purpose is gating privileged operations is
// the wrong trade. That refusal is only worth anything if the new routes stay out.
//
// Each membership would fail differently and none obviously:
//
//   - in `authExempt`   → re-authentication reachable with no session, so a proof could be minted
//     by anybody who can reach the address and hold an authenticator to it;
//   - in `setupAllowed` → reachable on a storageless install, which is a state that has no
//     credentials to re-present and is exactly where first run must not be shadowed;
//   - in `csrfExempt`   → a cross-site page could begin and finish a ceremony against a logged-in
//     browser, which is the mint step of a privileged operation.
//
// BY EXACT PATH AND BY METHOD, matching the lists' own shape: they are exact-path on purpose, and a
// prefix would silently widen them every time a route is added.
func TestTheReauthPairIsInNoneOfTheThreeExactPathLists(t *testing.T) {
	for _, p := range []string{"/api/auth/reauth/begin", "/api/auth/reauth/finish"} {
		r := httptest.NewRequest("POST", p, nil)
		if authExempt(r) {
			t.Errorf("%s IS in authExempt — a proof could be minted with no session at all", p)
		}
		if setupAllowed(r) {
			t.Errorf("%s IS in setupAllowed — reachable on an install with no credentials to present", p)
		}
		if csrfExempt(r) {
			t.Errorf("%s IS in csrfExempt — a cross-site page could mint a proof against a "+
				"logged-in browser", p)
		}
	}
}

// AND IT IS NOT REACHED BY A PREFIX FROM THE LOGIN PAIR. `/api/auth/reauth/*` and
// `/api/auth/passkeys/login/*` share no prefix today, so this cannot regress by accident — but the
// lists are the kind of thing a later change makes prefix-matched "to tidy them up", and that is the
// change this assertion exists to fail. A path that merely STARTS like an exempt one must not be
// exempt.
func TestTheListsDoNotMatchByPrefix(t *testing.T) {
	for _, p := range []string{
		"/api/auth/passkeys/login/begin/../../../reauth/begin",
		"/api/auth/passkeys/login/beginning",
		"/api/auth/reauth/begin/extra",
	} {
		r := httptest.NewRequest("POST", p, nil)
		if authExempt(r) || setupAllowed(r) || csrfExempt(r) {
			t.Errorf("%s matched a list — these are EXACT-path lists, not prefixes", p)
		}
	}
}

// THE ENROLMENT PAIR IS IN TWO OF THE THREE, AND ITS ABSENCE FROM THE THIRD IS A DECISION
// (qn.13 slice 9b-3, spec D4).
//
// It is in `authExempt` and `csrfExempt` for the login pair's reasons exactly: a scanner has no
// session, so requiring one makes the ceremony unreachable by the only person who performs it, and
// there is no CSRF cookie to double-submit before a session exists.
//
// IT IS DELIBERATELY NOT IN `setupAllowed`. That mode is a zero-storage first run, and enrolment
// cannot happen there: a QR is generated from a device page, and an install with no storage has no
// devices to generate one from. Adding it would widen the narrowest mode this product has for a
// ceremony that cannot succeed inside it.
//
// WHAT STANDS IN FOR CSRF HERE IS THE SECRET, and it is worth stating because the other entries in
// that list name different substitutes: `Configured()` for the first-run pair, SameSite plus the
// login limiter for the auth POSTs. A cross-site forgery against enrolment achieves nothing without
// a single-use value the admin generated and handed to somebody — and quince can revoke it.
func TestTheEnrolmentPairIsPreAuthButNotASetupModeRoute(t *testing.T) {
	enrolment := []string{
		"/api/enrol/passkey/begin",
		"/api/enrol/passkey/finish",
	}
	for _, p := range enrolment {
		r := httptest.NewRequest("POST", p, nil)
		if !authExempt(r) {
			t.Errorf("%s is not in authExempt — no scanner has a session, so the ceremony would 401", p)
		}
		if !csrfExempt(r) {
			t.Errorf("%s is not in csrfExempt — there is no CSRF cookie before a session exists, so "+
				"this would refuse with a message about CSRF rather than about anything the user did", p)
		}
		if setupAllowed(r) {
			t.Errorf("%s IS in setupAllowed — that mode is a zero-storage first run, which has no "+
				"devices and therefore no QR this could be answering", p)
		}
	}
}
