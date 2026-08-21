package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

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
		// `application/octet-stream` IS A SECURITY CONTROL, not a shrug about the file type.
		// A backup holds arbitrary user files, HTML and SVG among them. Served with a real
		// content type and `inline`, one of those executes script against quince's OWN origin
		// with the session cookie in scope — stored XSS reachable by anyone who can put a file
		// on the device. A preview feature therefore needs a separate origin or a sandbox and
		// is a rung, not a flag on this route (quince#1397 ruling).
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", contentDisposition(entry.RelativePath))
		// Nothing derived from backup content is cached anywhere (design §7's lazy model),
		// and an intermediary holding a decrypted file would be exactly that.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)

		written, err := io.Copy(w, rc)
		switch {
		case err == nil:
		case errors.Is(err, http.ErrContentLength):
			// THE FILE IS LONGER THAN ITS RECORD SAYS — the OPPOSITE of a short read, and it
			// was logged as one until quince#1379. net/http refuses the write past the
			// declared length, so io.Copy fails here on a stream that delivered too MUCH.
			//
			// WHICH BACKEND SERVED THE VERSION DECIDES WHETHER THIS ARM IS REACHED AT ALL,
			// and reading it as one behaviour is how it gets misread in both directions
			// (quince#1433). httpapi is consumer-defined and cannot tell them apart, so it
			// must be correct for both:
			//
			//   ENCRYPTED — this arm. `Open` pipes DecryptFile straight through with no
			//   bound, so an overlong file overruns the declared length and net/http tears
			//   the response: the client reads ZERO bytes and an `unexpected EOF`. Measured
			//   either side of the response buffer, because a large file could plausibly
			//   flush a complete-looking prefix first; it does not (quince#1381).
			//
			//   UNENCRYPTED — NOT this arm, and nothing here runs. `boundedFile` stops at
			//   the recorded size and the registry wrapper turns ErrOverlongFile into
			//   io.EOF (quince#1400), so io.Copy returns nil and the transfer is the
			//   success it is. Measured on hardware 2026-08-21: HTTP 200, Content-Length
			//   1003520, 1003520 bytes delivered, no error.
			//
			// SO THE ARM IS LIVE, WHICH IS THE QUESTION quince#1433 ASKED FIRST. It is not
			// vestigial and must not be deleted as such: `TestVaultFileLongerThanItsRecord
			// IsNotReportedAsEndingEarly` drives an unbounded stub — the encrypted shape —
			// and is green, and its assertion that the log names the long case is what
			// proves this branch is the one taken.
			//
			// THE PARAGRAPH THIS REPLACES STATED THE ENCRYPTED BEHAVIOUR UNQUALIFIED, which
			// is what made it read as simply stale to a session measuring the unencrypted
			// path on the stand. Both halves were true; neither was the whole, and the arm
			// that looks dead from one backend is the only one the other can take.
			//
			// That is why this line gets its own words: `unexpected EOF` is the whole of what
			// reaches the client, so the log is the only place the reason exists. The user's
			// remedy is the same either way and it is in the message — a fresh backup of this
			// device re-records the file.
			d.Log.Warn("vault: file is longer than its manifest record, so the download FAILED — "+
				"net/http refuses the write past the declared Content-Length and the client sees "+
				"`unexpected EOF`. This backup holds more bytes on disk for this file than its own "+
				"index records; a fresh backup of this device re-records it",
				"session", r.PathValue("id"), "file_id", r.PathValue("file_id"),
				"recorded_size", entry.Size, "err", err)
		default:
			// The header is already sent, so there is no status left to change. The
			// truncated body IS the report — the client sees fewer bytes than declared —
			// and the log is where the reason lives.
			d.Log.Warn("vault: file stream ended early — the backup holds fewer bytes than its "+
				"index records for this file",
				"session", r.PathValue("id"), "file_id", r.PathValue("file_id"),
				"recorded_size", entry.Size, "delivered", written, "err", err)
		}
	}
}

// statusForVaultCode maps a contracts §4 error code to its HTTP status, per §1's table.
//
// TOTAL OVER wire.VaultErrorCodes, AND ASSERTED TO BE — the enumeration is not decoration:
// this function claimed totality in a comment for one rung while answering 500 for two codes
// the surface really emits (quince#1375). A claim no test can fail is a claim that goes
// false quietly, which is why the split below exists.
//
// vaultCodeStatus reports whether it recognised the code; this wrapper is what the handlers
// call, and it keeps `internal` as the answer for an unrecognised one rather than a panic —
// an unmapped code is a bug in that table, and 500 tells the truth about it where a panic
// would take the daemon down over a browse.
func statusForVaultCode(code string) int {
	if status, ok := vaultCodeStatus(code); ok {
		return status
	}
	return http.StatusInternalServerError
}

// vaultCodeStatus is the table itself. The second return is what the totality test reads:
// without it a fallen-through code is indistinguishable from one deliberately mapped to 500,
// which is exactly how `io` hid its two neighbours.
func vaultCodeStatus(code string) (int, bool) {
	switch code {
	case wire.VaultCodeBadPassword:
		// 403 rather than 401: the caller IS authenticated (every route here is behind
		// authGuard). What failed is the backup's own password, which is a different
		// credential, and a 401 would invite a client to re-run the login flow.
		return http.StatusForbidden, true
	case wire.VaultCodeNotFound, wire.VaultCodeNotAFile:
		return http.StatusNotFound, true
	case wire.VaultCodeLocked:
		// The session is gone or was never unlocked. 409 rather than 404: the SESSION id may
		// be perfectly real and simply expired, and "conflict with the current state" is
		// what that is.
		return http.StatusConflict, true
	case wire.VaultCodeBusy:
		// 409 for the same reason as `locked` and NOT 500: the session is real and a stream
		// is open against it. The caller retries when the download finishes — a remedy that
		// exists, which a 500 would deny them.
		return http.StatusConflict, true
	case wire.VaultCodeCorruptManifest, wire.VaultCodeUnsupportedIOS, wire.VaultCodeUnsupportedVersion:
		return http.StatusUnprocessableEntity, true
	case wire.VaultCodeUnavailable:
		return http.StatusServiceUnavailable, true
	case wire.VaultCodeIO:
		return http.StatusInternalServerError, true
	}
	return 0, false
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

// contentDisposition names a downloaded file, per RFC 6266 with an RFC 5987 `filename*`.
//
// WITHOUT IT A DOWNLOAD SAVES AS A 40-CHARACTER SHA-1 WITH NO EXTENSION — the browser names
// the file after the URL's last segment, which is the file id. Measured on real hardware:
// 213,780 correct bytes, unopenable without a manual rename the user has nothing to base
// (quince#1397).
//
// `attachment`, ALWAYS. Never `inline`: see the Content-Type comment above — this is the
// same control, and the two must not drift apart.
//
// THE BASENAME, ALWAYS, AND NEVER CONDITIONALLY. A flattened full path is irreversible and
// still not a path; and disambiguating only when names collide would make a file's name
// depend on what else you had downloaded, which is not a property anyone can reason about.
// Browsers already append `(1)`; the domain and path belong in the browse row, which is
// where a user learns which `Info.plist` they took.
//
// SANITIZED AT CONSTRUCTION, NOT TRUSTED TO THE WRITER. A relative path is DEVICE CONTENT: it
// can carry quotes, newlines and control characters, and it is about to become a response
// header. Header splitting must be impossible where the value is built, not caught later by
// something that may or may not be looking.
func contentDisposition(relativePath string) string {
	name := path.Base(relativePath)
	if name == "." || name == "/" || name == "" {
		// A record with no usable basename still has to download as something. `file` is a
		// deliberate placeholder rather than the file id: the id is not a name, and putting
		// it here would reproduce the defect this function exists to fix.
		name = "file"
	}

	// The ASCII fallback for clients that do not implement RFC 5987. Anything outside
	// printable ASCII, plus the two characters that could terminate or split the header, is
	// replaced rather than escaped — a fallback is a best effort by definition, and an
	// unambiguous `_` beats a clever encoding that some client mis-parses.
	var ascii strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r > 0x7e || r == '"' || r == '\\' {
			ascii.WriteByte('_')
			continue
		}
		ascii.WriteRune(r)
	}

	// filename* carries the truth. url.PathEscape leaves sub-delims that RFC 5987's
	// `attr-char` excludes, so the encoding is done here against that grammar rather than
	// borrowed from a URL escaper that answers a different question.
	var enc strings.Builder
	for _, b := range []byte(name) {
		if isAttrChar(b) {
			enc.WriteByte(b)
			continue
		}
		fmt.Fprintf(&enc, "%%%02X", b)
	}

	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii.String(), enc.String())
}

// isAttrChar reports whether a byte may appear unescaped in an RFC 5987 ext-value. The set is
// ALPHA / DIGIT / "!#$&+-.^_`|~" — deliberately narrower than URL-unreserved, because this
// value sits inside a header parameter rather than a path.
func isAttrChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", b) >= 0
}
