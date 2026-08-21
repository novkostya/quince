package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/novkostya/quince/core/internal/auth"
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

// listUDID returns an error rather than a `refuse` bool, so its two callers can tell a REVOKED
// CREDENTIAL from a DATABASE FAULT (quince#1412).
//
// IT COLLAPSED THEM INTO ONE 401. `ScopeOf` returns two distinguishable errors — `ErrCredentialRevoked`
// for a session whose credential is gone, for which 401 is exactly right, and a raw error out of
// `GetPasskey`, which is a read failure. Refusing is correct in both cases; the REASON was wrong in
// one, and quince told a user who was authenticated to authenticate again over a fault no login
// screen can fix. That is the *troubleshooting is actionable* rule failing at the one moment it
// matters, and it did not need new information — the typed error was already in hand and thrown away.
//
// `scope_resource.go` argues the same case correctly twenty lines up, for the resource guard: *"a
// registry that could not answer must not read as a scope violation, or a transient database fault
// becomes a permanent-looking 403."* This is that reasoning carried down.
// THE THIRD SHAPE (spec D8, slice 8c). `GET /api/jobs` and `GET /api/versions` are permitted for a
// scoped principal, so refusing them would take away their own device's history — but the response
// must be narrowed, and both readers already take a udid filter where "" means every device.
//
// A SCOPED PRINCIPAL'S FILTER IS FORCED, NOT DEFAULTED. The caller supplies `?udid=`, so honouring
// it would let a scoped holder read another device's jobs by asking for them — the filter has to
// OVERRIDE the query rather than fill in when it is absent. That distinction is the whole of this
// function, and getting it the other way round would look identical in a test that only ever asks
// for its own device.
//
// THE ADMIN KEEPS THE QUERY, including "" for all devices, so nothing changes for them.
func listUDID(d Deps, r *http.Request) (string, error) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		return r.URL.Query().Get("udid"), nil
	}
	scope, err := d.Auth.ScopeOf(p)
	if err != nil {
		// A principal we cannot resolve gets nothing, rather than the unfiltered list that "" means.
		// WHAT it gets told is now the caller's to decide, and the cause travels with it.
		return "", err
	}
	if scope == "" {
		return r.URL.Query().Get("udid"), nil
	}
	return scope, nil
}

// writeScopeResolutionError maps a failure to resolve WHO is calling, keeping the two causes apart.
//
// ONE PLACE, so the three call sites cannot drift. `handlers_device_notify.go` already spelled this
// out inline for `callerScope`; this is the same mapping named once and shared, which is the fix
// quince#1412 asks for rather than a third copy of it.
func (d Deps) writeScopeResolutionError(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrCredentialRevoked) {
		// THE SESSION'S CREDENTIAL IS GONE, so authenticating again is exactly the remedy.
		writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	// A READ FAILED. The caller is authenticated and there is nothing they can do at a login
	// screen; saying otherwise sends them somewhere that cannot help.
	writeError(w, d.Log, http.StatusInternalServerError, "internal_error",
		"could not resolve who is making this request")
}

// errNoPrincipal — the request reached a handler that must know WHO is calling, with nothing bound
// into its context.
//
// AN INVARIANT VIOLATION, NOT A CLIENT ERROR. Every JSON route runs behind `authGuard`, which binds
// the principal, so this is reachable only by registering a route outside that chain — the same
// class of mistake `/api/ws` was (quince#1380). It is therefore a 500 and not a 401: telling a
// caller to authenticate when they already did sends them somewhere that cannot help.
var errNoPrincipal = errors.New("httpapi: no principal bound to this request")

// callerScope answers WHO IS CALLING — the principal's own scope, and nothing else.
//
// A DIFFERENT QUESTION FROM listUDID'S, AND THE DIFFERENCE IS THE QUERY STRING. `listUDID` decides
// what a LIST endpoint may report on, so for an admin it returns `?udid=` — the admin is allowed to
// ask about any device. That is exactly wrong for deciding whose preference row a WRITE lands in:
// the owner would then be whatever the query said, so an admin could write a scoped holder's row by
// naming them, which is D7's ruling inverted through the write path (quince#1409 review, finding 1).
//
// Being `scopedOwnDevice` is NOT a defence for the caller of this function: that guard constrains
// the PATH udid, and says nothing about the query.
//
// THE ERROR IS RETURNED RATHER THAN A `refuse` BOOL, so the caller can tell a revoked credential
// from a database fault — the distinction `writeScopeResolutionError` maps, and the one both
// helpers in this file make. `listUDID` collapsed them into a single 401 until quince#1412.
func callerScope(d Deps, r *http.Request) (string, error) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		return "", errNoPrincipal
	}
	// "" for an unscoped principal — the admin's own row, which is what 0018 backfills to.
	return d.Auth.ScopeOf(p)
}
