package ws

import "github.com/novkostya/quince/core/internal/wire"

// Principal is what this socket is allowed to hear.
//
// A ws-LOCAL TYPE RATHER THAN `auth.Principal`, deliberately. This package must not learn how
// credentials work, and `auth` must not learn what a WebSocket frame is; what crosses the seam is
// one question — *may this principal receive this envelope* — and one fact, the device it is
// confined to. The daemon resolves the credential; this decides the frame.
type Principal struct {
	// ScopeUDID is the device this principal is confined to. EMPTY MEANS THE ADMIN, who hears
	// everything — the same convention as `passkeys.scope_udid` and `sessions_auth.credential_id`,
	// kept identical on purpose so there is one rule to remember rather than three.
	ScopeUDID string
}

// AdminPrincipal is the unconfined principal. Named rather than spelled `Principal{}` at call
// sites, so "this connection hears everything" is a sentence somebody wrote.
func AdminPrincipal() Principal { return Principal{} }

// DevicePrincipal confines a connection to one device.
func DevicePrincipal(udid string) Principal { return Principal{ScopeUDID: udid} }

// MayReceive reports whether this principal is allowed the envelope.
//
// THE ADMIN HEARS EVERYTHING, which is today's behaviour for every connection and stays exactly
// that until something mints a scoped credential.
//
// A SCOPED PRINCIPAL HEARS GLOBAL EVENTS AND ITS OWN DEVICE'S. Global — `hello`, `session.locked`,
// `config.updated` — are facts about quince rather than about anyone's device, and withholding them
// would break the socket for its holder: `hello` is the first frame, and `session.locked` is how a
// client learns its own session ended. **`config.updated` carries no data by design** (see its
// constant), so it discloses nothing; it merely prompts a refetch the API will scope on its own.
//
// FAIL-CLOSED ON AN UNKNOWN EVENT, which is `wire.EventDevice`'s contract: an unclassified type is
// reported as device-scoped with no device, so it matches no scoped principal and reaches only the
// admin. A new event type therefore starts life invisible to scoped holders rather than broadcast to
// them, and the gate over the event constants is what turns that from a silence into a build failure.
func (p Principal) MayReceive(env wire.Envelope) bool {
	if p.ScopeUDID == "" {
		return true
	}
	udid, scoped := wire.EventDevice(env)
	if !scoped {
		return true
	}
	return udid == p.ScopeUDID
}
