package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/novkostya/quince/core/internal/store"
)

// The rpId half of qn.6k — spec D2, and the reason the credentials table stores an rp_id at all.
//
// A passkey is bound to a DOMAIN. The access path is a user choice (qn.6f's four tiers), so moving
// between them — reverse proxy to Tailscale, or a domain change — breaks every credential while the
// phone still lists them, and NOTHING IN THE PROTOCOL SAYS SO. WebAuthn's own failure for this is
// indistinguishable from "no credential here": the browser simply finds nothing to offer.
//
// Deriving the rpId at use time is necessary and not sufficient. Storing it per credential is what
// turns "your passkey stopped working" into a sentence — which is the state-honesty rule applied to
// a credential rather than to a job.

// ErrRPIDMismatch is returned when a credential exists but was registered against a different
// rpId than the one this request arrives on.
//
// IT CARRIES BOTH DOMAINS BECAUSE THE MESSAGE IS THE WHOLE VALUE. A bare "authentication failed"
// here is worse than useless: the user's phone still lists the passkey, so the honest reading of a
// generic error is "quince is broken", and the actual remedy — go back to the address you
// registered on, or register again here — is unguessable.
type ErrRPIDMismatch struct {
	Registered string // the rpId the credential was created against
	Presented  string // the rpId this request arrived on
}

func (e ErrRPIDMismatch) Error() string {
	return fmt.Sprintf("this passkey was registered for %q and you are on %q — "+
		"passkeys are bound to the address you set them up on. Sign in with your password, "+
		"then add a passkey for this address.", e.Registered, e.Presented)
}

// RPIDFromRequest derives the Relying Party ID for this request: the host, without the port.
//
// NOT CONFIGURED, and that is D2's second argument doing double duty. A pinned `rp_id` that
// disagrees with the origin fails every assertion with no way for the user to see why, and D12 says
// config carries what the user SET rather than what the software could derive. Deriving is also the
// only option that stays correct across all four qn.6f tiers without the user editing YAML.
//
// The PORT IS EXCLUDED because an rpId is a domain and never an authority: a credential registered
// on :8443 must still work on :443 behind the same name. That is WebAuthn's rule, not a choice —
// but it is worth stating, because the origin check (which the library performs, and which DOES
// include scheme and port) is a different comparison that happens beside this one.
//
// A bare IP is returned unchanged and is CAUGHT ELSEWHERE, not here: an IP cannot be an rpId, and
// the registration surface refuses that tier outright (story 4) rather than minting a credential
// that can never be used. This function's job is to say what the address is, not whether it is
// usable.
func RPIDFromRequest(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// ResolveCredential looks a credential up for an assertion arriving on rpID, and refuses a
// cross-domain one with a message that names both.
//
// The ORDER of the two failures matters and is deliberate: "no such credential" comes first, so a
// credential that does not exist here cannot be distinguished from one registered elsewhere by
// probing — the mismatch message is only ever shown for a credential this quince actually holds.
func ResolveCredential(st *store.Store, credentialID, rpID string) (store.Passkey, error) {
	pk, ok, err := st.GetPasskey(credentialID)
	if err != nil {
		return store.Passkey{}, err
	}
	if !ok {
		return store.Passkey{}, ErrNoCredential
	}
	if pk.RPID != rpID {
		return store.Passkey{}, ErrRPIDMismatch{Registered: pk.RPID, Presented: rpID}
	}
	return pk, nil
}

// isUsableRPID reports whether this address can be a relying party at all.
//
// An rpId must be a DOMAIN. A bare IP cannot be one — a protocol constraint, not a certificate one,
// so no amount of TLS work rescues the self-signed-at-an-IP tier, and refusing at registration beats
// minting a credential that could never be used (spec story 4). `localhost` is permitted because
// browsers treat it as a secure context, and it is how quince is reached in development.
//
// Deliberately NOT a fuller validity check: whether a real name is acceptable is the BROWSER's
// judgement — it requires the rpId to be a registrable domain suffix of the origin — and duplicating
// that here would be a second, weaker copy of a rule the client already enforces loudly at
// registration.
func isUsableRPID(rpID string) bool {
	if rpID == "" {
		return false
	}
	if rpID == "localhost" {
		return true
	}
	if net.ParseIP(rpID) != nil {
		return false
	}
	return strings.Contains(rpID, ".")
}

// RPIDSupported reports whether this address can be a relying party, for surfaces that need to say
// so BEFORE a ceremony is attempted (spec story 4: refuse the tier rather than offer a button that
// cannot work). The ceremony enforces the same predicate itself; this is the read-only half.
func RPIDSupported(rpID string) bool { return isUsableRPID(rpID) }
