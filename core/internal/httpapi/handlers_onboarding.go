package httpapi

import (
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// GET /api/onboarding/step1 → {complete, detected}
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
func (d Deps) handleOnboardingStep1() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, d.Log, http.StatusOK, step1(r))
	}
}

// step1 is the detection, split out so the test states the request and reads the answer.
//
// DETECTION IS A STATE, NOT A BUTTON. `r.TLS != nil` or `X-Forwarded-Proto: https` and the
// step is done — the top-tier user meets zero friction and is never asked to confirm
// something quince can see for itself (G1). Everything else is offered the tiers.
//
// LOOPBACK IS DELIBERATELY NOT COMPLETE, though a session cookie works fine there. The step
// is not "can you log in from this browser" — it is "can you reach quince from your phone",
// and a developer on http://localhost cannot. Telling them the step is finished would be
// exactly the false assurance this rung exists to remove, one layer up.
func step1(r *http.Request) wire.OnboardingStep1 {
	switch {
	case r.TLS != nil:
		return wire.OnboardingStep1{Complete: true, Detected: wire.Step1DetectedTLS}
	case auth.SecureOrigin(r):
		// SecureOrigin is true and r.TLS is nil, so it can only be the forwarded header.
		return wire.OnboardingStep1{Complete: true, Detected: wire.Step1DetectedForwarded}
	default:
		return wire.OnboardingStep1{Complete: false, Detected: wire.Step1DetectedNone}
	}
}
