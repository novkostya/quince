package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
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

	udid, err := listUDID(d, r)
	if err != nil {
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

	udid, err := listUDID(d, r)
	if err != nil || udid != "DEVICE-A" {
		t.Fatalf("got %q err=%v — an absent query must not mean every device for a scoped holder",
			udid, err)
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
	if udid, err := listUDID(d, r); err != nil || udid != "ANY-DEVICE" {
		t.Fatalf("the adminx27s query was overridden: %q err=%v", udid, err)
	}

	r2 := httptest.NewRequest("GET", "/api/versions", nil)
	r2 = r2.WithContext(withPrincipal(r2.Context(), admin))
	if udid, err := listUDID(d, r2); err != nil || udid != "" {
		t.Fatalf("the admin lost the all-devices list: %q err=%v", udid, err)
	}
}

// A PRINCIPAL THAT CANNOT BE RESOLVED GETS NOTHING, not the unfiltered list. "" means every device
// to both readers, so falling back to it on an error would be the widest possible answer to the
// narrowest possible question.
func TestAnUnresolvablePrincipalIsRefusedRatherThanUnfiltered(t *testing.T) {
	d, _ := listDeps(t)
	r := httptest.NewRequest("GET", "/api/jobs", nil)
	r = r.WithContext(withPrincipal(r.Context(), auth.Principal{CredentialID: "revoked"}))

	udid, err := listUDID(d, r)
	if err == nil {
		t.Fatalf("a revoked credential resolved to %q instead of being refused", udid)
	}
}

// No principal at all is the exempt path, and the guard — not this filter — is what handles it.

// THE TWO CAUSES REACH THE WIRE AS DIFFERENT ANSWERS (quince#1412).
//
// `listUDID` returned a bool, so both mapped to `401 authentication required` — and one of them is
// a database fault, for which that sentence tells an authenticated user to do something a login
// screen cannot fix. Refusing is right in both cases; the REASON was wrong in one.
//
// ASSERTED AT THE MAPPING RATHER THAN THROUGH A ROUTER, because a store that fails on demand is a
// seam this package does not have — and what the fix changed is which status each cause produces,
// which is exactly what this reads.
func TestARevokedCredentialAndAReadFailureAreDifferentAnswers(t *testing.T) {
	d, _ := listDeps(t)

	revoked := httptest.NewRecorder()
	d.writeScopeResolutionError(revoked, auth.ErrCredentialRevoked)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked credential answered %d, want 401 — authenticating again IS the remedy",
			revoked.Code)
	}

	fault := httptest.NewRecorder()
	d.writeScopeResolutionError(fault, errors.New("database is locked"))
	if fault.Code != http.StatusInternalServerError {
		t.Fatalf("a database fault answered %d, want 500 — the caller is authenticated and cannot "+
			"fix a read failure at a login screen", fault.Code)
	}

	// AND THE BODIES DIFFER, not only the codes. A user reads the sentence, not the status.
	if revoked.Body.String() == fault.Body.String() {
		t.Fatalf("both causes produced the same body: %s", revoked.Body.String())
	}
}

// AND THE WHOLE POINT OF THE ERROR RETURN: the cause SURVIVES `listUDID`. A bool could not carry it,
// so no caller could have made the distinction above however carefully it was written.
func TestListUDIDCarriesTheCauseOutward(t *testing.T) {
	d, _ := listDeps(t)
	r := httptest.NewRequest("GET", "/api/jobs", nil)
	r = r.WithContext(withPrincipal(r.Context(), auth.Principal{CredentialID: "revoked"}))

	_, err := listUDID(d, r)
	if !errors.Is(err, auth.ErrCredentialRevoked) {
		t.Fatalf("got %v, want ErrCredentialRevoked — the cause did not survive the call", err)
	}
}
