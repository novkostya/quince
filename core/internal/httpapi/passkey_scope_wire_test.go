package httpapi

import (
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// qn.13 slice 11 / D9 — the admin's passkey list MARKS each row admin or device-scoped.
//
// WHY THIS IS A TEST AND NOT A GLANCE AT THE MAPPER. `enrolment_ceremony.go` records what the
// absent field already cost: the stored label had to be derived from the scope because
// "`wire.Passkey` carries no scope, so two enrolled devices produced two rows the admin could tell
// apart only by guessing." The label made rows DISTINGUISHABLE; only this field makes them
// CLASSIFIED, and the failure it prevents is silent — a scoped row rendering as an admin one looks
// exactly like an admin row.
//
// SYNTHETIC UDIDS THROUGHOUT. A real one is Operator-private and never enters a fixture.

func TestAnAdminCredentialCarriesNoScope(t *testing.T) {
	got := passkeyToWire(store.Passkey{
		CredentialID: "cred-admin",
		Name:         "laptop",
		RPID:         "quince.example.com",
		CreatedAt:    time.Now().UTC(),
		ScopeUDID:    nil,
	})
	if got.Scope != nil {
		t.Fatalf("Scope = %+v, want nil — nil means ADMIN, and a non-nil scope on an admin "+
			"credential would mark the one row that administers quince as confined", got.Scope)
	}
}

func TestAScopedCredentialNamesItsDevice(t *testing.T) {
	udid := "udid-fixture-0001"
	got := passkeyToWire(store.Passkey{
		CredentialID: "cred-scoped",
		Name:         "hallway tablet",
		RPID:         "quince.example.com",
		CreatedAt:    time.Now().UTC(),
		ScopeUDID:    &udid,
	})
	if got.Scope == nil {
		t.Fatal("Scope = nil, want the device — an unmarked row reads as ADMIN, which is the " +
			"privilege this rung exists to withhold")
	}
	if got.Scope.UDID != udid {
		t.Fatalf("Scope.UDID = %q, want %q", got.Scope.UDID, udid)
	}
}

// THE WIRE VALUE MUST NOT ALIAS THE STORE'S POINTER. Two rows sharing one pointer is a shape where
// writing through either changes both, and nothing in the types stops a future caller trying.
func TestTheWireScopeIsACopyRatherThanTheStoresPointer(t *testing.T) {
	udid := "udid-fixture-0002"
	p := store.Passkey{CredentialID: "c", CreatedAt: time.Now().UTC(), ScopeUDID: &udid}

	got := passkeyToWire(p)
	udid = "udid-fixture-MUTATED"

	if got.Scope == nil || got.Scope.UDID != "udid-fixture-0002" {
		t.Fatalf("Scope = %+v — the wire row followed the store's pointer, so it reports whatever "+
			"the caller's variable holds later rather than what the credential was scoped to", got.Scope)
	}
}
