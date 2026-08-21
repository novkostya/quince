package auth

import (
	"bytes"
	"testing"

	"github.com/novkostya/quince/core/internal/store"
)

// quince#1393 — ONE WebAuthn user.id PER PRINCIPAL.
//
// Sharing one handle told the platform that a household member and the admin were the same user.
// `user.name` is a display string; `user.id` is the identity, and D2.1's fix made the label carry
// identity work it cannot do.

func TestTheAdminHandleIsUnchanged(t *testing.T) {
	svc, _ := newTestAuth(t)
	want, err := userHandle(svc.store)
	if err != nil {
		t.Fatalf("userHandle: %v", err)
	}
	got, err := handleForScope(svc.store, store.AdminScope())
	if err != nil {
		t.Fatalf("handleForScope: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the admin handle changed — every credential already issued would be orphaned")
	}
}

func TestAScopedPrincipalGetsItsOwnHandle(t *testing.T) {
	svc, _ := newTestAuth(t)
	admin, err := userHandle(svc.store)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := handleForScope(svc.store, store.DeviceScope("DEVICE-A"))
	if err != nil {
		t.Fatalf("handleForScope: %v", err)
	}
	if bytes.Equal(scoped, admin) {
		t.Fatal("a scoped principal shares the admin's user.id — to the platform they are one user, " +
			"which is what lets an enrolment operate on the admin's own credential")
	}
	if len(scoped) != len(admin) {
		t.Fatalf("scoped handle is %d bytes, admin is %d — no reason for a scoped identity to be weaker",
			len(scoped), len(admin))
	}
}

func TestTwoDevicesGetDifferentHandles(t *testing.T) {
	svc, _ := newTestAuth(t)
	a, err := handleForScope(svc.store, store.DeviceScope("DEVICE-A"))
	if err != nil {
		t.Fatal(err)
	}
	seedHandled(t, svc.store, "cred-a", "DEVICE-A", a)
	b, err := handleForScope(svc.store, store.DeviceScope("DEVICE-B"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two devices share a user.id — their credentials would collapse into one")
	}
}

// A DEVICE'S SECOND CREDENTIAL REUSES ITS FIRST HANDLE. Two passkeys for one device are one user,
// which is what the platform should see — and "stable" has to mean stable for a value the
// authenticator stores and presents back.
func TestASecondCredentialForOneDeviceReusesTheHandle(t *testing.T) {
	svc, _ := newTestAuth(t)
	first, err := handleForScope(svc.store, store.DeviceScope("DEVICE-A"))
	if err != nil {
		t.Fatal(err)
	}
	seedHandled(t, svc.store, "cred-1", "DEVICE-A", first)

	second, err := handleForScope(svc.store, store.DeviceScope("DEVICE-A"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("a device's second credential got a NEW handle — the platform would see two users " +
			"for one device, and the first credential's identity would no longer be reachable")
	}
}

// THE UPGRADE PATH. A credential registered before quince#1393 has no stored handle, and must still
// resolve to the admin's shared one — otherwise every existing passkey stops signing in.
func TestAPreQn13CredentialResolvesToTheSharedHandle(t *testing.T) {
	svc, _ := newTestAuth(t)
	admin, err := userHandle(svc.store)
	if err != nil {
		t.Fatal(err)
	}
	got, err := handleOf(svc.store, store.Passkey{CredentialID: "old", UserHandle: nil})
	if err != nil {
		t.Fatalf("handleOf: %v", err)
	}
	if !bytes.Equal(got, admin) {
		t.Fatal("a pre-quince#1393 credential did not resolve to the shared handle — every passkey " +
			"issued before this change would fail to sign in")
	}
}

// The stored column keeps meaning what 0016 says: nil for the admin, a value for a scoped principal.
func TestStoredHandleIsNilForTheAdmin(t *testing.T) {
	if storedHandle(store.AdminScope(), []byte("anything")) != nil {
		t.Fatal("an admin credential stored a handle — the column must mean the shared one")
	}
	if storedHandle(store.DeviceScope("DEVICE-A"), []byte("abc")) == nil {
		t.Fatal("a scoped credential stored no handle — login would resolve it to the admin's")
	}
}

func seedHandled(t *testing.T, st *store.Store, id, udid string, handle []byte) {
	t.Helper()
	scope := store.DeviceScope(udid)
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: id,
		PublicKey:    []byte("k"),
		RPID:         rpHome,
		Name:         id,
		UserHandle:   storedHandle(scope, handle),
	}, scope); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}
