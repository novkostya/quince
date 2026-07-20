package httpapi

import "net/http"

// handleVersionDelete serves DELETE /api/versions/{id} → 202 (contracts §1, a confirmed
// destructive action). The storage subsystem removes the artifact + registry row, audits the
// event, and emits version.deleted; the handler maps its status.
func (d Deps) handleVersionDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := d.VersionAdmin.Delete(r.PathValue("id"))
		switch status {
		case http.StatusAccepted:
			writeJSON(w, d.Log, http.StatusAccepted, map[string]string{})
		case http.StatusNotFound:
			writeError(w, d.Log, http.StatusNotFound, "not_found", "no such version")
		case http.StatusServiceUnavailable:
			writeError(w, d.Log, http.StatusServiceUnavailable, "unavailable", "storage is unavailable")
		default:
			msg := "could not delete version"
			if err != nil {
				msg = err.Error()
			}
			writeError(w, d.Log, http.StatusInternalServerError, "internal", msg)
		}
	}
}
