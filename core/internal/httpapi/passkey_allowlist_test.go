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
