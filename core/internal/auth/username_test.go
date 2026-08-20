package auth

import (
	"errors"
	"testing"

	"github.com/novkostya/quince/core/internal/store"
)

// G7 — NO SCOPED CREDENTIAL IS EVER NAMED `quince-admin`.
//
// Operator, 2026-08-20, on being shown the measurement: "household member must not be quince-admin,
// that's wild." The platform labels a credential with its `user.name`, so a scoped holder's phone
// would tell them they hold admin — the opposite of what their credential grants.
//
// This gate also protects the SECOND property D2.1 buys: iOS collapses credentials on
// `(rpId, username)`, so distinct names are what stop two credentials of DIFFERENT AUTHORITY
// merging into one unselectable row.

func TestAnAdminCredentialKeepsTheAnchorUsername(t *testing.T) {
	// quince#819's constant must survive for admin credentials — the keychain keys on
	// (origin, username), and a different name would file the admin passkey as a second identity
	// beside the password rather than beside it.
	u := passkeyUser{name: ""}
	if got := u.WebAuthnName(); got != adminUsername {
		t.Fatalf("admin username: got %q want %q", got, adminUsername)
	}
	if got := u.WebAuthnDisplayName(); got != adminUsername {
		t.Fatalf("admin display name: got %q want %q", got, adminUsername)
	}
}

func TestAScopedCredentialCarriesItsDeviceName(t *testing.T) {
	u := passkeyUser{name: "Kitchen iPad"}
	if got := u.WebAuthnName(); got != "Kitchen iPad" {
		t.Fatalf("scoped username: got %q want the device name", got)
	}
	if u.WebAuthnName() == adminUsername {
		t.Fatal("a scoped credential is named quince-admin — the thing D2.1 forbids")
	}
	// Display name follows the same rule; two spellings could disagree and only one is shown.
	if u.WebAuthnDisplayName() != u.WebAuthnName() {
		t.Fatal("display name and username disagree")
	}
}

func TestScopeUsernameResolvesTheDeviceName(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.store.UpsertDeviceIdentity(store.DeviceIdentityRow{
		UDID: "DEVICE-A", Name: "Kitchen iPad",
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}

	name, err := scopeUsername(svc.store, store.DeviceScope("DEVICE-A"))
	if err != nil {
		t.Fatalf("scopeUsername: %v", err)
	}
	if name != "Kitchen iPad" {
		t.Fatalf("got %q want the device's own name", name)
	}

	// The admin case resolves to empty, which `WebAuthnName` turns into the constant. Asserted
	// because "" and "quince-admin" are two representations of one thing and only one is stored.
	adminName, err := scopeUsername(svc.store, store.AdminScope())
	if err != nil {
		t.Fatalf("admin scopeUsername: %v", err)
	}
	if adminName != "" {
		t.Fatalf("admin scope produced a name: %q", adminName)
	}
}

// AN UNKNOWN DEVICE IS AN ERROR, NOT A FALLBACK, and this is the assertion that keeps it one.
//
// A generic fallback would make every unknown-device credential share a username — and since the
// collapse key is (rpId, username), that reintroduces the exact unselectable row D2.1 removes, at
// the moment quince is least sure what it is looking at. The udid is not an option either: it is
// Operator-private and this value is displayed on a screen.
func TestScopeUsernameRefusesAnUnknownDevice(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, err := scopeUsername(svc.store, store.DeviceScope("NOT-A-DEVICE"))
	if !errors.Is(err, ErrUnknownScopeDevice) {
		t.Fatalf("got %v — want ErrUnknownScopeDevice rather than a fallback name", err)
	}
}

// A device with an EMPTY name is the same case as an unknown one: there is no name to show, and
// falling back would collapse. Asserted separately because the row exists, so a `found` check alone
// would pass it through.
func TestScopeUsernameRefusesADeviceWithNoName(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.store.UpsertDeviceIdentity(store.DeviceIdentityRow{
		UDID: "DEVICE-B", Name: "",
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	_, err := scopeUsername(svc.store, store.DeviceScope("DEVICE-B"))
	if !errors.Is(err, ErrUnknownScopeDevice) {
		t.Fatalf("got %v — a nameless device must not produce a blank or default username", err)
	}
}
