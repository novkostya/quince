package httpapi

import (
	"strings"
	"testing"
)

// EVERY REGISTERED ROUTE CARRIES A DECISION. `assertRoutesClassified` already panics at
// construction, so this is the readable form of the same claim plus the two properties the panic
// cannot express: that no class is the zero value, and that the admin-only set is the one D3 names.
func TestNoRouteIsClassifiedUnset(t *testing.T) {
	for pattern, class := range routeScope {
		if class == scopeUnset {
			t.Errorf("%s is classified scopeUnset — that is the zero value, not a decision", pattern)
		}
	}
	if len(routeScope) < 50 {
		// The control: a table that shrank to nothing would pass every loop above it.
		t.Fatalf("routeScope holds %d entries; the registered set is ~65", len(routeScope))
	}
}

// THE ADMIN SURFACE, ASSERTED BY NAME. D3's "no" rows are Settings, storages, other devices,
// pairing and issuing passkeys; this is that list as code, so a later edit that quietly opens one
// fails here with the route in the message.
func TestTheAdminOnlySurfaceIsRefusedToScopedPrincipals(t *testing.T) {
	mustRefuse := []string{
		"GET /api/config", "PUT /api/config",
		"POST /api/config/storage", "DELETE /api/config/storage/{name}",
		"GET /api/storages", "POST /api/storages/probe",
		"GET /api/auth/passkeys", "DELETE /api/auth/passkeys/{id}",
		"POST /api/auth/passkeys/register/begin", "POST /api/auth/passkeys/register/finish",
		"PUT /api/auth/password", "DELETE /api/auth/password",
		"POST /api/auth/reauth/begin", "POST /api/auth/reauth/finish",
		"GET /api/devices", "POST /api/devices/rescan",
		"DELETE /api/versions/{id}",
	}
	for _, p := range mustRefuse {
		class, ok := scopeOfPattern(p)
		if !ok {
			t.Errorf("%s has no scope decision", p)
			continue
		}
		if !class.refusesScoped() {
			t.Errorf("%s does NOT refuse a scoped principal — spec D3 says it must", p)
		}
	}
}

// THE CONTROL, and it is the one that matters: the routes a scoped holder's Home is built on must
// NOT be admin-only. Without this, classifying everything `adminOnly` would pass the test above and
// leave the feature useless.
func TestTheirOwnDeviceSurfaceIsNotRefused(t *testing.T) {
	mustNotRefuse := []string{
		"GET /api/devices/{udid}",
		"POST /api/jobs",
		"GET /api/jobs/{id}", "GET /api/jobs/{id}/log", "POST /api/jobs/{id}/cancel",
		"PUT /api/devices/{udid}/notifications",
		"POST /api/devices/{udid}/encryption",
		"GET /api/jobs", "GET /api/versions",
		"GET /api/health", "GET /api/auth/status",
	}
	for _, p := range mustNotRefuse {
		class, ok := scopeOfPattern(p)
		if !ok {
			t.Errorf("%s has no scope decision", p)
			continue
		}
		if class.refusesScoped() {
			t.Errorf("%s refuses a scoped principal — their own device page is built on it", p)
		}
	}
}

// THE DEVICES LIST IS REFUSED, NOT NARROWED (spec D8: unreachable rather than merely unlinked).
// Stated as its own test because "return one row" is the helpful-looking wrong answer, and a later
// reader is more likely to reach for it than for anything else in the table.
func TestTheDevicesListIsUnreachableRatherThanFiltered(t *testing.T) {
	class, ok := scopeOfPattern("GET /api/devices")
	if !ok || !class.refusesScoped() {
		t.Fatal("GET /api/devices must be refused outright; filtering it to one row is the thing D8 forbids")
	}
	if class == scopedFiltered {
		t.Fatal("GET /api/devices is classified scopedFiltered — that is the narrowed answer D8 rejects")
	}
}

// The encryption password is PERMITTED, which is the Operator's reversal of the architect's rule
// (spec D3). Asserted because it is the one row whose absence would read as an oversight.
func TestTheEncryptionRouteIsNotAdminOnly(t *testing.T) {
	class, _ := scopeOfPattern("POST /api/devices/{udid}/encryption")
	if class.refusesScoped() {
		t.Fatal("the encryption password was refused — Operator ruled a scoped holder may change it, " +
			"because a control the platform trivially bypasses is not a control")
	}
}

// The refusal must SAY what it is, not just refuse — troubleshooting is actionable.
func TestTheRefusalNamesTheBoundary(t *testing.T) {
	const want = "administrator action"
	if !strings.Contains(scopeRefusalDetail, want) {
		t.Fatalf("the refusal text %q does not name the boundary", scopeRefusalDetail)
	}
	if !strings.Contains(scopeRefusalDetail, "one device") {
		t.Fatalf("the refusal text %q does not say what access the caller DOES have", scopeRefusalDetail)
	}
}
