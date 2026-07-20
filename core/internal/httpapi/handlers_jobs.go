package httpapi

import "net/http"

// Job command handlers (contracts §1): POST /api/jobs starts a backup (202 Job); POST
// /api/jobs/{id}/cancel cancels a running one (202 Job). The subsystem returns an HTTP status +
// reason; the handler maps 202 to the Job body and anything else to the {error:{code,message}}
// envelope. Reads (GET) are handled in handlers_read.go.

// jobCreateRequest is the POST /api/jobs body (contracts §1). transport auto is deferred to qn.4b
// (the engine returns 422); retry_of is optional (the assisted-model retry chain).
type jobCreateRequest struct {
	UDID      string `json:"udid"`
	Transport string `json:"transport"` // usb | wifi (auto → 422 until qn.4b)
	RetryOf   string `json:"retry_of"`
}

// handleJobCreate serves POST /api/jobs → 202 Job; 409 already-running, 422 bad/auto transport,
// 404 unknown device, 503 no engine.
func (d Deps) handleJobCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req jobCreateRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		job, status, reason := d.JobControl.StartBackup(req.UDID, req.Transport, req.RetryOf)
		if status != http.StatusAccepted {
			writeError(w, d.Log, status, statusCode(status), reason)
			return
		}
		writeJSON(w, d.Log, http.StatusAccepted, job)
	}
}

// handleJobCancel serves POST /api/jobs/{id}/cancel → 202 Job; 409 not-running, 404 unknown job.
func (d Deps) handleJobCancel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, status, reason := d.JobControl.CancelJob(r.PathValue("id"))
		if status != http.StatusAccepted {
			writeError(w, d.Log, status, statusCode(status), reason)
			return
		}
		writeJSON(w, d.Log, http.StatusAccepted, job)
	}
}
