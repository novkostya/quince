package httpapi

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/store"
)

// THE OVERRIDE, WHICH IS THE WHOLE POINT (spec D8, slice 8c).
//
// `GET /api/jobs` and `GET /api/versions` are permitted for a scoped principal — refusing them
// would take away their own device's history — so the RESPONSE is narrowed instead. The caller
// supplies `?udid=`, so the scope must OVERRIDE the query rather than fill in when it is absent.
//
// Getting that backwards would look identical in any test that only ever asks for its own device,
// which is why asking for SOMEBODY ELSE'S is the first case here.

func listDeps(t *testing.T) (Deps, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return Deps{Auth: auth.NewService(st, log), Log: log}, st
}

func scopedPrincipal(t *testing.T, st *store.Store, credID, udid string) auth.Principal {
	t.Helper()
	scope := store.DeviceScope(udid)
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: credID,
		PublicKey:    []byte("k"),
		RPID:         "quince.example",
		Name:         credID,
	}, scope); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	return auth.Principal{CredentialID: credID}
}

func TestAScopedPrincipalCannotAskForAnotherDevicesList(t *testing.T) {
	d, st := listDeps(t)
	p := scopedPrincipal(t, st, "cred-a", "DEVICE-A")

	r := httptest.NewRequest("GET", "/api/jobs?udid=SOMEONE-ELSE", nil)
	r = r.WithContext(withPrincipal(r.Context(), p))

	udid, refuse := listUDID(d, r)
	if refuse {
		t.Fatalf("a resolvable scoped principal was refused")
	}
	if udid != "DEVICE-A" {
		t.Fatalf("got %q — the query was HONOURED rather than overridden, so a scoped holder can "+
			"read another device's list by asking for it", udid)
	}
}

// And with no udid asked for, a scoped principal still gets its own rather than "" — which the
// readers treat as EVERY device.
func TestAScopedPrincipalWithNoQueryGetsItsOwnDevice(t *testing.T) {
	d, st := listDeps(t)
	p := scopedPrincipal(t, st, "cred-a", "DEVICE-A")

	r := httptest.NewRequest("GET", "/api/versions", nil)
	r = r.WithContext(withPrincipal(r.Context(), p))

	udid, refuse := listUDID(d, r)
	if refuse || udid != "DEVICE-A" {
		t.Fatalf("got %q refuse=%v — an absent query must not mean every device for a scoped holder",
			udid, refuse)
	}
}

// THE CONTROL. The admin keeps the query, including empty meaning every device — without this,
// narrowing everything to nothing would pass both assertions above and break the Devices page.
func TestTheAdminKeepsTheQueryIncludingAllDevices(t *testing.T) {
	d, st := listDeps(t)
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: "admin-1", PublicKey: []byte("k"), RPID: "quince.example", Name: "admin",
	}, store.AdminScope()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	admin := auth.Principal{CredentialID: "admin-1"}

	r := httptest.NewRequest("GET", "/api/versions?udid=ANY-DEVICE", nil)
	r = r.WithContext(withPrincipal(r.Context(), admin))
	if udid, refuse := listUDID(d, r); refuse || udid != "ANY-DEVICE" {
		t.Fatalf("the admin's query was overridden: %q refuse=%v", udid, refuse)
	}

	r2 := httptest.NewRequest("GET", "/api/versions", nil)
	r2 = r2.WithContext(withPrincipal(r2.Context(), admin))
	if udid, refuse := listUDID(d, r2); refuse || udid != "" {
		t.Fatalf("the admin lost the all-devices list: %q refuse=%v", udid, refuse)
	}
}

// A PRINCIPAL THAT CANNOT BE RESOLVED GETS NOTHING, not the unfiltered list. "" means every device
// to both readers, so falling back to it on an error would be the widest possible answer to the
// narrowest possible question.
func TestAnUnresolvablePrincipalIsRefusedRatherThanUnfiltered(t *testing.T) {
	d, _ := listDeps(t)
	r := httptest.NewRequest("GET", "/api/jobs", nil)
	r = r.WithContext(withPrincipal(r.Context(), auth.Principal{CredentialID: "revoked"}))

	udid, refuse := listUDID(d, r)
	if !refuse {
		t.Fatalf("a revoked credential resolved to %q instead of being refused", udid)
	}
}

// No principal at all is the exempt path, and the guard — not this filter — is what handles it.
