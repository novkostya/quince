package httpapi

import (
	"io"
	"net/http"
	"strconv"

	"github.com/novkostya/quince/core/internal/wire"
)

// The four unlocked-session routes, frozen in contracts §1 since qn.1 and implemented here
// at qn.8. Every one is behind authGuard like the rest of /api; none is exempted.

// handleVersionUnlock serves POST /api/versions/{id}/unlock {password} → Session.
func (d Deps) handleVersionUnlock() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}

		// THE PASSWORD IS NEVER LOGGED, and there is nothing here to make that true — it is
		// read into a local and passed on. Named because a future edit that logs the request
		// body for debugging would break design §6's rule silently, and this is the one
		// handler where that matters.
		s, code, msg := d.VaultBrowse.Unlock(r.PathValue("id"), body.Password)
		if code != "" {
			writeError(w, d.Log, statusForVaultCode(code), code, msg)
			return
		}
		writeJSON(w, d.Log, http.StatusOK, s)
	}
}

// handleSessionLock serves POST /api/sessions/{id}/lock → 204.
//
// IDEMPOTENT, AND AN UNKNOWN ID IS 204 RATHER THAN 404: the state the caller asked for is
// the state that exists, and a double-click must not look like a fault. contracts §1 says
// 204 and means it.
func (d Deps) handleSessionLock() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if code, msg := d.VaultBrowse.Lock(r.PathValue("id")); code != "" {
			writeError(w, d.Log, statusForVaultCode(code), code, msg)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleSessionBrowse serves GET /api/sessions/{id}/browse?domain&prefix&cursor&limit.
func (d Deps) handleSessionBrowse() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		query := browseQuery(q.Get("domain"), q.Get("prefix"), q.Get("cursor"), q.Get("limit"))

		page, code, msg := d.VaultBrowse.Browse(r.PathValue("id"), query)
		if code != "" {
			writeError(w, d.Log, statusForVaultCode(code), code, msg)
			return
		}
		// Entries is never null on the wire: a client iterating a page should not have to
		// distinguish "no entries" from "field absent".
		if page.Entries == nil {
			page.Entries = []wire.FileEntry{}
		}
		writeJSON(w, d.Log, http.StatusOK, page)
	}
}

// handleSessionFile serves GET /api/sessions/{id}/file/{file_id} → the decrypted bytes.
func (d Deps) handleSessionFile() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rc, entry, code, msg := d.VaultBrowse.OpenFile(r.PathValue("id"), r.PathValue("file_id"))
		if code != "" {
			writeError(w, d.Log, statusForVaultCode(code), code, msg)
			return
		}
		defer func() { _ = rc.Close() }()

		// CONTENT-LENGTH FROM THE RECORDED SIZE, and that is what makes a short read
		// DETECTABLE rather than silent (spec D8.1). If the backup holds fewer bytes than
		// its index records, the body ends early against a declared length and the client
		// reports a broken transfer — which is true. Sending no length instead would let a
		// truncated file arrive looking complete.
		w.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
		w.Header().Set("Content-Type", "application/octet-stream")
		// Nothing derived from backup content is cached anywhere (design §7's lazy model),
		// and an intermediary holding a decrypted file would be exactly that.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)

		if _, err := io.Copy(w, rc); err != nil {
			// The header is already sent, so there is no status left to change. The
			// truncated body IS the report — the client sees fewer bytes than declared —
			// and the log is where the reason lives.
			d.Log.Warn("vault: file stream ended early",
				"session", r.PathValue("id"), "file_id", r.PathValue("file_id"), "err", err)
		}
	}
}

// statusForVaultCode maps a contracts §4 error code to its HTTP status.
//
// TOTAL OVER THE CODES THE SEAM CAN PRODUCE, with `internal` as the default rather than a
// panic: an unmapped code is a bug in this table, and answering 500 tells the truth about it
// while a panic would take the daemon down over a browse.
func statusForVaultCode(code string) int {
	switch code {
	case "bad_password":
		// 403 rather than 401: the caller IS authenticated (every route here is behind
		// authGuard). What failed is the backup's own password, which is a different
		// credential, and a 401 would invite a client to re-run the login flow.
		return http.StatusForbidden
	case "not_found", "not_a_file":
		return http.StatusNotFound
	case "locked":
		// The session is gone or was never unlocked. 409 rather than 404: the SESSION id may
		// be perfectly real and simply expired, and "conflict with the current state" is
		// what that is.
		return http.StatusConflict
	case "corrupt_manifest", "unsupported_ios":
		return http.StatusUnprocessableEntity
	case "unavailable":
		return http.StatusServiceUnavailable
	case "io":
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// browseQuery parses the browse route's query string.
//
// A LIMIT THAT IS NOT A NUMBER IS ZERO, WHICH MEANS "the default" — not an error. The
// alternative is a 400 for `?limit=abc`, which serves nobody: the caller gets a page either
// way, and the seam already discloses a clamp when one happens (BrowsePage.EffectiveLimit).
// A refusal here would be strictness with no reader.
func browseQuery(domain, prefix, cursor, limit string) wire.BrowseQuery {
	n, _ := strconv.Atoi(limit)
	if n < 0 {
		n = 0
	}
	return wire.BrowseQuery{Domain: domain, Prefix: prefix, Cursor: cursor, Limit: n}
}
