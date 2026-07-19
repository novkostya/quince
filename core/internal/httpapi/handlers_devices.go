package httpapi

import (
	"context"
	"net/http"
	"time"
)

// rescanTimeout bounds a rescan so a wedged muxer supervisor can't hang the request.
const rescanTimeout = 5 * time.Second

// handleRescan serves POST /api/devices/rescan (contracts §1): restart the MANAGED in-container
// muxer so USB devices missed by an unprivileged container's absent hotplug re-enumerate — which
// reuses the muxd client's reconnect→Reset→replay reconcile (no new device-table code). 202 when
// quince manages the muxer; 409 when it is external (devices.manage_muxer: false) or still served
// by another process. Ruled from qn.2's gap capture; landed by qn.2b.
func (d Deps) handleRescan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), rescanTimeout)
		defer cancel()
		accepted, reason := d.Muxer.Rescan(ctx)
		if !accepted {
			writeError(w, d.Log, http.StatusConflict, "muxer_external", reason)
			return
		}
		// 202: the restart is under way; re-enumerated devices arrive via device.* WS events
		// (no op_id — rescan is fire-and-forget, unlike pair/encryption).
		writeJSON(w, d.Log, http.StatusAccepted, map[string]string{"status": "rescanning"})
	}
}
