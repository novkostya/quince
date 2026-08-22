package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// qn.13 slice 8f / spec D3's SECOND EXCEPTION — a scoped holder reads the storage list in order to
// choose where their own backup goes, and sees `{id, name, reachable}` and nothing else.
//
// Ruled by the Operator 2026-08-22 and confirmed on quince#1472. The row it comes from SPLIT: this
// read is yes, storage MANAGEMENT stays adminOnly.
//
// WHAT THE PROJECTION WITHHOLDS IS THE CLAIM. Capacity, health, backend and path are the admin's
// operational picture; a household member picking a disk does not need them and would learn the
// shape of the install from them. So the assertions below are mostly about ABSENCE, and every one
// of them is paired with the admin case — an absence test with no control passes for a handler that
// returns nothing at all.
//
// ASSERTED ON THE ENCODED JSON, not on the struct. The claim is about what crosses the wire: a
// zeroed `Storage` would carry `"path":""`, which a client cannot tell from *this storage has no
// path*, and that is the defect a separate type exists to avoid.
//
// SYNTHETIC UDIDS. A real one is Operator-private and never enters a fixture.

type fakeStorages struct{ list []wire.Storage }

func (f *fakeStorages) Storages(string) []wire.Storage      { return f.list }
func (f *fakeStorages) Recheck(string) (wire.Storage, bool) { return wire.Storage{}, false }
func (f *fakeStorages) JobsOn(string) []string              { return nil }

func storageDeps(t *testing.T) (Deps, *store.Store) {
	t.Helper()
	d, st := listDeps(t)
	d.Storages = &fakeStorages{list: []wire.Storage{
		{ID: "st-1", Name: "attic disk", Path: "/mnt/attic", Backend: "zfs", Reachable: true, Default: true},
		{ID: "st-2", Name: "desk disk", Path: "/mnt/desk", Backend: "copy", Reachable: false},
	}}
	return d, st
}

func getStorages(t *testing.T, d Deps, principal principalArg) string {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/storages", nil)
	if principal.set {
		r = r.WithContext(withPrincipal(r.Context(), principal.p))
	}
	w := httptest.NewRecorder()
	d.handleStorages()(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	return w.Body.String()
}

type principalArg struct {
	p   auth.Principal
	set bool
}

func TestAScopedHolderSeesOnlyIdNameAndReachable(t *testing.T) {
	d, st := storageDeps(t)
	p := scopedPrincipal(t, st, "cred-scoped", "udid-fixture-0001")

	body := getStorages(t, d, principalArg{p: p, set: true})

	for _, want := range []string{`"id":"st-1"`, `"name":"attic disk"`, `"reachable":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("payload is missing %s — a picker needs all three:\n%s", want, body)
		}
	}
	// THE WITHHELD FIELDS, by key rather than by value: a key that is absent cannot be misread,
	// where an empty string can.
	for _, leaked := range []string{`"path"`, `"backend"`, `"default"`, `"free_bytes"`, `"will_be_full"`} {
		if strings.Contains(body, leaked) {
			t.Fatalf("payload carries %s — that is the admin's operational picture:\n%s", leaked, body)
		}
	}
}

// UNREACHABLE STORAGES STAY IN THE LIST. Omitting them would collapse *exists but unreachable* into
// *does not exist*, and only the first has a remedy.
func TestAnUnreachableStorageIsListedAsUnreachableRatherThanHidden(t *testing.T) {
	d, st := storageDeps(t)
	p := scopedPrincipal(t, st, "cred-scoped", "udid-fixture-0002")

	var got wire.ScopedStoragesResponse
	if err := json.Unmarshal([]byte(getStorages(t, d, principalArg{p: p, set: true})), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Storages) != 2 {
		t.Fatalf("%d storages, want 2 — the unreachable one must be listed, disabled", len(got.Storages))
	}
	if got.Storages[1].Reachable {
		t.Fatalf("the unreachable storage reports reachable=true: %+v", got.Storages[1])
	}
}

// THE CONTROL. Without it every absence above passes for a handler that returns an empty list.
func TestAnAdminStillSeesTheWholeStorageObject(t *testing.T) {
	d, st := storageDeps(t)
	p := seedAdminCred(t, st, "cred-admin")

	body := getStorages(t, d, principalArg{p: p, set: true})

	for _, want := range []string{`"path":"/mnt/attic"`, `"backend":"zfs"`, `"default":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the admin lost %s from the storage object:\n%s", want, body)
		}
	}
}

// AND A PASSWORD LOGIN IS THE ADMIN — a second control, on the other way of being one.
func TestAPasswordLoginSeesTheWholeStorageObject(t *testing.T) {
	d, _ := storageDeps(t)

	body := getStorages(t, d, principalArg{p: auth.Principal{}, set: true})

	if !strings.Contains(body, `"backend":"zfs"`) {
		t.Fatalf("a password login was given the scoped projection:\n%s", body)
	}
}

// A CALLER QUINCE CANNOT IDENTIFY GETS THE PROJECTION, NOT THE ADMIN'S PICTURE.
//
// `ScopeOf` returns `("", ErrCredentialRevoked)` when the credential a session was minted with has
// been removed — quince#1001 ends those sessions, so this is the window between the two. Consulting
// the error and not acting on it lets an unidentifiable caller fall through to the FULL object,
// which is the generous answer on the one branch where generosity is the disclosure.
//
// THE BRANCH WAS ARGUED IN A COMMENT AND HELD BY NOTHING until this test (quince#1477 review). That
// is the third time on this rung — and the second on this exact `err`-before-value shape, after
// quince#1465 — that correct code was shipped with no assertion that it stays correct.
func TestACallerQuinceCannotIdentifyGetsTheProjection(t *testing.T) {
	d, st := storageDeps(t)
	seedAdminCred(t, st, "cred-admin") // keeps the install configured after the removal
	p := scopedPrincipal(t, st, "cred-gone", "udid-fixture-0003")
	if _, err := st.DeletePasskey("cred-gone"); err != nil {
		t.Fatalf("remove credential: %v", err)
	}

	body := getStorages(t, d, principalArg{p: p, set: true})

	if strings.Contains(body, `"path"`) {
		t.Fatalf("a caller whose credential is gone was handed the admin's operational picture:\n%s", body)
	}
	// THE CONTROL, IN THE SAME ASSERTION. Without it this passes for a handler that returned an
	// empty list, or nothing at all — an absence proves the projection only if the projection is
	// demonstrably there.
	if !strings.Contains(body, `"name":"attic disk"`) || !strings.Contains(body, `"reachable":true`) {
		t.Fatalf("the projection itself is missing, so the absence of \"path\" proves nothing:\n%s", body)
	}
}
