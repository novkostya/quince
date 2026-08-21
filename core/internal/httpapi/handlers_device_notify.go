package httpapi

import (
	"errors"
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
)

// deviceNotificationsRequest is the PUT /api/devices/{udid}/notifications body (contracts §1).
//
// A POINTER, AND THAT IS THE WHOLE OF THE VALIDATION. `enabled` is a boolean, so a body that omits
// it and a body that says `false` both decode to Go's `false` — and the omitted one would silently
// MUTE the device. There is no value to guess at here, so an absent key is refused rather than
// defaulted: an empty `PUT` is a client bug, and answering 200 to one is how a device goes quiet
// with nothing anywhere saying why.
type deviceNotificationsRequest struct {
	Enabled *bool `json:"enabled"`
}

// handleDeviceNotifications serves PUT /api/devices/{udid}/notifications → 200 {enabled}.
//
// It records whether quince notifies about this device at all (quince#1270) — the SUBJECT axis,
// AND-ed with the global `notifications:` categories. It reaches no device: this is quince's own
// policy, stored in the app DB.
func (d Deps) handleDeviceNotifications() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req deviceNotificationsRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		if req.Enabled == nil {
			writeError(w, d.Log, http.StatusUnprocessableEntity, "unprocessable_entity",
				"enabled is required and must be true or false")
			return
		}
		// THE CALLER'S OWN PREFERENCE, and `callerScope` rather than `listUDID` is the whole of
		// finding 1 from this PR's review. `listUDID` answers what a LIST may report on, which for
		// an admin is `?udid=` — so using it here made the owner of the written row whatever the
		// query string said. An admin could then write a scoped holder's preference row by naming
		// them, which is D7's ruling inverted through the write path; and the admin's own mute
		// landed under a key nothing reads, so the handler echoed 200 to a write that did nothing.
		//
		// Being `scopedOwnDevice` is not a defence: that guard constrains the PATH udid.
		owner, err := callerScope(d, r)
		if err != nil {
			// A REVOKED CREDENTIAL AND A DATABASE FAULT ARE NOT THE SAME REFUSAL (quince#940).
			// The first is an auth problem the user can act on; the second is not, and telling
			// them to authenticate sends them to a screen that cannot fix it.
			if errors.Is(err, auth.ErrCredentialRevoked) {
				writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			writeError(w, d.Log, http.StatusInternalServerError, "internal_error",
				"could not resolve who is making this request")
			return
		}
		stored, status, reason := d.DeviceNotifs.SetNotificationsEnabled(
			r.PathValue("udid"), owner, *req.Enabled)
		if status != http.StatusOK {
			writeError(w, d.Log, status, statusCode(status), reason)
			return
		}
		// THE BODY ECHOES WHAT WAS STORED — `stored`, returned by the write, not `*req.Enabled`.
		//
		// That distinction is the whole point of the return value. What the echo buys is that a
		// client never has to assume its own request succeeded in order to render the control it
		// just moved; echoing the REQUEST would make that guarantee true only for as long as the
		// store cannot alter a bool, which is a property nobody is holding still (quince#1281
		// review). The two values are equal today, and this is what keeps them equal.
		writeJSON(w, d.Log, http.StatusOK, map[string]bool{"enabled": stored})
	}
}
