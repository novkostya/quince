package httpapi

import (
	"net/http"

	"github.com/novkostya/quince/core/internal/wire"
)

// GET /api/onboarding/probe/nonce → {nonce}. SAME-ORIGIN, and it never carries a CORS header.
//
// This is where a legitimate page gets the token it will present to the echo below. The ruling's
// safety argument rests on exactly that asymmetry: *"a legitimate page obtained its nonce same-origin
// from this quince. A drive-by page has none, gets no header, and reads nothing."*
//
// SO THIS ENDPOINT MUST NEVER BE CORS-READABLE. A foreign origin may cause a mint — nothing stops it
// issuing the request — but it cannot READ the response, so it cannot obtain a usable nonce. The
// bound on that nuisance is `probeNonceMax`, which evicts rather than refuses.
func (d Deps) handleProbeNonce() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nonce, err := d.ProbeNonces.mint()
		if err != nil {
			d.Log.Error("could not mint a probe nonce", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not mint a nonce")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.ProbeNonce{Nonce: nonce})
	}
}

// GET /api/onboarding/probe?nonce=… → {nonce, detected}. THE CROSS-ORIGIN HALF.
//
// Operator ruling 2026-08-14 (quince#908's CORS ruling, amended on quince#939): one nonce-gated
// endpoint serves both probes, and `/api/health` gets no CORS — not now, and not by citing that
// ruling later. Health carries `version`, `mode`, the muxer list and `insecure_transport_allowed`,
// and that last one is a machine-readable *this box serves cookies without `Secure`*, which is a
// recon primitive quince#933's banner exists to warn a human about.
//
// WHAT IT ANSWERS, and it is two questions in one round trip:
//
//	nonce      did I reach MYSELF? Without it a success means "a quince answered", and if the name
//	           points at a different quince on the LAN the check passes while the redirect sends the
//	           user somewhere else entirely.
//	detected   what did quince SEE on this connection — its own TLS, a forwarded scheme, or neither?
//	           `detected: none` behind a working https proxy is the nginx caveat, and it is the whole
//	           reason quince#939 needed more than "did you answer".
//
// `detected` COMES FROM `detectHTTPS`, the same function `GET /api/onboarding/https` uses — required
// by the ruling, and it is this codebase's most-repeated defect otherwise: a predicate computed twice
// diverges, and `RequireStorage`, `CheckStorages` and `AllowsInsecureTransport` each carry a
// paragraph about having been bitten by it.
//
// THE FOUR CORS CONSTRAINTS ARE PART OF THE RULING, not implementation detail:
//
//  1. THE NONCE TRAVELS IN THE QUERY STRING and the request stays a SIMPLE GET. A custom header would
//     trigger a preflight, and the `OPTIONS` preflight does not carry the header's VALUE — so the
//     gate would have nothing to gate on, leaving only "allow every preflight blindly" or "refuse
//     them all". Keeping it simple removes the problem rather than solving it.
//  2. NEVER `Access-Control-Allow-Credentials`. The endpoint is unauthenticated and must stay so;
//     with credentials the widening becomes a cross-origin door onto a cookie-bearing request, which
//     is a different decision from the one that was taken.
//  3. ECHO THE CALLER'S `Origin`, NEVER `*`, AND SEND `Vary: Origin`. The header is origin-dependent
//     by construction, so an intermediary caching without `Vary` can hand one origin's grant to
//     another — quietly restoring the wildcard the ruling refused.
//  4. THE BODY IS `{nonce, detected}` AND NOTHING ELSE. Adding a field is a contracts change AND
//     needs the ruling revisited, because *it leaks nothing* is the entire safety argument. A version
//     string added for convenience would undo it.
//
// AN INVALID OR ABSENT NONCE STILL ANSWERS 200 — with no CORS header, so a cross-origin caller cannot
// read it. It is deliberately not a 4xx: the response a drive-by page gets is indistinguishable from
// a network error, which is correct, because a failure is a failure either way and there is nothing
// to learn from telling it apart.
func (d Deps) handleProbe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nonce := r.URL.Query().Get("nonce")
		if origin := r.Header.Get("Origin"); origin != "" && d.ProbeNonces.valid(nonce) {
			// `Vary` UNCONDITIONALLY WHEN AN ORIGIN WAS PRESENTED, including on the paths that do
			// not grant — a cache that stored the ungranted answer under a key ignoring `Origin`
			// would serve it to the origin that would have been granted, and vice versa.
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		if r.Header.Get("Origin") != "" {
			w.Header().Add("Vary", "Origin")
		}
		writeJSON(w, d.Log, http.StatusOK, wire.ProbeResult{
			Nonce:    nonce,
			Detected: detectHTTPS(r, d.Proxies).Detected,
		})
	}
}
