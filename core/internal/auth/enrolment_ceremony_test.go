package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/novkostya/quince/core/internal/store"
)

// THE ENROLMENT CEREMONY's refusals and its scope binding (spec D4, D4.1).
//
// WHAT IS NOT TESTED HERE, AND WHY. There is no synthetic authenticator in this package — every
// existing test of `FinishPasskeyRegistration` drives its refusal paths and passes `nil` for the
// request. So a full happy-path finish cannot be exercised, and that is declared rather than faked:
// a stub authenticator would assert that quince agrees with a fixture I wrote, not that it agrees
// with a phone. What IS testable is every path where the ceremony decides something, and those are
// where the confinement lives.

func enrolService(t *testing.T) (*Service, *Enrolments, *time.Time) {
	t.Helper()
	svc, clock := newTestAuth(t)
	enr := NewEnrolments()
	enr.now = func() time.Time { return *clock }
	if err := svc.store.UpsertDeviceIdentity(store.DeviceIdentityRow{
		UDID: enrolDevice, Name: "Household iPhone", UpdatedAt: *clock,
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return svc, enr, clock
}

func mintFor(t *testing.T, enr *Enrolments, udid string) (string, Enrolment) {
	t.Helper()
	tok, en, err := enr.Mint(store.DeviceScope(udid))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return tok, en
}

// THE SCOPE COMES OFF THE SECRET, and nothing in the call names a device. Asserted through what the
// platform is asked to SHOW, because that is the observable half of D2.1 — a scoped credential must
// carry its device's name, never the admin constant.
func TestEnrolmentCeremonyTakesItsScopeFromTheSecret(t *testing.T) {
	svc, enr, _ := enrolService(t)
	tok, _ := mintFor(t, enr, enrolDevice)

	options, _, err := svc.BeginEnrolment(NewPasskeyCeremonies(), enr, rpHome, tok, "10.0.0.1")
	if err != nil {
		t.Fatalf("BeginEnrolment: %v", err)
	}
	creation, ok := options.(*protocol.CredentialCreation)
	if !ok {
		t.Fatalf("options are %T, want *protocol.CredentialCreation", options)
	}
	if creation.Response.User.Name == adminUsername {
		t.Fatalf("the enrolment ceremony offered the ADMIN username %q — D2.1 forbids it, and a "+
			"household member would be told they are the admin", adminUsername)
	}
	if creation.Response.User.Name != "Household iPhone" {
		t.Fatalf("user.name = %q, want the device's name", creation.Response.User.Name)
	}
	// D4.1, at the ceremony rather than at the helper: an exclusion list here would disclose the
	// admin's credential ids to whoever scanned the QR.
	if n := len(creation.Response.CredentialExcludeList); n != 0 {
		t.Fatalf("the enrolment ceremony disclosed %d credential id(s) pre-authentication", n)
	}
}

// BEGIN CHECKS, IT DOES NOT SPEND. A failed Face ID must not cost the household member a trip back
// to the admin (D4).
func TestBeginEnrolmentDoesNotConsumeTheSecret(t *testing.T) {
	svc, enr, _ := enrolService(t)
	tok, _ := mintFor(t, enr, enrolDevice)

	for i := range 3 {
		if _, _, err := svc.BeginEnrolment(NewPasskeyCeremonies(), enr, rpHome, tok, "10.0.0.1"); err != nil {
			t.Fatalf("BeginEnrolment #%d: %v", i+1, err)
		}
	}
	if _, err := enr.Check(tok); err != nil {
		t.Fatalf("the secret is no longer usable after three begins: %v", err)
	}
}

func TestBeginEnrolmentRefusesEveryDeadSecret(t *testing.T) {
	for _, tc := range []struct {
		name string
		kill func(t *testing.T, enr *Enrolments, tok string, en Enrolment, clock *time.Time)
		want error
	}{
		{"unknown", func(*testing.T, *Enrolments, string, Enrolment, *time.Time) {}, ErrEnrolmentUnknown},
		{"expired", func(_ *testing.T, _ *Enrolments, _ string, _ Enrolment, clock *time.Time) {
			*clock = clock.Add(enrolmentTTL + time.Second)
		}, ErrEnrolmentExpired},
		{"revoked", func(t *testing.T, enr *Enrolments, _ string, en Enrolment, _ *time.Time) {
			if err := enr.Revoke(enrolDevice, en.ID); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
		}, ErrEnrolmentRevoked},
		{"spent", func(t *testing.T, enr *Enrolments, tok string, _ Enrolment, _ *time.Time) {
			if _, err := enr.Spend(tok); err != nil {
				t.Fatalf("Spend: %v", err)
			}
		}, ErrEnrolmentSpent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, enr, clock := enrolService(t)
			tok, en := mintFor(t, enr, enrolDevice)

			// THE CONTROL: live, this same call succeeds. Without it a refusal proves nothing —
			// a ceremony that refused everything would pass every case in this table.
			if _, _, err := svc.BeginEnrolment(NewPasskeyCeremonies(), enr, rpHome, tok, "10.0.0.1"); err != nil {
				t.Fatalf("control: a live secret was refused: %v", err)
			}
			presented := tok
			if tc.name == "unknown" {
				presented = "not-a-real-secret"
			}
			tc.kill(t, enr, tok, en, clock)

			_, _, err := svc.BeginEnrolment(NewPasskeyCeremonies(), enr, rpHome, presented, "10.0.0.1")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// THE SHARPEST ONE: the secret is RE-READ at finish, so an admin who revokes a QR while the phone
// sits on the unlock sheet is obeyed.
//
// THE ERROR IS THE PROOF OF ORDERING. The ceremony key here is real, so if `FinishEnrolment` checked
// the secret AFTER the registration it would fail with a registration error instead. Getting
// `ErrEnrolmentRevoked` is what says the secret is consulted first.
func TestFinishEnrolmentRereadsTheSecretAndRefusesARevokedOne(t *testing.T) {
	svc, enr, _ := enrolService(t)
	tok, en := mintFor(t, enr, enrolDevice)
	cer := NewPasskeyCeremonies()

	_, key, err := svc.BeginEnrolment(cer, enr, rpHome, tok, "10.0.0.1")
	if err != nil {
		t.Fatalf("BeginEnrolment: %v", err)
	}
	if err := enr.Revoke(enrolDevice, en.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, _, _, err = svc.FinishEnrolment(cer, enr, key, rpHome, tok, nil, time.Now().UTC(), "", "10.0.0.2")
	if !errors.Is(err, ErrEnrolmentRevoked) {
		t.Fatalf("got %v, want ErrEnrolmentRevoked", err)
	}
	// AND NOTHING WAS WRITTEN. A refusal that still stored a credential would be the worst of both.
	creds, err := svc.store.ListPasskeys()
	if err != nil {
		t.Fatalf("ListPasskeys: %v", err)
	}
	if len(creds) != 0 {
		t.Fatalf("%d credential(s) stored despite the refusal", len(creds))
	}
}

// PRE-AUTH AND METERED. `BeginSetupPasskey`'s reason applies and one more besides: this path's gate
// is a 256-bit value in a URL, so the bucket is also what bounds guessing at it.
func TestBeginEnrolmentIsRateLimited(t *testing.T) {
	svc, enr, _ := enrolService(t)
	tok, _ := mintFor(t, enr, enrolDevice)

	var lastErr error
	for range 10 {
		_, _, lastErr = svc.BeginEnrolment(NewPasskeyCeremonies(), enr, rpHome, tok, "10.0.0.9")
	}
	if !errors.Is(lastErr, ErrRateLimited) {
		t.Fatalf("got %v after ten attempts from one address, want ErrRateLimited", lastErr)
	}
	// THE LIMIT IS PER ADDRESS, not global — another phone in the household must not be locked out
	// by the first one's retries.
	if _, _, err := svc.BeginEnrolment(NewPasskeyCeremonies(), enr, rpHome, tok, "10.0.0.10"); err != nil {
		t.Fatalf("a different address was refused: %v", err)
	}
}

// FINISH IS METERED TOO, and this is the twin the review asked for (quince#1426).
//
// Begin's limiter claimed to bound guessing at the secret. It did not while this door took the same
// secret unmetered — an attacker would have used this one, and it does MORE work per request: a full
// attestation verification runs before anything can refuse.
func TestFinishEnrolmentIsRateLimited(t *testing.T) {
	svc, enr, _ := enrolService(t)
	tok, _ := mintFor(t, enr, enrolDevice)

	// THE CONTROL: one call from a fresh address is NOT rate-limited. It fails for a ceremony
	// reason instead, which is what proves the limiter is not simply refusing everything.
	_, _, _, err := svc.FinishEnrolment(NewPasskeyCeremonies(), enr, "no-such-ceremony",
		rpHome, tok, nil, time.Now().UTC(), "", "10.0.0.20")
	if errors.Is(err, ErrRateLimited) {
		t.Fatalf("control: the first call from a fresh address was rate-limited")
	}

	var lastErr error
	for range 10 {
		_, _, _, lastErr = svc.FinishEnrolment(NewPasskeyCeremonies(), enr, "no-such-ceremony",
			rpHome, tok, nil, time.Now().UTC(), "", "10.0.0.21")
	}
	if !errors.Is(lastErr, ErrRateLimited) {
		t.Fatalf("got %v after ten attempts from one address, want ErrRateLimited", lastErr)
	}
}

// THE CEREMONY'S SCOPE IS COMPARED AT FINISH, so a registration begun for one device cannot be
// finished for another (quince#1426 review).
//
// UNREACHABLE THROUGH ANY SHIPPED CALLER, and that is stated rather than hidden: both call sites
// resolve the scope identically, so this refusal fires for nobody today. It exists so a third
// caller cannot mint a credential confined to a device its ceremony was never begun for — the same
// shape as `store.Scope` and `Disclosure`, which also cost a line and remove a class of mistake.
func TestARegistrationCannotBeFinishedUnderADifferentScope(t *testing.T) {
	svc, _, _ := enrolService(t)
	const other = "DEVICE-B"
	if err := svc.store.UpsertDeviceIdentity(store.DeviceIdentityRow{
		UDID: other, Name: "Other iPhone", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// THE CONTROL: finishing under the SAME scope gets past this guard, and fails later on the
	// authenticator response — which is the point, since there is no authenticator here.
	cer := NewPasskeyCeremonies()
	_, key, err := BeginPasskeyRegistration(svc.store, cer, rpHome, store.DeviceScope(enrolDevice), DiscloseNothing())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	_, err = FinishPasskeyRegistration(svc.store, cer, key, "phone", rpHome, nil, time.Now().UTC(),
		store.DeviceScope(enrolDevice))
	if errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("control: the matching scope was refused as a mismatch")
	}

	cer2 := NewPasskeyCeremonies()
	_, key2, err := BeginPasskeyRegistration(svc.store, cer2, rpHome, store.DeviceScope(enrolDevice), DiscloseNothing())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	_, err = FinishPasskeyRegistration(svc.store, cer2, key2, "phone", rpHome, nil, time.Now().UTC(),
		store.DeviceScope(other))
	if !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("got %v, want ErrScopeMismatch — a ceremony begun for one device was finished for another", err)
	}
	// AND THE ADMIN DIRECTION, which is the one with teeth: a scoped ceremony must not be
	// completable as an admin credential.
	cer3 := NewPasskeyCeremonies()
	_, key3, err := BeginPasskeyRegistration(svc.store, cer3, rpHome, store.DeviceScope(enrolDevice), DiscloseNothing())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	_, err = FinishPasskeyRegistration(svc.store, cer3, key3, "phone", rpHome, nil, time.Now().UTC(),
		store.AdminScope())
	if !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("got %v, want ErrScopeMismatch — a scoped ceremony was finishable as ADMIN", err)
	}
}
