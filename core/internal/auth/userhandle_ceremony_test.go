package auth

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/novkostya/quince/core/internal/store"
)

// THE CEREMONY CARRIES THE IDENTITY IT WAS BEGUN UNDER (quince#1440).
//
// MEASURED ON HARDWARE BEFORE THIS FIX, 2026-08-21: every first enrolment for a device failed with
// the library's `ID mismatch for User and Session`, AFTER the household member had completed Face ID
// and their phone had stored a credential quince then refused. The daemon logged it; the person saw
// a generic failure and was left holding an orphan credential.
//
// THE CAUSE IS A DERIVATION THAT IS NOT STABLE. `handleForScope` reuses a stored handle when the
// device already has a credential and otherwise MINTS a fresh one, persisting nothing — so for the
// FIRST enrolment, begin and finish each invented a different `user.id`. There could never be a
// second, because the first is what stores the handle a second would reuse.
//
// WHY THE TEST IS AT THE CEREMONY RECORD RATHER THAN END TO END. Completing a registration needs a
// real authenticator, which this package does not have. What CAN be pinned is the property whose
// absence caused it: what the browser was told at begin is what finish will use.

func handleFromOptions(t *testing.T, options any) []byte {
	t.Helper()
	creation, ok := options.(*protocol.CredentialCreation)
	if !ok {
		t.Fatalf("options are %T", options)
	}
	// `User.ID` is `any` on the wire type, and the library fills it with the []byte the user
	// object returned. Asserted rather than converted, so a shape change fails here loudly.
	id, ok := creation.Response.User.ID.(protocol.URLEncodedBase64)
	if !ok {
		t.Fatalf("user.id is %T, want protocol.URLEncodedBase64", creation.Response.User.ID)
	}
	return id
}

// THE FAILING CASE, DIRECTLY: a device with NO credential yet.
func TestAScopedCeremonyRecordsTheHandleItOffered(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.store.UpsertDeviceIdentity(store.DeviceIdentityRow{
		UDID: enrolDevice, Name: "Household iPhone", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cer := NewPasskeyCeremonies()

	options, key, err := BeginPasskeyRegistration(svc.store, cer, rpHome,
		store.DeviceScope(enrolDevice), DiscloseNothing())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	offered := handleFromOptions(t, options)
	if len(offered) == 0 {
		t.Fatal("the ceremony offered an empty user.id")
	}

	pending, ok := cer.in[key]
	if !ok {
		t.Fatal("no ceremony was recorded")
	}
	if !bytes.Equal(pending.handle, offered) {
		t.Fatalf("the ceremony recorded %q but offered %q — finish would use the wrong identity",
			base64.RawURLEncoding.EncodeToString(pending.handle),
			base64.RawURLEncoding.EncodeToString(offered))
	}

	// AND THE DERIVATION IS GENUINELY UNSTABLE, which is what makes recording it necessary rather
	// than tidy. This is the control: without it the assertion above would pass just as happily
	// against a `handleForScope` that was already deterministic, and would prove nothing.
	again, err := handleForScope(svc.store, store.DeviceScope(enrolDevice))
	if err != nil {
		t.Fatalf("handleForScope: %v", err)
	}
	if bytes.Equal(again, offered) {
		t.Fatal("handleForScope is stable for an uncredentialed device — this test no longer " +
			"describes the defect it was written for, so re-derive whether the fix is still needed")
	}
}

// THE ADMIN PATH IS UNAFFECTED, and its handle IS stable — it lives in `settings`. Asserted so the
// fix cannot be read as changing the case that always worked.
func TestTheAdminHandleIsStableAcrossDerivations(t *testing.T) {
	svc, _ := newTestAuth(t)

	first, err := handleForScope(svc.store, store.AdminScope())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := handleForScope(svc.store, store.AdminScope())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the admin's handle is not stable, which would break first-run registration too")
	}
}

// AN ASSERTION CEREMONY CARRIES NO HANDLE, because the user handle arrives IN the assertion and
// scope resolves from `credential_id` afterwards (D2). Pinned so nobody "fixes" the nil.
func TestAnAssertionCeremonyCarriesNoHandle(t *testing.T) {
	cer := NewPasskeyCeremonies()
	key, err := cer.put(&webauthn.SessionData{Challenge: "c"}, rpHome, ceremonyAssert, store.Scope{}, nil)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if pending := cer.in[key]; pending.handle != nil {
		t.Fatalf("an assertion ceremony recorded a handle: %v", pending.handle)
	}
}
