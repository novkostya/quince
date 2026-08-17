package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/novkostya/quince/core/internal/config"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/tlsx"
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
		got := detectHTTPS(r, d.Proxies)
		// THE TWO ANSWERS ARE INDEPENDENT AND BOTH MAY BE SET (quince#940 §1 + §2). One says why
		// this CONNECTION is not encrypted; the other says why quince's OWN certificate is not
		// serving. A user behind no proxy with a mismatched pair has two true answers and needs
		// both — which is why this is a second field rather than another `unencrypted_code` value.
		//
		// A NIL `Config` MEANS NO ANSWER RATHER THAN A PANIC: the admin CLIs build routers without
		// one, and this route is reachable on any of them.
		if d.Config != nil {
			got.TLSUnusableCode = tlsUnusableCode(d.Config.Current(), d.Keeper, time.Now())
		}
		writeJSON(w, d.Log, http.StatusOK, got)
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

// tlsUnusableCode says WHY quince's own certificate is not being served, when the config asks for
// one and the daemon has none (quince#940 §1).
//
// # THE RULING DRAWS THE LINE AT KIND-VERSUS-DETAIL
//
// Operator, 2026-08-14: `GET /api/onboarding/https` MAY say WHICH KIND of failure it was, pre-auth,
// on a CLAIMED install — *the certificate and the key do not match* · *the file could not be read* ·
// *it has expired* — carrying NO filesystem path and NO OpenSSL text. `CheckTLS`'s raw `Detail` is
// AUTHENTICATED and reaches a signed-in session only.
//
// A pre-auth observer already knows TLS is not working; they are reading this page over http. The
// classification adds a fact about the operator's own configuration, which is not a secret from
// somebody who can already see the symptom. A PATH is different in kind — it is filesystem layout —
// and quince#908 §3's argument does not stretch to cover it, because that argument holds only where
// an install can be claimed outright. This one is claimed.
//
// # `tlsx.Inspect` ALREADY DRAWS EXACTLY THAT LINE
//
// Its `Outcome` is a closed enum with no path in it; its `Reason` names the file by design
// (quince#514). So the ruling's two halves are already two fields on a type this rung shipped last
// week, and only `Outcome` crosses to a pre-auth caller.
//
// # AND `CheckTLS` IS NOT THE SOURCE, THOUGH §1 NAMES IT
//
// `CheckTLS` is a STARTUP GATE: `main` treats `!req.OK()` as fatal, so a daemon that is serving has
// `Unusable: false` by construction — an unusable pair stopped the process instead. The state §1
// describes is the RUNTIME edit, where `subscribeTLS` warns and keeps serving, and it lives in the
// Keeper. Recorded on quince#940 so the next reader does not wire up a field that is always false.
//
// # WHY THE GUARD IS `!HasCertificate()` AND NOT A STALENESS FLAG
//
// This is a PRE-AUTH endpoint and `tlsx.Inspect` reads two files, so the guard decides how often an
// unauthenticated caller can make the daemon touch the disk. It is O(1) in every healthy case: only
// a config that ASKS for TLS while the daemon is serving NO certificate reaches the read.
//
// THAT IS ALSO THE ONLY CASE THAT CAN REACH THIS PAGE, which is what makes the narrow guard
// sufficient rather than merely cheap. A failed ROTATION leaves the previous certificate loaded, so
// `plainHalf` still redirects http to https — that user is on the TLS half and gets
// `detected: tls`, never this branch. The case where somebody is reading this over plain http with
// TLS configured is precisely the case where nothing loaded at all.
func tlsUnusableCode(cfg config.Config, keeper certKeeper, now time.Time) string {
	if !cfg.TLS.Enabled() || keeper == nil || keeper.HasCertificate() {
		return ""
	}
	// NO HOSTNAME, deliberately. `wrong_host` is a question about the name a user is heading for,
	// which this endpoint has no opinion about — the certificate STEP asks it, with the name the
	// user typed. Passing `r.Host` here would report `wrong_host` for every operator reaching a
	// working certificate by IP, which is not a fault and is not what this branch is about.
	// AND NO CURRENT HOST, for the neighbouring reason: this branch reports WHY a configured pair
	// did not load, and nothing it renders asks about coverage of any address.
	rep := tlsx.Inspect(cfg.TLS.CertFile, cfg.TLS.KeyFile, "", "", now)
	if rep.Outcome == tlsx.OutcomeUsable {
		// CONFIGURED, INSPECTS CLEAN, AND STILL NOT LOADED. Nothing here can say why — the pair was
		// readable a moment ago and the daemon is not serving it — so this reports the honest
		// unclassifiable answer the ruling asks for rather than inventing one. It is reachable: a
		// file can become readable between the failed load and this request.
		return wire.TLSUnusableUnknown
	}
	return rep.Outcome
}
