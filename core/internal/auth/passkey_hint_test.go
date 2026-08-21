package auth

import (
	"encoding/base64"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/novkostya/quince/core/internal/store"
)

// qn.13 slice 7 / G8 — THE HINT SELECTS, IT NEVER GRANTS (spec D2.2).
//
// WHAT IS TESTABLE HERE AND WHAT IS NOT, stated for the same reason `reauth_test.go` states it: the
// ceremony is, the assertion is not. Verifying a real signature needs a real authenticator, so what
// these prove is that a remembered credential changes only the OFFER — the allow-list quince builds
// and hands to the browser. That a hint cannot become authority is proven one layer down, by
// `FinishPasskeyAssertion` resolving the principal from the credential the assertion names, and by
// the library refusing an asserted id outside a non-empty allow-list (`webauthn/login.go:292-301`).

const hintRP = "quince.example.com"

// allowedIn pulls the allow-list out of whatever `BeginPasskeyAssertion` returned.
//
// THE RETURN IS `any`, which is the wire shape the handler passes through verbatim, so a test that
// wants the list has to assert its way in. Failing loudly on the type is deliberate: if the
// library's return type changes, this must stop compiling or stop passing rather than silently
// reading an empty list out of a shape it no longer understands — which would make every assertion
// below pass for the wrong reason.
func allowedIn(t *testing.T, assertion any) []protocol.CredentialDescriptor {
	t.Helper()
	ca, ok := assertion.(*protocol.CredentialAssertion)
	if !ok {
		t.Fatalf("assertion is %T, want *protocol.CredentialAssertion — the shape this reads changed", assertion)
	}
	return ca.Response.AllowedCredentials
}

// NO HINT IS THE DISCOVERABLE FLOW, and this is the control for every case below. An EMPTY
// allow-list means "platform, you choose", which is qn.6k's behaviour and what a browser that has
// never stored an id must still get.
func TestNoHintLeavesTheCeremonyDiscoverable(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, cred1, hintRP)
	cer := NewPasskeyCeremonies()

	assertion, key, err := svc.BeginPasskeyAssertion(cer, hintRP, "10.0.0.1", "")
	if err != nil {
		t.Fatalf("BeginPasskeyAssertion: %v", err)
	}
	if key == "" {
		t.Fatal("no ceremony key — the ceremony was not recorded")
	}
	if got := allowedIn(t, assertion); len(got) != 0 {
		t.Fatalf("allowCredentials = %d entries, want 0 — a ceremony with no hint must stay discoverable", len(got))
	}
}

// THE HINT REACHES THE OFFER, which is the whole mechanism: quince decides what is offered instead
// of asking the platform to choose (D2.2).
func TestAHintIsOfferedAsTheOnlyCredential(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, cred1, hintRP)
	cer := NewPasskeyCeremonies()

	hint := base64.RawURLEncoding.EncodeToString([]byte("credential-one"))
	assertion, _, err := svc.BeginPasskeyAssertion(cer, hintRP, "10.0.0.1", hint)
	if err != nil {
		t.Fatalf("BeginPasskeyAssertion: %v", err)
	}
	got := allowedIn(t, assertion)
	if len(got) != 1 {
		t.Fatalf("allowCredentials = %d entries, want exactly 1 — the hint names one credential", len(got))
	}
	if string(got[0].CredentialID) != "credential-one" {
		t.Fatalf("offered %q, want %q — the offer must be the credential the browser remembered",
			got[0].CredentialID, "credential-one")
	}
	if got[0].Type != protocol.PublicKeyCredentialType {
		t.Fatalf("descriptor type = %q, want %q", got[0].Type, protocol.PublicKeyCredentialType)
	}
}

// THE HINT IS ECHOED, NOT LOOKED UP — and this is the assertion that pins the ORACLE decision, so
// changing it should be a decision rather than a test edit.
//
// A credential id this install has never seen produces the SAME shaped offer as a real one. That is
// what makes the endpoint refuse to answer "does this quince know this passkey": a caller cannot
// tell a hit from a miss, because there is no lookup to hit. The alternative — falling back to
// discoverable for an unknown id — would make the empty list mean "no such credential" to anybody
// who can reach a PRE-AUTH endpoint.
//
// THE CONTROL IS THE TEST ABOVE. This passing on its own would also be consistent with the hint
// being ignored entirely; the pair is what shows the offer is built from the hint and only from it.
func TestAnUnknownHintIsOfferedJustLikeAKnownOne(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, cred1, hintRP)
	cer := NewPasskeyCeremonies()

	unknown := base64.RawURLEncoding.EncodeToString([]byte("no-such-credential"))
	assertion, _, err := svc.BeginPasskeyAssertion(cer, hintRP, "10.0.0.1", unknown)
	if err != nil {
		t.Fatalf("BeginPasskeyAssertion: %v", err)
	}
	got := allowedIn(t, assertion)
	if len(got) != 1 || string(got[0].CredentialID) != "no-such-credential" {
		t.Fatalf("allowCredentials = %v, want the requested id echoed — a lookup here would be a "+
			"credential-presence oracle on a pre-auth endpoint", got)
	}
}

// A MALFORMED HINT IS IGNORED, NOT REFUSED. It is a hint; the honest response to one that makes no
// sense is today's behaviour, not an error on a sign-in page nobody asked to see fail.
//
// THE EMPTY STRING IS IN HERE AS A BOUNDARY, NOT AS A MALFORMED VALUE — it is what
// `passkeyHintCredentialID()` returns for a browser holding qn.6k's boolean, so it is the ordinary
// upgrade path rather than an edge, and it must reach the discoverable flow like the rest.
//
// WHAT IS NOT TESTED HERE, AND CANNOT BE: `allowedForHint`'s `len(raw) == 0` arm. Measured against
// this encoder, the only string decoding to zero bytes without an error is `""`, which the earlier
// arm returns on — so no input reaches it, deleting it leaves this suite green, and that is stated
// at the arm itself rather than implied by a test that looks like it covers it.
func TestAMalformedHintFallsBackToDiscoverable(t *testing.T) {
	for _, tc := range []struct {
		name string
		hint string
	}{
		{"not base64url at all", "not!base64url"},
		{"a single character, which is an incomplete group", "A"},
		{"padded base64, which this encoding refuses", "Y3JlZA=="},
		{"the empty string, which is qn.6k's boolean arriving as no hint", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, st := newConfiguredService(t)
			seedPasskey(t, st, cred1, hintRP)
			cer := NewPasskeyCeremonies()

			assertion, _, err := svc.BeginPasskeyAssertion(cer, hintRP, "10.0.0.1", tc.hint)
			if err != nil {
				t.Fatalf("BeginPasskeyAssertion: %v — a hint that makes no sense must not fail a "+
					"sign-in page nobody asked to see fail", err)
			}
			if got := allowedIn(t, assertion); len(got) != 0 {
				t.Fatalf("allowCredentials = %v, want empty — a hint that names no credential must "+
					"leave the ceremony discoverable, not offer nothing", got)
			}
		})
	}
}

// THE HINT RECORDS NOTHING ON THE CEREMONY, which is what keeps D2's resolution order true: scope
// and identity come from `credential_id` AFTER the assertion, so a login ceremony has nothing to
// carry. A hint that leaked into the ceremony would be a claim the browser made about who it is.
func TestAHintDoesNotGiveTheCeremonyAScopeOrAHandle(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, cred1, hintRP)
	cer := NewPasskeyCeremonies()

	hint := base64.RawURLEncoding.EncodeToString([]byte("credential-one"))
	_, key, err := svc.BeginPasskeyAssertion(cer, hintRP, "10.0.0.1", hint)
	if err != nil {
		t.Fatalf("BeginPasskeyAssertion: %v", err)
	}
	pending, ok := cer.take(key, ceremonyAssert)
	if !ok {
		t.Fatal("the ceremony was not recorded as an assertion")
	}
	if pending.scope != (store.Scope{}) {
		t.Fatalf("ceremony scope = %+v, want the zero value — an assertion carries no scope (D2)", pending.scope)
	}
	if pending.handle != nil {
		t.Fatalf("ceremony handle = %v, want nil — the user handle arrives IN the assertion", pending.handle)
	}
}
