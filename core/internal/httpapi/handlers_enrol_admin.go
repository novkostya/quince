package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// THE ADMIN'S ENROLMENT SURFACE — qn.13 slice 9c, spec D4 and D9.
//
// Mint a secret for one device, see what is outstanding, cancel one before it is used. This is the
// first thing in the rung that can ISSUE a scoped credential, which is why every slice that
// constrains one landed before it (D4's ordering, and `0008_passkeys.sql`'s before that).
//
// ADMIN ONLY, ALL THREE. D3 puts *issuing passkeys* on the admin's side of the line, and the reason
// is not symmetry: a scoped holder who could mint an enrolment secret for their own device could
// hand out further credentials to it, which is delegation quince never granted. `routeScope` says
// `adminOnly` and slice 8a's guard is what enforces it — nothing in these handlers re-checks, so
// the classification is not decoration.
//
// THE SECRET IS RETURNED EXACTLY ONCE. `wire.EnrolmentIssued` carries it; `wire.Enrolment`, which
// the listing returns, has no field for it. So a client that loses it asks for another, and no GET
// of this page can leak a live credential into a screenshot or a cache.

// enrolmentDevice resolves the path's device and refuses if quince does not know it.
//
// A DEVICE THAT DOES NOT EXIST GETS A 404, NOT A SECRET. Minting for an unknown udid would produce a
// live credential-issuing token confined to nothing quince can name — and the enrolment ceremony
// would then fail at `scopeUsername`, after a human had already scanned it.
func (d Deps) enrolmentDevice(w http.ResponseWriter, r *http.Request) (wire.Device, bool) {
	udid := r.PathValue("udid")
	if d.Devices == nil {
		writeError(w, d.Log, http.StatusServiceUnavailable, "unavailable", "the device registry is not wired")
		return wire.Device{}, false
	}
	dev, ok := d.Devices.Device(udid)
	if !ok {
		writeError(w, d.Log, http.StatusNotFound, "not_found", "no such device")
		return wire.Device{}, false
	}
	return dev, true
}

func enrolmentWire(e auth.Enrolment) wire.Enrolment {
	return wire.Enrolment{
		ID:        e.ID,
		UDID:      e.ScopeUDID,
		CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
		ExpiresAt: e.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

// POST /api/devices/{udid}/enrolments → 201 {id, udid, created_at, expires_at, secret}
func (d Deps) handleEnrolmentCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev, ok := d.enrolmentDevice(w, r)
		if !ok {
			return
		}
		token, en, err := d.Enrolments.Mint(store.DeviceScope(dev.UDID))
		if err != nil {
			// ErrEnrolmentAdminScope and ErrEnrolmentScopeUnset are both unreachable from here —
			// the scope is built from a udid this handler just resolved — so a failure is a bug
			// rather than a caller error, and it says so instead of blaming the request.
			d.Log.Error("minting an enrolment secret failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not create an enrolment link")
			return
		}
		writeJSON(w, d.Log, http.StatusCreated, wire.EnrolmentIssued{
			Enrolment: enrolmentWire(en),
			Secret:    token,
		})
	}
}

// GET /api/devices/{udid}/enrolments → 200 {enrolments: [...]}
//
// LIVE ONES ONLY. A spent or expired secret grants nothing, and listing it would turn a page whose
// job is *what authority is outstanding* into a history nobody asked for. The ruling's phrasing is
// the test: *authority nobody can see is authority nobody revokes* — and authority that no longer
// exists is not authority.
func (d Deps) handleEnrolmentList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev, ok := d.enrolmentDevice(w, r)
		if !ok {
			return
		}
		out := []wire.Enrolment{}
		for _, e := range d.Enrolments.List(dev.UDID) {
			out = append(out, enrolmentWire(e))
		}
		// A SLICE, NEVER NULL — the UI renders a list and `null` is not one (the same reason
		// `handleVersions` and `handleJobs` both normalise).
		writeJSON(w, d.Log, http.StatusOK, map[string][]wire.Enrolment{"enrolments": out})
	}
}

// DELETE /api/devices/{udid}/enrolments/{id} → 204
//
// THE THREE REFUSALS ARE KEPT APART, for `Enrolments`' own reason one layer down. Cancelling a
// secret that was already USED is not a no-op with a tidy status: the credential it minted exists,
// and telling the admin "cancelled" would be the opposite of the truth at the moment they most need
// it. The remedy for that case is the passkey list, and the message says so.
func (d Deps) handleEnrolmentRevoke() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev, ok := d.enrolmentDevice(w, r)
		if !ok {
			return
		}
		err := d.Enrolments.Revoke(dev.UDID, r.PathValue("id"))
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, auth.ErrEnrolmentWrongDevice):
			// THE PATH CLAIMED A DEVICE AND THE ID BELONGS TO ANOTHER. 404 rather than a
			// success, because the link this request named does not exist on this device —
			// and a distinct code, so a UI can say the useful thing rather than "not found".
			writeError(w, d.Log, http.StatusNotFound, "enrolment_wrong_device",
				"no enrolment link with that id for this device")
		case errors.Is(err, auth.ErrEnrolmentNotFound):
			writeError(w, d.Log, http.StatusNotFound, "not_found",
				"no such enrolment link — it may have expired already")
		case errors.Is(err, auth.ErrEnrolmentSpent):
			writeError(w, d.Log, http.StatusConflict, "enrolment_spent",
				"this link has already been used — remove the passkey it created instead")
		case errors.Is(err, auth.ErrEnrolmentRevoked):
			writeError(w, d.Log, http.StatusConflict, "enrolment_revoked", "this link was already cancelled")
		default:
			d.Log.Error("revoking an enrolment secret failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not cancel this enrolment link")
		}
	}
}
