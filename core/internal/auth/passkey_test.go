package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// AN rpId MUST BE A DOMAIN — spec story 4. A bare IP cannot be one, and that is a protocol
// constraint rather than a certificate one, so no amount of TLS work rescues the self-signed-at-an-IP
// tier. Refusing at registration beats minting a credential that can never be used.
func TestRelyingPartyRefusesAddressesThatCannotBeOne(t *testing.T) {
	for _, rp := range []string{"", "192.0.2.10", "2001:db8::1", "quince"} {
		if _, err := relyingParty(rp); err == nil {
			t.Errorf("relyingParty(%q) accepted an address that cannot be an rpId", rp)
		} else {
			var un ErrUnsupportedRPID
			if !errors.As(err, &un) {
				t.Errorf("relyingParty(%q) = %v, want ErrUnsupportedRPID", rp, err)
			}
		}
	}
	// A real name, and localhost, are usable. localhost because browsers treat it as a secure
	// context and it is how quince is reached in development.
	for _, rp := range []string{"quince.example.com", "localhost"} {
		if _, err := relyingParty(rp); err != nil {
			t.Errorf("relyingParty(%q) refused a usable rpId: %v", rp, err)
		}
	}
}

// THE HANDLE IS BUILT PER CEREMONY, NOT ONCE, and the origin follows the rpId. This is the one
// place the implementation departs from every example in the library, so it is asserted rather than
// left to the file comment: two different names must produce two different relying parties.
func TestRelyingPartyIsPerRPIDRatherThanASingleton(t *testing.T) {
	a, err := relyingParty(rpHome)
	if err != nil {
		t.Fatalf("relyingParty(%s): %v", rpHome, err)
	}
	b, err := relyingParty(rpOther)
	if err != nil {
		t.Fatalf("relyingParty(%s): %v", rpOther, err)
	}
	if a.Config.RPID == b.Config.RPID {
		t.Fatalf("both relying parties claim %q — the handle is being shared across domains", a.Config.RPID)
	}
	if got, want := a.Config.RPOrigins, []string{"https://" + rpHome}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("origins = %v, want %v — the origin must follow the rpId", got, want)
	}
}

// The user handle must be STABLE, because the authenticator stores it and presents it back. Minting
// a second one would orphan every credential registered under the first.
func TestUserHandleIsMintedOnceAndStable(t *testing.T) {
	st := newResetStore(t)

	first, err := userHandle(st)
	if err != nil {
		t.Fatalf("userHandle: %v", err)
	}
	if len(first) != 32 {
		t.Errorf("handle is %d bytes, want 32", len(first))
	}
	second, err := userHandle(st)
	if err != nil {
		t.Fatalf("userHandle again: %v", err)
	}
	if string(first) != string(second) {
		t.Error("a second call minted a different handle — every existing credential would be orphaned")
	}
}

// AND IT MUST SURVIVE A PASSWORD RESET'S SIBLING OPERATIONS but NOT outlive `quince auth reset`'s
// intent. Reset clears credentials, so the handle may legitimately persist — what must never happen
// is the handle changing while credentials still reference it.
func TestUserHandleSurvivesAPasswordChange(t *testing.T) {
	st := newResetStore(t)
	before, err := userHandle(st)
	if err != nil {
		t.Fatalf("userHandle: %v", err)
	}
	if err := st.SetSetting(settingPasswordHash, "a-different-hash"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	after, err := userHandle(st)
	if err != nil {
		t.Fatalf("userHandle after password change: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the handle changed with the password — it must not be derived from it")
	}
}

// A CHALLENGE IS SINGLE USE. Taking it twice must fail the second time, or a captured response
// could be replayed against a second attempt.
func TestCeremonyIsSingleUse(t *testing.T) {
	cer := NewPasskeyCeremonies()
	key, err := cer.put(&webauthn.SessionData{Challenge: "c"}, rpHome)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	if _, ok := cer.take(key); !ok {
		t.Fatal("first take failed")
	}
	if _, ok := cer.take(key); ok {
		t.Error("second take succeeded — the challenge is replayable")
	}
}

// AND IT EXPIRES. The user is looking at a Face ID sheet; anything longer than the TTL is an
// abandoned ceremony or a replay, not a slow human.
func TestCeremonyExpires(t *testing.T) {
	cer := NewPasskeyCeremonies()
	now := time.Now().UTC()
	cer.now = func() time.Time { return now }

	key, err := cer.put(&webauthn.SessionData{Challenge: "c"}, rpHome)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	now = now.Add(challengeTTL + time.Second)
	if _, ok := cer.take(key); ok {
		t.Error("an expired ceremony was accepted")
	}
}

// The ceremony REMEMBERS ITS OWN rpId, so a finish arriving on a different tier can be refused
// rather than storing a credential the authenticator signed for another domain.
func TestCeremonyCarriesItsRPID(t *testing.T) {
	cer := NewPasskeyCeremonies()
	key, err := cer.put(&webauthn.SessionData{Challenge: "c"}, rpHome)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := cer.take(key)
	if !ok {
		t.Fatal("take failed")
	}
	if got.rpID != rpHome {
		t.Errorf("ceremony rpID = %q, want %q", got.rpID, rpHome)
	}
}

// The admin's WebAuthn name must be THE SAME STRING the login form's anchor uses (quince#819).
// A keychain keys a credential on (origin, username), so a mismatch would file the passkey as a
// second identity beside the password rather than beside it.
func TestPasskeyUserNameMatchesTheLoginAnchor(t *testing.T) {
	u := passkeyUser{handle: []byte("h")}
	if u.WebAuthnName() != "quince-admin" || u.WebAuthnDisplayName() != "quince-admin" {
		t.Errorf("name=%q display=%q, want quince-admin for both — quince#819's anchor",
			u.WebAuthnName(), u.WebAuthnDisplayName())
	}
}

// Beginning a registration must produce options AND a ceremony key, and must not fail on a box with
// no credentials yet — the first registration is the one that matters most.
func TestBeginRegistrationOnAFreshBox(t *testing.T) {
	st := newResetStore(t)
	cer := NewPasskeyCeremonies()

	options, key, err := BeginPasskeyRegistration(st, cer, rpHome)
	if err != nil {
		t.Fatalf("BeginPasskeyRegistration: %v", err)
	}
	if options == nil {
		t.Error("no creation options returned")
	}
	if key == "" {
		t.Error("no ceremony key returned")
	}
	if _, ok := cer.take(key); !ok {
		t.Error("the returned key does not resolve to a stored ceremony")
	}
}

// AND IT REFUSES ON A TIER THAT CANNOT SUPPORT PASSKEYS, before any ceremony is stored — story 4.
func TestBeginRegistrationRefusesAnIPAddress(t *testing.T) {
	st := newResetStore(t)
	cer := NewPasskeyCeremonies()

	_, _, err := BeginPasskeyRegistration(st, cer, "192.0.2.10")

	var un ErrUnsupportedRPID
	if !errors.As(err, &un) {
		t.Fatalf("got %v, want ErrUnsupportedRPID", err)
	}
	if len(cer.in) != 0 {
		t.Error("a ceremony was stored for an address that can never complete one")
	}
}

// A finish arriving on a DIFFERENT domain than the ceremony began on is refused, and the refusal
// names both. Moving between access tiers mid-ceremony is the real case.
func TestFinishRefusesACeremonyFromAnotherDomain(t *testing.T) {
	st := newResetStore(t)
	cer := NewPasskeyCeremonies()

	_, key, err := BeginPasskeyRegistration(st, cer, rpHome)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, err = FinishPasskeyRegistration(st, cer, key, "phone", rpOther, nil, time.Now().UTC())

	var mm ErrRPIDMismatch
	if !errors.As(err, &mm) {
		t.Fatalf("got %v, want ErrRPIDMismatch", err)
	}
	if mm.Registered != rpHome || mm.Presented != rpOther {
		t.Errorf("got %q → %q, want %q → %q", mm.Registered, mm.Presented, rpHome, rpOther)
	}
}

// An unknown or spent ceremony key is a named refusal rather than a panic or a generic 500.
func TestFinishWithNoCeremonyIsNamed(t *testing.T) {
	st := newResetStore(t)
	cer := NewPasskeyCeremonies()

	_, err := FinishPasskeyRegistration(st, cer, "not-a-key", "phone", rpHome, nil, time.Now().UTC())
	if !errors.Is(err, ErrNoChallenge) {
		t.Fatalf("got %v, want ErrNoChallenge", err)
	}
}
