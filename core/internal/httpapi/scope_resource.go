package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// deviceResolver answers WHICH DEVICE a request concerns, for a route that is permitted only for
// the principal's own.
//
// FOUR SHAPES, WHICH IS WHY THIS IS A TABLE AND NOT A HELPER. quince#1342's inventory found that
// D3's capability list reads as route-level yes/no while the routes are not: the device is in the
// PATH for `/api/devices/{udid}/…`, behind a LOOKUP for jobs and ops, and in the BODY for
// `POST /api/jobs`. A guard that only read paths would pass that last one through unchecked, and it
// is the one that starts a backup.
type deviceResolver func(d Deps, r *http.Request) (udid string, ok bool)

// fromPath reads `{udid}` straight out of the matched pattern.
func fromPath(_ Deps, r *http.Request) (string, bool) {
	u := r.PathValue("udid")
	return u, u != ""
}

// fromJob resolves the device through the job named in the path.
func fromJob(d Deps, r *http.Request) (string, bool) {
	if d.Jobs == nil {
		return "", false
	}
	job, ok := d.Jobs.Job(r.PathValue("id"))
	if !ok {
		// AN UNKNOWN JOB IS NOT A SCOPE FAILURE, and it must not become one: answering 403 here
		// would tell a caller that a job id exists when it does not. Reporting "no device" makes
		// the guard refuse, and the handler's own 404 is what the admin still sees.
		return "", false
	}
	return job.UDID, job.UDID != ""
}

// fromOp resolves the device through the operation named in the path.
func fromOp(d Deps, r *http.Request) (string, bool) {
	if d.Ops == nil {
		return "", false
	}
	op, ok := d.Ops.Op(r.PathValue("op_id"))
	if !ok {
		return "", false
	}
	return op.UDID, op.UDID != ""
}

// fromBody reads the device out of the JSON body, and PUTS THE BODY BACK.
//
// THE ONE RESOLVER THAT MUTATES THE REQUEST, so it is the one worth reading twice. A request body is
// a stream and the handler downstream needs it intact; this buffers it, decodes a copy, and restores
// `r.Body` from the buffer. `bodyLimit` has already capped it at 1 MiB, so the buffer is bounded by
// the same rule that bounds every other read.
//
// A BODY THAT DOES NOT PARSE IS NOT A SCOPE FAILURE. The guard reports "no device" and refuses; the
// handler would have answered 400, and the admin still gets that answer because the guard does not
// run for them.
func fromBody(_ Deps, r *http.Request) (string, bool) {
	if r.Body == nil {
		return "", false
	}
	buf, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(buf))
	if err != nil {
		return "", false
	}
	var probe struct {
		UDID string `json:"udid"`
	}
	if err := json.Unmarshal(buf, &probe); err != nil {
		return "", false
	}
	return probe.UDID, probe.UDID != ""
}

// resourceDevice names how each `scopedOwnDevice` route finds its device.
//
// TOTALITY IS ASSERTED, as it is for `routeScope`: a route classified `scopedOwnDevice` with no
// entry here would be permitted with nothing compared, which is the unchecked route this rung must
// never ship. `assertResolversPresent` panics at construction instead.
var resourceDevice = map[string]deviceResolver{
	"GET /api/devices/{udid}":                fromPath,
	"POST /api/devices/{udid}/encryption":    fromPath,
	"POST /api/devices/{udid}/pair":          fromPath,
	"POST /api/devices/{udid}/pair/validate": fromPath,
	"POST /api/devices/{udid}/reset-working": fromPath,
	"POST /api/devices/{udid}/wifi-sync":     fromPath,
	"PUT /api/devices/{udid}/notifications":  fromPath,

	"GET /api/jobs/{id}":         fromJob,
	"GET /api/jobs/{id}/log":     fromJob,
	"POST /api/jobs/{id}/cancel": fromJob,

	"GET /api/ops/{op_id}": fromOp,

	// THE FOURTH SHAPE. Its device is in the payload, not the path, and it looks like a plain
	// create — which is exactly why it is the one a path-reading guard would miss.
	"POST /api/jobs": fromBody,

	// THE VAULT ROUTES, resolvable since slice 8b-2. A version knows its device; a session
	// knows its version. Both hops existed — `storage.Manager.Version` since the vault surface
	// landed, and `vault.Registry.Get` since sessions did — and only the interfaces did not
	// name them.
	"POST /api/versions/{id}/unlock":        fromVersion,
	"POST /api/sessions/{id}/lock":          fromSession,
	"GET /api/sessions/{id}/browse":         fromSession,
	"GET /api/sessions/{id}/file/{file_id}": fromSession,
}

// unresolvableToday are `scopedOwnDevice` routes with NO way to find their device yet, and they
// FAIL CLOSED until there is one.
//
// `POST /api/versions/{id}/unlock` needs a version→device lookup: `VersionReader` is
// `Versions(udid)` and has no `Version(id)`. The three `/api/sessions/{id}/…` vault routes need a
// session→device lookup, and `VaultBrowse` exposes none. Both are new reader surface on other
// subsystems — a different reviewable claim from comparing a device to a scope, so slice 8b-2.
//
// REFUSED RATHER THAN PASSED THROUGH, and the direction is the whole point. This temporarily
// contradicts D3's *browse and download from a version — yes*; it affects nobody, because nothing
// mints a scoped credential until slice 9, and an unchecked route is the one thing that must not
// ship. A permitted-but-uncompared route would look identical in a passing test suite.
// unresolvableToday is EMPTY since slice 8b-2, and it stays as a named, asserted-empty map
// rather than being deleted: `assertResolversPresent` is what makes a future
// `scopedOwnDevice` route with no resolver a panic instead of an unchecked route, and it
// needs somewhere to record a deliberate exception if one is ever right again.
var unresolvableToday = map[string]string{}

// assertResolversPresent panics if a `scopedOwnDevice` route has neither a resolver nor a stated
// reason to fail closed.
func assertResolversPresent(patterns []string) {
	var missing []string
	for _, p := range patterns {
		class, ok := scopeOfPattern(p)
		if !ok || class != scopedOwnDevice {
			continue
		}
		if _, has := resourceDevice[p]; has {
			continue
		}
		if _, known := unresolvableToday[p]; known {
			continue
		}
		missing = append(missing, p)
	}
	if len(missing) > 0 {
		panic("httpapi: scopedOwnDevice routes with no device resolver (qn.13 slice 8b) — add each " +
			"to resourceDevice, or to unresolvableToday with its reason: " + strings.Join(missing, ", "))
	}
}

// scopedResourceGuard permits a route only for the principal's OWN device.
//
// THE ADMIN IS UNTOUCHED, which is every principal today, so this is a no-op until slice 9 mints a
// scoped credential.
func scopedResourceGuard(d Deps, pattern string, next http.HandlerFunc) http.HandlerFunc {
	resolve, resolvable := resourceDevice[pattern]
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			next(w, r)
			return
		}
		scope, err := d.Auth.ScopeOf(p)
		if err != nil {
			writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if scope == "" {
			next(w, r) // the admin
			return
		}
		if !resolvable {
			// FAIL CLOSED. See `unresolvableToday` for why these four exist and what closes them.
			writeError(w, d.Log, http.StatusForbidden, "forbidden", scopeRefusalDetail)
			return
		}
		udid, found := resolve(d, r)
		if !found || udid != scope {
			// ONE ANSWER FOR "NOT YOURS" AND "NOT THERE", deliberately. Distinguishing them would
			// let a scoped holder enumerate which job ids and device udids exist by reading the
			// difference between 403 and 404 — the shape `ErrRPIDMismatch` and quince#1259's
			// refusal both avoid, applied to a resource rather than a credential.
			writeError(w, d.Log, http.StatusForbidden, "forbidden", scopeRefusalDetail)
			return
		}
		next(w, r)
	}
}

// fromVersion resolves the device through the version named in the path.
//
// AN ERROR IS NOT "NO DEVICE". `VersionReader.Version` distinguishes a missing row from a failed
// query, and so does this: a registry that could not answer must not read as a scope violation, or
// a transient database fault becomes a permanent-looking 403.
func fromVersion(d Deps, r *http.Request) (string, bool) {
	if d.Versions == nil {
		return "", false
	}
	v, ok, err := d.Versions.Version(r.PathValue("id"))
	if err != nil || !ok {
		return "", false
	}
	return v.UDID, v.UDID != ""
}

// fromSession resolves the device through the session's version. Two hops, both cheap.
func fromSession(d Deps, r *http.Request) (string, bool) {
	if d.VaultBrowse == nil || d.Versions == nil {
		return "", false
	}
	versionID, ok := d.VaultBrowse.SessionVersion(r.PathValue("id"))
	if !ok {
		// A LOCKED OR EXPIRED SESSION REPORTS NO DEVICE, so the guard refuses rather than admitting
		// a request under a session that no longer exists.
		return "", false
	}
	v, ok, err := d.Versions.Version(versionID)
	if err != nil || !ok {
		return "", false
	}
	return v.UDID, v.UDID != ""
}
