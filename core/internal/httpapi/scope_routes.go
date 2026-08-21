package httpapi

import (
	"net/http"
	"strings"
)

// scopeClass says what a DEVICE-SCOPED principal may do with a route. The admin is unaffected by
// every value here.
//
// CLASSIFICATION LIVES BESIDE THE PATTERN, NOT IN A PATH SWITCH, and that is forced rather than
// preferred. `authExempt` matches on `r.Method + " " + r.URL.Path`, which cannot express
// `DELETE /api/auth/passkeys/{id}` — and the codebase deliberately refuses prefix matching, because
// a prefix *"would silently exempt every future onboarding step"*. Go sets `r.Pattern` only AFTER
// routing, so a middleware wrapping the mux cannot read it. The one place the pattern exists as a
// string is the registration call, so that is where the decision is recorded.
type scopeClass int

const (
	// scopeUnset is the zero value and is NOT a class. A route registered without a decision trips
	// the startup assertion rather than defaulting — the same shape as `store.Scope`, and for the
	// same reason: a default here would GRANT, and this rung has spent five slices removing those.
	scopeUnset scopeClass = iota

	// adminOnly — a scoped principal is refused outright (spec D3).
	adminOnly

	// scopedOwnDevice — permitted, but only for the principal's own device. The narrowing is slice
	// 8b's; classifying it now is what makes the table total today.
	scopedOwnDevice

	// scopedFiltered — permitted, and the RESPONSE must be narrowed to the principal's device.
	// Slice 8c's.
	scopedFiltered

	// openToAll — no device in it, and nothing a scoped principal must not see: health, its own
	// session, its own notification subscription.
	openToAll
)

// routeScope is the decision for every registered pattern.
//
// TOTAL BY ASSERTION, NOT BY CONVENTION. `assertRoutesClassified` runs at server construction and
// panics on a pattern with no entry, so a route added without a decision fails on the first request
// in any test rather than serving a scoped principal something nobody considered.
var routeScope = map[string]scopeClass{
	// ── Admin only. Not their device, or not their authority (spec D3). ──────────────────────────
	"GET /api/config":                          adminOnly,
	"PUT /api/config":                          adminOnly,
	"POST /api/config/storage":                 adminOnly,
	"DELETE /api/config/storage/{name}":        adminOnly,
	"POST /api/config/storage/{name}/default":  adminOnly,
	"POST /api/config/insecure-transport":      adminOnly,
	"GET /api/storages":                        adminOnly,
	"POST /api/storages/probe":                 adminOnly,
	"POST /api/storages/probe/hook":            adminOnly,
	"POST /api/storages/zfs/hostkey":           adminOnly,
	"POST /api/storages/zfs/hostkey/trust":     adminOnly,
	"POST /api/storages/zfs/key":               adminOnly,
	"GET /api/storages/zfs/helper":             adminOnly,
	"POST /api/storages/{name}/recheck":        adminOnly,
	"GET /api/onboarding/https":                adminOnly,
	"GET /api/onboarding/probe":                adminOnly,
	"GET /api/onboarding/probe/nonce":          adminOnly,
	"POST /api/onboarding/certificate":         adminOnly,
	"POST /api/onboarding/certificate/apply":   adminOnly,
	"POST /api/onboarding/certificate/confirm": adminOnly,
	"POST /api/onboarding/certificate/cancel":  adminOnly,

	// CREDENTIAL MANAGEMENT IS THE ADMIN'S, and D9 is explicit that revocation is too. A scoped
	// holder issuing or removing credentials is the escalation this rung exists to prevent.
	"GET /api/auth/passkeys":                  adminOnly,
	"DELETE /api/auth/passkeys/{id}":          adminOnly,
	"PATCH /api/auth/passkeys/{id}":           adminOnly,
	"POST /api/auth/passkeys/register/begin":  adminOnly,
	"POST /api/auth/passkeys/register/finish": adminOnly,
	"PUT /api/auth/password":                  adminOnly,
	"DELETE /api/auth/password":               adminOnly,
	"POST /api/auth/reauth/begin":             adminOnly,
	"POST /api/auth/reauth/finish":            adminOnly,

	// THE DEVICES LIST IS UNREACHABLE, NOT NARROWED (spec D8). A scoped holder's Home is their own
	// device page, which reads `GET /api/devices/{udid}`. Returning a one-row list here would be the
	// helpful-looking version of the thing D8 forbids.
	"GET /api/devices":         adminOnly,
	"POST /api/devices/rescan": adminOnly,

	// ── Their own device only. Narrowing is slice 8b. ────────────────────────────────────────────
	"GET /api/devices/{udid}":                scopedOwnDevice,
	"POST /api/devices/{udid}/encryption":    scopedOwnDevice,
	"POST /api/devices/{udid}/pair":          scopedOwnDevice,
	"POST /api/devices/{udid}/pair/validate": scopedOwnDevice,
	"POST /api/devices/{udid}/reset-working": scopedOwnDevice,
	"POST /api/devices/{udid}/wifi-sync":     scopedOwnDevice,
	"PUT /api/devices/{udid}/notifications":  scopedOwnDevice,
	// ISSUING CREDENTIALS IS THE ADMIN'S (spec D3), and not by symmetry: a scoped holder who
	// could mint an enrolment secret for their own device could hand out further credentials to
	// it, which is delegation quince never granted. These sit under `/api/devices/{udid}/` and
	// are still adminOnly — the prefix is not the classification.
	"POST /api/devices/{udid}/enrolments":        adminOnly,
	"GET /api/devices/{udid}/enrolments":         adminOnly,
	"DELETE /api/devices/{udid}/enrolments/{id}": adminOnly,
	"GET /api/jobs/{id}":                         scopedOwnDevice,
	"GET /api/jobs/{id}/log":                     scopedOwnDevice,
	"POST /api/jobs/{id}/cancel":                 scopedOwnDevice,
	"GET /api/ops/{op_id}":                       scopedOwnDevice,
	"DELETE /api/versions/{id}":                  adminOnly, // D3: deleting a version is NOT theirs

	// `POST /api/jobs` NAMES ITS DEVICE IN THE BODY, not the path — the fourth shape, and the one
	// most easily missed because it looks like a plain create.
	"POST /api/jobs": scopedOwnDevice,

	// ── Permitted, response narrowed. Slice 8c. ──────────────────────────────────────────────────
	"GET /api/jobs":     scopedFiltered,
	"GET /api/versions": scopedFiltered,

	// ── No device in it. ─────────────────────────────────────────────────────────────────────────
	"GET /api/health":                      openToAll,
	"GET /api/auth/status":                 openToAll,
	"POST /api/auth/login":                 openToAll,
	"POST /api/auth/setup":                 openToAll,
	"POST /api/auth/logout":                openToAll,
	"POST /api/auth/passkeys/login/begin":  openToAll,
	"POST /api/auth/passkeys/login/finish": openToAll,
	"POST /api/auth/setup/passkey/begin":   openToAll,
	"POST /api/auth/setup/passkey/finish":  openToAll,
	// Pre-auth: there is no principal yet, so there is no scope to narrow. What bounds these is
	// the enrolment secret, not this table (qn.13 D4).
	"POST /api/enrol/passkey/begin":  openToAll,
	"POST /api/enrol/passkey/finish": openToAll,

	// NOTIFICATIONS ARE THEIRS TO MANAGE (spec D3: "their own notification preference"). The SEND
	// path is what scopes what they receive (D7); subscribing is not the place to confine them,
	// because a subscription outlives a scope change.
	"GET /api/notifications":                       openToAll,
	"POST /api/notifications/subscriptions":        openToAll,
	"DELETE /api/notifications/subscriptions/{id}": openToAll,
	"POST /api/notifications/test":                 openToAll,

	// ── The vault surface (qn.8). A version belongs to a device, so these are that device's. ─────
	//
	// FOUND BY THE ASSERTION, NOT BY THE INVENTORY. quince#1342's route list was built by grepping
	// the registration file for a `"METHOD /api/..."` literal and these four were in it — but the
	// classification above was written from that list and dropped them anyway. The panic named all
	// four on the first test that built a server, which is the whole argument for a totality gate
	// over a careful reading.
	"POST /api/versions/{id}/unlock":        scopedOwnDevice,
	"POST /api/sessions/{id}/lock":          scopedOwnDevice,
	"GET /api/sessions/{id}/browse":         scopedOwnDevice,
	"GET /api/sessions/{id}/file/{file_id}": scopedOwnDevice,

	// THE CATCH-ALL. It answers 404 for anything under /api/ that matched nothing, so it carries no
	// data and must not refuse a scoped principal — a 403 here would tell them a route EXISTS that
	// they cannot reach, which is a different fact from "no such route".
	"/api/": openToAll,
}

// scopeOfPattern returns the class for a registered pattern, and whether one was recorded.
func scopeOfPattern(pattern string) (scopeClass, bool) {
	c, ok := routeScope[pattern]
	return c, ok && c != scopeUnset
}

// refusesScoped reports whether this class refuses a device-scoped principal outright.
func (c scopeClass) refusesScoped() bool { return c == adminOnly }

// scopeGuardFor wraps a handler with the refusal its class implies.
//
// ONLY `adminOnly` IS ENFORCED HERE. The other classes are permitted at this layer and narrowed
// deeper — per-resource in slice 8b, per-response in 8c — because a guard that could only answer
// yes or no would have to refuse `GET /api/devices/{udid}` outright, which is the route a scoped
// holder's Home is built on.
func scopeGuardFor(d Deps, pattern string, next http.HandlerFunc) http.HandlerFunc {
	class, ok := scopeOfPattern(pattern)
	if !ok {
		// Unreachable past `assertRoutesClassified`; a belt-and-braces refusal rather than a
		// silent grant if that assertion is ever removed.
		return func(w http.ResponseWriter, r *http.Request) {
			writeError(w, d.Log, http.StatusForbidden, "forbidden", "this route has no scope decision")
		}
	}
	if class == scopedOwnDevice {
		return scopedResourceGuard(d, pattern, next)
	}
	if !class.refusesScoped() {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			// No principal means an exempt route, which is `authExempt`'s business rather than this
			// guard's. Nothing admin-only is exempt, so this cannot be reached today.
			next(w, r)
			return
		}
		scope, err := d.Auth.ScopeOf(p)
		if err != nil {
			writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if scope != "" {
			// NAMES WHAT IT IS AND WHY, per *troubleshooting is actionable*. A bare 403 would leave
			// a household member unable to tell a bug from a boundary.
			writeError(w, d.Log, http.StatusForbidden, "forbidden",
				scopeRefusalDetail)
			return
		}
		next(w, r)
	}
}

// assertRoutesClassified panics if a registered pattern has no scope decision.
//
// A PANIC AT CONSTRUCTION, NOT AN ERROR AT REQUEST TIME. The server is built in every test that
// touches the API, so an unclassified route fails the suite immediately and by name. Deferring it to
// the request would mean the failure appears only on the route nobody wrote a test for — which is
// the same route nobody classified.
//
// It is the totality gate quince#1342's inventory called for, in the only place that can see both
// the registered set and the decisions.
func assertRoutesClassified(patterns []string) {
	var missing []string
	for _, p := range patterns {
		if _, ok := scopeOfPattern(p); !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		panic("httpapi: routes registered with no scope decision (qn.13 D3/D8) — add each to " +
			"routeScope in scope_routes.go: " + strings.Join(missing, ", "))
	}
}

// scopedMux records every pattern it registers so the assertion above can see the whole set.
//
// A WRAPPER RATHER THAN 60 EDITED CALL SITES: the registration reads exactly as it did, and the
// classification cannot be forgotten at a call site because it is not written at one.
type scopedMux struct {
	mux      *http.ServeMux
	deps     Deps
	patterns []string
}

func (m *scopedMux) HandleFunc(pattern string, h http.HandlerFunc) {
	m.patterns = append(m.patterns, pattern)
	m.mux.HandleFunc(pattern, scopeGuardFor(m.deps, pattern, h))
}

func (m *scopedMux) Handle(pattern string, h http.Handler) {
	m.HandleFunc(pattern, h.ServeHTTP)
}

// scopeRefusalDetail is the sentence a scoped principal meets at an admin-only route.
//
// IT NAMES THE BOUNDARY AND THE ACCESS THEY DO HAVE. A bare 403 leaves a household member unable to
// tell a bug from a rule, which is *troubleshooting is actionable* failing at the moment a user is
// most likely to conclude quince is broken.
const scopeRefusalDetail = "this is an administrator action; your access is limited to one device"
