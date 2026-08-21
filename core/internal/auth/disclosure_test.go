package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/novkostya/quince/core/internal/store"
)

// SPEC D4.1 — the enrolment ceremony sends NO exclusion list, and the admin's own path keeps its
// one unchanged. Two ceremonies, two answers, and the difference is who is standing at the other
// end.
//
// THIS IS THE TEST THAT KEEPS THEM DIFFERENT. A later refactor that unified the two — the natural
// tidy-up, since they differ by one option — would hand every admin credential id to whoever
// scanned a QR, pre-authentication, with nothing failing.

// seedDeviceFor makes the device a scoped ceremony must be able to name (D2.1). Without it,
// `scopeUsername` refuses and every scoped assertion below fails for the wrong reason.
func seedDeviceFor(t *testing.T, st *store.Store, udid, name string) {
	t.Helper()
	if err := st.UpsertDeviceIdentity(store.DeviceIdentityRow{
		UDID: udid, Name: name, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed device %s: %v", udid, err)
	}
}

func creationFor(t *testing.T, st *store.Store, scope store.Scope, disclose Disclosure) *protocol.CredentialCreation {
	t.Helper()
	options, _, err := BeginPasskeyRegistration(st, NewPasskeyCeremonies(), rpHome, scope, disclose)
	if err != nil {
		t.Fatalf("BeginPasskeyRegistration: %v", err)
	}
	creation, ok := options.(*protocol.CredentialCreation)
	if !ok {
		t.Fatalf("options are %T, want *protocol.CredentialCreation", options)
	}
	return creation
}

func TestTheAdminCeremonyStillExcludesWhatIsRegistered(t *testing.T) {
	st := newResetStore(t)
	seedScoped(t, st, "admin-cred-one", nil)
	seedScoped(t, st, "admin-cred-two", nil)

	creation := creationFor(t, st, store.AdminScope(), ExcludeRegistered())
	if n := len(creation.Response.CredentialExcludeList); n != 2 {
		t.Fatalf("the admin ceremony excluded %d credentials, want 2 — a duplicate on one "+
			"authenticator would no longer be refused by the device", n)
	}
}

// THE LEAK THIS PREVENTS, stated as the assertion rather than as a comment: an exclusion list is a
// list of credential IDs, and the enrolment page is reached with no session at all.
func TestTheEnrolmentCeremonyDisclosesNothing(t *testing.T) {
	st := newResetStore(t)
	seedScoped(t, st, "admin-cred-one", nil)
	seedScoped(t, st, "admin-cred-two", nil)

	// THE CONTROL FIRST: with the same store, the admin ceremony DOES disclose. Without this, a
	// build where `existingCredentials` returned nothing would pass the assertion below while
	// proving nothing at all.
	if n := len(creationFor(t, st, store.AdminScope(), ExcludeRegistered()).Response.CredentialExcludeList); n != 2 {
		t.Fatalf("fixture: the admin ceremony excluded %d, want 2; the assertion below is vacuous", n)
	}

	seedDeviceFor(t, st, "DEVICE-A", "Household iPhone")
	creation := creationFor(t, st, store.DeviceScope("DEVICE-A"), DiscloseNothing())
	if n := len(creation.Response.CredentialExcludeList); n != 0 {
		t.Fatalf("the enrolment ceremony disclosed %d credential id(s) to an unauthenticated "+
			"scanner — spec D4.1 forbids any", n)
	}
}

// THE POLICY IS STATED, NEVER DEFAULTED. Forgetting the argument is a compile error; passing the
// zero value is refused here. Same shape as `store.Scope`, and for the same reason — the permissive
// answer is the dangerous one, so it must not be reachable by omission.
func TestARegistrationWithNoStatedDisclosureIsRefused(t *testing.T) {
	st := newResetStore(t)

	if _, _, err := BeginPasskeyRegistration(st, NewPasskeyCeremonies(), rpHome,
		store.AdminScope(), ExcludeRegistered()); err != nil {
		t.Fatalf("a stated policy: got %v, want nil (the control)", err)
	}
	_, _, err := BeginPasskeyRegistration(st, NewPasskeyCeremonies(), rpHome,
		store.AdminScope(), Disclosure{})
	if !errors.Is(err, ErrDisclosureUnset) {
		t.Fatalf("an unstated policy: got %v, want ErrDisclosureUnset", err)
	}
}

// THE SCOPE AND THE DISCLOSURE ARE INDEPENDENT, and this is worth pinning because the two arguments
// sit side by side and "scoped implies no exclusions" is the plausible shortcut somebody will reach
// for. It is wrong in both directions: D2.2 makes an ADMIN enrolling a scoped credential on their
// own phone a supportable want, and that ceremony is still pre-auth.
func TestScopeDoesNotDecideDisclosure(t *testing.T) {
	st := newResetStore(t)
	seedScoped(t, st, "admin-cred-one", nil)

	seedDeviceFor(t, st, "DEVICE-A", "Household iPhone")
	scopedButDisclosing := creationFor(t, st, store.DeviceScope("DEVICE-A"), ExcludeRegistered())
	if len(scopedButDisclosing.Response.CredentialExcludeList) != 1 {
		t.Fatal("a scoped ceremony silently stopped disclosing — the policy is the argument, not the scope")
	}
	adminButSilent := creationFor(t, st, store.AdminScope(), DiscloseNothing())
	if len(adminButSilent.Response.CredentialExcludeList) != 0 {
		t.Fatal("an admin ceremony silently disclosed — the policy is the argument, not the scope")
	}
}
