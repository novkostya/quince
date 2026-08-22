package httpapi

import (
	"net/http"
	"net/url"
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

	// scopedProjection — permitted, and the response carries FEWER FIELDS for a scoped principal.
	// The same rows, projected. Slice 8f's, and the first of its kind here.
	//
	// WHY NOT `scopedFiltered`, WHICH IS THE TEMPTING REUSE. That class means *narrowed to the
	// principal's DEVICE*, and every route carrying it filters ROWS by udid. `GET /api/storages`
	// has no device in it at all: a scoped holder sees every storage the admin does, and what is
	// withheld is capacity, health, backend, path and the `will_be_full` projection — the admin's
	// operational picture (spec D3, second exception). Rows and fields are different narrowings
	// and a map that called them one thing would be describing this route wrongly to the next
	// reader, which is the whole job of the map.
	//
	// IT IS NOT A WEAKER `adminOnly` EITHER. The route is not being relaxed; the handler returns
	// DIFFERENT CONTENT per principal, which is a shape the enumeration has to be able to state.
	scopedProjection

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
	"GET /api/config":                         adminOnly,
	"PUT /api/config":                         adminOnly,
	"POST /api/config/storage":                adminOnly,
	"DELETE /api/config/storage/{name}":       adminOnly,
	"POST /api/config/storage/{name}/default": adminOnly,
	"POST /api/config/insecure-transport":     adminOnly,
	// THE SECOND EXCEPTION (spec D3, Operator 2026-08-22): a scoped holder reads this list in
	// order to choose a destination for their own backup. Storage MANAGEMENT stays adminOnly —
	// every other route in this block is untouched.
	"GET /api/storages":                        scopedProjection,
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

	// qn.9. Overview describes ONE version, which belongs to one device, so it is that device's on
	// exactly the reasoning above — and it reaches the version through the same session id, so the
	// guard resolves it by the same SessionVersion hop. Caught by the assertion, again: this is the
	// FIFTH route it has named, and the first four are the paragraph above.
	"GET /api/sessions/{id}/overview": scopedOwnDevice,

	// qn.9 slice 6 — the PRE-UNLOCK tier. It needs no session and no password, and that
	// changes nothing about WHOSE it is: it describes one version of one device, so a
	// scoped principal reaches it exactly when that device is theirs. Needing no unlock is
	// not a reason to need no authorization — it is a route that reads a device name, an
	// iOS version and an installed-app list, which is precisely what D8 scopes.
	"GET /api/versions/{id}/overview": scopedOwnDevice,

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

// enforcesItsOwnPrincipal names the `authExempt` routes whose scope class the scope guard CANNOT
// enforce, and which therefore check the caller themselves.
//
// WHY THIS EXISTS AT ALL. `authGuard` binds the principal, and it skips every route in
// `authExempt` — so for an exempt route no principal is ever bound and `scopeGuardFor` has nothing
// to test. Its `routeScope` entry is inert: a comment that reads like a control.
//
// THAT IS NOT HYPOTHETICAL. `POST /api/config/insecure-transport` was classified `adminOnly` and
// carried, on a configured install, only *is there a session* — never *whose*. A device-scoped
// household member could turn on plain-http transport for the whole install, which is the
// downgrade the banner describes as *anyone who can see the traffic can sign in as you*, including
// as the admin. Found on hardware, 2026-08-21 (quince#1441). The classification was right and
// enforced nowhere.
//
// SO THE COMBINATION IS ASSERTED RATHER THAN TRUSTED. An exempt route whose class is anything but
// `openToAll` must appear here, and appearing here is a claim that the handler does the check
// itself. A route added to `authExempt` with an `adminOnly` entry and no line here fails at
// construction, which is the same shape as the totality assertion above and for the same reason:
// this is a mistake nobody would see, because the wrong version looks exactly like the right one.
var enforcesItsOwnPrincipal = map[string]string{
	"POST /api/config/insecure-transport": "handleInsecureTransportSet refuses a scoped principal " +
		"after authenticating (quince#1441)",

	// THE CERTIFICATE MUTATIONS ARE BOUNDED BY `Configured()`, not by a principal — on a claimed
	// install they refuse every caller, which closes them to a scoped holder as a side effect of
	// closing them to everyone. Verified at each handler rather than assumed.
	"POST /api/onboarding/certificate/apply":   "Configured()-gated in the handler (quince#908 §5)",
	"POST /api/onboarding/certificate/confirm": "Configured()-gated in the handler (quince#908 §5)",
	"POST /api/onboarding/certificate/cancel":  "Configured()-gated in the handler (quince#1158)",

	// THESE FOUR HAVE NO PRINCIPAL CHECK AND NEED NONE, and saying so is the point of listing them.
	// They are ruled pre-auth READS — the page explaining why you cannot log in, its probe pair, and
	// the offline certificate check — so anyone who can reach quince at all can already read them
	// WITHOUT a session. A scoped holder learns nothing they could not learn logged out, which is
	// why this is an accepted exposure rather than a hole.
	//
	// THEIR `adminOnly` ENTRY IS ARGUABLY THE WRONG LABEL and is left alone deliberately: these are
	// Operator-ruled pre-auth routes, so re-classifying them is a decision rather than a tidy-up.
	// Recorded here so the next reader meets the question instead of the silence.
	"GET /api/onboarding/https":        "ruled pre-auth read; readable with no session at all",
	"GET /api/onboarding/probe":        "ruled pre-auth read; readable with no session at all",
	"GET /api/onboarding/probe/nonce":  "ruled pre-auth read; readable with no session at all",
	"POST /api/onboarding/certificate": "ruled pre-auth READ of two named files (quince#908)",
}

// assertExemptRoutesEnforceTheirOwnScope refuses a build where an `authExempt` route claims a scope
// class that nothing can apply to it.
func assertExemptRoutesEnforceTheirOwnScope(patterns []string) {
	var unenforced []string
	for _, p := range patterns {
		method, path, ok := strings.Cut(p, " ")
		if !ok {
			continue
		}
		// Built rather than parsed: `authExempt` reads method and path off a request, and calling
		// it with one is what keeps this assertion honest if that function's shape ever changes.
		if !authExempt(&http.Request{Method: method, URL: &url.URL{Path: path}}) {
			continue
		}
		class, known := scopeOfPattern(p)
		if !known || class == openToAll {
			continue
		}
		if _, declared := enforcesItsOwnPrincipal[p]; !declared {
			unenforced = append(unenforced, p)
		}
	}
	if len(unenforced) > 0 {
		panic("httpapi: authExempt routes whose scope class NOTHING ENFORCES — `authGuard` binds no " +
			"principal for an exempt route, so scopeGuardFor cannot test one. Either check the " +
			"caller inside the handler and record it in enforcesItsOwnPrincipal, or classify the " +
			"route openToAll: " + strings.Join(unenforced, ", "))
	}
}
