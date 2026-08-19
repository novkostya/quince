package httpapi

import "net/http"

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
		status, reason := d.DeviceNotifs.SetNotificationsEnabled(r.PathValue("udid"), *req.Enabled)
		if status != http.StatusOK {
			writeError(w, d.Log, status, statusCode(status), reason)
			return
		}
		// THE BODY ECHOES WHAT WAS STORED, not what was asked for. They are the same value today and
		// the echo costs nothing; what it buys is that a client never has to assume its own request
		// succeeded in order to render the control it just moved.
		writeJSON(w, d.Log, http.StatusOK, map[string]bool{"enabled": *req.Enabled})
	}
}
