package httpapi

import (
	"net/http"
)

// Device-ops handlers (contracts §1): pair / validate / encryption drive async Ops (202
// {op_id}, narrated over op.updated), and GET /api/ops/{op_id} is the poll/refresh fallback.
// The subsystem returns an HTTP status + reason; the handler maps 202/200 to the success
// body and anything else to the {error:{code,message}} envelope.

// handlePair serves POST /api/devices/{udid}/pair → 202 {op_id}; 409 when the device isn't on
// USB (pairing is USB-only), 404 unknown device.
func (d Deps) handlePair() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opID, status, reason := d.Ops.Pair(r.Context(), r.PathValue("udid"))
		if status != http.StatusAccepted {
			writeError(w, d.Log, status, statusCode(status), reason)
			return
		}
		writeJSON(w, d.Log, http.StatusAccepted, map[string]string{"op_id": opID})
	}
}

// handleResetWorking serves POST /api/devices/{udid}/reset-working → 202 (qn.5b Reset, contracts
// §1): discard the device's dirty working/ so the next backup starts clean from latest/. 409 while
// a backup is running for the device, 404 unknown device, 503 no engine wired. Never touches a
// committed version.
func (d Deps) handleResetWorking() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, reason := d.WorkingReset.ResetWorking(r.PathValue("udid"))
		if status != http.StatusAccepted {
			writeError(w, d.Log, status, statusCode(status), reason)
			return
		}
		writeJSON(w, d.Log, http.StatusAccepted, map[string]string{"note": reason})
	}
}

// handlePairValidate serves POST /api/devices/{udid}/pair/validate → {paired: bool}.
func (d Deps) handlePairValidate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		paired, status, reason := d.Ops.Validate(r.Context(), r.PathValue("udid"))
		if status != http.StatusOK {
			writeError(w, d.Log, status, statusCode(status), reason)
			return
		}
		writeJSON(w, d.Log, http.StatusOK, map[string]bool{"paired": paired})
	}
}

// encryptionRequest is the POST /api/devices/{udid}/encryption body (contracts §1). Passwords
// travel in the TLS body and are handed to the subsystem in memory — never logged (this
// struct is never log-formatted) and never placed in a URL.
type encryptionRequest struct {
	Action      string `json:"action"` // enable | change_password | disable
	Password    string `json:"password"`
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// handleEncryption serves POST /api/devices/{udid}/encryption → 202 {op_id}.
func (d Deps) handleEncryption() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req encryptionRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		opID, status, reason := d.Ops.Encryption(r.Context(), r.PathValue("udid"),
			req.Action, req.Password, req.OldPassword, req.NewPassword)
		if status != http.StatusAccepted {
			writeError(w, d.Log, status, statusCode(status), reason)
			return
		}
		writeJSON(w, d.Log, http.StatusAccepted, map[string]string{"op_id": opID})
	}
}

// handleOp serves GET /api/ops/{op_id} → Op (poll/refresh fallback for op.updated).
func (d Deps) handleOp() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		op, ok := d.Ops.Op(r.PathValue("op_id"))
		if !ok {
			writeError(w, d.Log, http.StatusNotFound, "not_found", "no such op")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, op)
	}
}

// statusCode maps an HTTP status to a short error code for the {error:{code}} envelope.
func statusCode(status int) string {
	switch status {
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnprocessableEntity:
		return "invalid_request"
	case http.StatusBadGateway:
		return "device_unreachable"
	case http.StatusServiceUnavailable:
		return "unavailable"
	default:
		return "bad_request"
	}
}
