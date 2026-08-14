package httpapi

import (
	"net/http"
	"strings"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// GET /api/onboarding/https → {complete, detected, unencrypted_code?}
//
// THE FIRST ONBOARDING SURFACE IN THE PRODUCT, and it sets the shape steps 2 and 3 inherit
// (qn.6f slice 2, design §9). That is the argument for it being this narrow: it answers one
// question, in one boolean plus the reason, and nothing else. A richer step-1 payload would
// be a precedent every later step cites.
//
// PRE-AUTH — the fifth authExempt route, by exact path (Operator ruling 2026-08-02 on
// quince#501: "Of course it's pre-auth, that's the only viable option"). The chicken-and-egg
// is the whole rung: on plain http to a LAN address the browser discards the session cookie,
// so the page explaining exactly that would otherwise sit behind the door the defect locks.
//
// BY EXACT PATH, not by prefix, and that is a constraint rather than a style: authExempt
// switches on `r.Method + " " + r.URL.Path` with no prefix support, so an `/api/onboarding/*`
// exemption would mean changing the MATCHER and silently exempting every future onboarding
// step. Step 1 only.
//
// It carries NO device, storage or version data — nothing an unauthenticated caller should
// not see. What it discloses is whether the connection it arrived on is encrypted, which the
// caller already knows, having made it.
func (d Deps) handleOnboardingHTTPS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, d.Log, http.StatusOK, detectHTTPS(r, d.Proxies))
	}
}

// detectHTTPS is the detection, split out so the test states the request and reads the answer.
//
// DETECTION IS A STATE, NOT A BUTTON. `r.TLS != nil` or `X-Forwarded-Proto: https` and the
// step is done — the top-tier user meets zero friction and is never asked to confirm
// something quince can see for itself (G1). Everything else is offered the tiers.
//
// LOOPBACK IS DELIBERATELY NOT COMPLETE, though a session cookie works fine there. The step
// is not "can you log in from this browser" — it is "can you reach quince from your phone",
// and a developer on http://localhost cannot. Telling them the step is finished would be
// exactly the false assurance this rung exists to remove, one layer up.
func detectHTTPS(r *http.Request, trusted *auth.TrustedProxies) wire.OnboardingHTTPS {
	switch {
	case r.TLS != nil:
		return wire.OnboardingHTTPS{Complete: true, Detected: wire.HTTPSDetectedTLS}
	case auth.SecureOrigin(r, trusted):
		// SecureOrigin is true and r.TLS is nil, so it can only be the forwarded header.
		return wire.OnboardingHTTPS{Complete: true, Detected: wire.HTTPSDetectedForwarded}
	default:
		return wire.OnboardingHTTPS{
			Complete: false, Detected: wire.HTTPSDetectedNone,
			UnencryptedCode: unencryptedCode(r),
		}
	}
}

// unencryptedCode says WHICH of the four shapes of evidence produced `detected: none`
// (quince#940 §2 + quince#939 §7). Called only from the default branch above, where
// `auth.SecureOrigin` has already returned false.
//
// IT TAKES NO TRUSTED-PROXY LIST, and that is deliberate rather than an omission: every branch below
// is decided by the request's own headers, and the one that mentions trust is REACHED rather than
// re-derived — see its comment. A parameter this function did not read would misdescribe what it
// depends on.
//
// IT READS THE SAME INPUTS `SecureOrigin` READS AND DOES NOT RE-DECIDE ANYTHING. That predicate owns
// the verdict — a second copy of it is what `SecureOrigin`'s own doc comment refuses to allow, for
// the reason quince#497 gives: copies drift, and this one decides whether a cookie survives. What
// this adds is a classification of a case that has ALREADY been decided as not-secure, so there is
// no branch here that could disagree with it.
//
// THE ORDER IS EVIDENCE-FIRST, NOT REMEDY-FIRST. `X-Forwarded-Proto` present is a stronger fact than
// `X-Forwarded-For` present, so it is tested first; the header's own value then separates *the proxy
// disagrees with me about trust* from *the proxy is telling me the truth and the truth is plain*.
func unencryptedCode(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	switch {
	case proto == "":
		// NO SCHEME HEADER. `X-Forwarded-For` is the only evidence left that anything is in front,
		// and it is a HINT rather than a verdict — nginx does not set it by default either, so a
		// deployment with a correctly-configured proxy can land in either branch. The client's copy
		// must not state this as fact (quince#939 §7).
		if r.Header.Get("X-Forwarded-For") != "" {
			return wire.UnencryptedProxyNotForwardingScheme
		}
		return wire.UnencryptedNoProxySeen
	case strings.EqualFold(proto, "https"):
		// SecureOrigin already said no, and the header says https, so the only way both are true is
		// that the list is configured and this peer is not on it. Reached rather than assumed: the
		// `!trusted.Configured()` arm of SecureOrigin returns TRUE, so an unconfigured list cannot
		// arrive here at all.
		return wire.UnencryptedProxyUntrusted
	default:
		// PRESENT AND NOT `https`. The proxy is doing its job and reporting plain http upstream of
		// itself; quince is not the thing that is wrong. Any non-https value lands here — see the
		// constant for why that is honest rather than lossy.
		return wire.UnencryptedProxyReportsPlain
	}
}
