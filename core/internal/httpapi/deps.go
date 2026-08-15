package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Deps is everything NewRouter needs. The read interfaces are consumer-defined here so
// httpapi imports neither the demo provider nor (later) the real device/job/version
// subsystems — they satisfy these structurally and are wired in main.
type Deps struct {
	Log     *slog.Logger
	Version string
	Mode    string // normal | demo | public_demo — see HealthResponse (spec story 5)
	// DemoResetMinutes is reported on GET /api/health (spec story 6). main gates it on the mode
	// that actually resets, so httpapi reports whatever it is handed and decides nothing.
	DemoResetMinutes int
	Config           *config.Service
	Auth             *auth.Service
	Bus              *bus.Bus
	Devices          DeviceReader
	Jobs             JobReader
	JobControl       JobControl
	Versions         VersionReader
	VersionAdmin     VersionAdmin
	Storages         StorageReader
	// ZFSKeyDir is the directory POST /api/storages/zfs/key generates or finds helper keys in. Empty
	// means `config.DefaultZFSKeyDir`, which is what production leaves it as (quince#818).
	//
	// A DIRECTORY SINCE quince#989, because there is one key per PARENT DATASET and the filename is
	// derived from it. It was a single path, which is what let a second storage be handed the first
	// storage's key — and with it the first storage's parent, silently.
	//
	// IT IS A DEPENDENCY SO TESTS CAN BE HERMETIC, not so an operator can retarget it: the handler
	// takes no path from the request precisely so the endpoint has no reachable target but quince's
	// own, and a test writing into `/data` would either fail or dirty the box.
	ZFSKeyDir string
	// ZFSKnownHostsPath is where POST /api/storages/zfs/hostkey/trust records a confirmed host key.
	// Empty means `config.DefaultZFSKnownHosts` (quince#912) — a dependency for the same
	// hermetic-test reason as ZFSKeyPath above, and equally not a retargeting knob.
	ZFSKnownHostsPath string
	Muxer             MuxerControl
	Ops               DeviceOps
	WorkingReset      WorkingReset
	// Reconcile publishes `reconciling` on GET /api/health (qn.6i). Nil wherever no asynchronous
	// reconciliation exists — `--demo`, the admin CLIs, every test router — and nil reports false,
	// which is the truth there rather than a default: with no runner there is no provisional state.
	Reconcile      ReconcileReporter
	AllowedOrigins []string
	// StorageRequired reports whether quince has NO storage declared, in which case it is in the
	// first-run setup state and setupGuard refuses everything outside setupAllowed (qn.6e, Operator
	// ruling 2026-08-07).
	//
	// A FUNCTION, NOT A BOOL, because the condition CLEARS WHILE THE PROCESS RUNS: adding a storage
	// through POST /api/config/storage ends the mode with no restart, and a value captured at wiring
	// time would leave the daemon refusing its own API after setup had succeeded. Read from the live
	// config on each request, which is what makes "the zero-storage condition IS the state" true
	// rather than aspirational.
	//
	// NIL MEANS NEVER REQUIRED, so --demo and every test that does not wire it are unaffected.
	StorageRequired func() bool
	// Proxies decides whether X-Forwarded-For may be believed (quince#464). Nil behaves as
	// "trust nobody", which is the shipping default and today's behaviour.
	Proxies *auth.TrustedProxies
	// ProbeNonces backs the two probe endpoints (Operator ruling 2026-08-14). NewRouter fills it
	// when a caller left it nil, so a test router and `--demo` both get a working probe rather than
	// a nil dereference — the store holds no configuration and nothing outside this package needs
	// to build one.
	ProbeNonces *probeNonces
	// Keeper is what serves TLS, and the certificate trial points it at a pair WITHOUT writing
	// `config.yml` (quince#908 slice 5). `*tlsx.Keeper` satisfies it; nil means this quince has no
	// TLS listener, and `NewRouter` substitutes a stand-in that refuses with a stated reason so the
	// apply route answers 503 rather than pretending.
	//
	// AN INTERFACE SO `httpapi` DOES NOT IMPORT `tlsx` for one method, and so a test can drive the
	// trial without minting a real certificate for the daemon to load.
	Keeper certKeeper
	// CertTrial holds the one certificate being tried out (quince#908 slice 5). NewRouter fills it
	// from `Keeper` when a caller left it nil, for `ProbeNonces`' reason.
	CertTrial *certTrial
	// Store is the app DB, for surfaces that read rows rather than a domain model. Passkey
	// registration is the first: a credential list is rows, and a service in front of four SQL
	// statements would be a facade rather than a boundary (qn.6k).
	Store *store.Store
	// Passkeys holds in-flight WebAuthn ceremonies — in memory, two-minute TTL, single use. NIL
	// WHEREVER PASSKEYS CANNOT BE BEGUN (the admin CLIs, test routers that do not exercise them),
	// and the routes are not registered when it is, so a nil here is unreachable rather than a
	// panic waiting.
	Passkeys *auth.PasskeyCeremonies
	// Reauth and Proofs back the qn.6n re-authentication pair. Nil in the same places Passkeys is,
	// and the routes are registered under the same condition, so a nil here is unreachable for the
	// same reason rather than a second thing to remember.
	Reauth *auth.ReauthCeremonies
	Proofs *auth.Proofs
	// PasswordAdmin backs PUT/DELETE /api/auth/password (qn.6m). NIL IN --demo, ON PURPOSE — and
	// unlike Passkeys above, the routes ARE still registered when it is nil, because NewRouter
	// installs UnavailablePasswordAdmin and the surface must refuse with a stated reason rather
	// than quietly vanish.
	PasswordAdmin PasswordAdmin
}

// WorkingReset drives POST /api/devices/{udid}/reset-working (qn.5b, contracts §1): discard a
// device's dirty working/ so the next backup starts clean from latest/. The real implementation is
// *backup.Engine (it holds the per-UDID single-flight, so it can refuse 409 while a backup runs);
// UnavailableWorkingReset stands in for --demo / when no engine is wired. Consumer-defined here
// (primitives only) so httpapi imports no backup subsystem — same pattern as JobControl. Returns an
// HTTP status + reason so the handler maps outcomes without cross-package sentinel errors (202 =
// reset done / already clean; 409 a backup is running; 404 unknown device). Never touches a
// committed version.
type WorkingReset interface {
	ResetWorking(udid, storageID string) (status int, reason string)
}

// UnavailableWorkingReset is the WorkingReset used when no backup engine is wired (--demo, or a
// misconfigured deploy): reset reports 503 honestly (no silent no-op).
type UnavailableWorkingReset struct{}

func (UnavailableWorkingReset) ResetWorking(string, string) (int, string) {
	return http.StatusServiceUnavailable,
		"the backup engine is unavailable (running --demo, or no device backend is configured)"
}

// JobControl drives POST /api/jobs and POST /api/jobs/{id}/cancel (contracts §1). The real
// implementation is *backup.Engine (non-demo); UnavailableJobControl stands in for --demo and
// when no engine is wired. Consumer-defined here (primitives + wire.Job) so httpapi imports no
// backup subsystem — same pattern as DeviceOps/VersionAdmin. Returns an HTTP status + reason so
// the handler maps outcomes without cross-package sentinel errors (202 = accepted; 409 already
// running; 422 bad/auto transport; 404 unknown device or job).
type JobControl interface {
	StartBackup(udid, transport, storageID, retryOf string) (job wire.Job, status int, reason string)
	CancelJob(id string) (job wire.Job, status int, reason string)
}

// UnavailableJobControl is the JobControl used when no backup engine is wired (--demo, which loops
// scripted jobs for the read surface, or a misconfigured deploy): the command surface reports 503
// honestly (no silent no-op), never fabricating a job.
type UnavailableJobControl struct{}

func (UnavailableJobControl) StartBackup(string, string, string, string) (wire.Job, int, string) {
	return wire.Job{}, http.StatusServiceUnavailable,
		"the backup engine is unavailable (running --demo, or no device backend is configured)"
}

func (UnavailableJobControl) CancelJob(string) (wire.Job, int, string) {
	return wire.Job{}, http.StatusServiceUnavailable, "the backup engine is unavailable"
}

// VersionAdmin performs the destructive version operations (contracts §1 DELETE
// /api/versions/{id} → 202, a confirmed destructive action). The real implementation is
// *storage.Manager (non-demo) or the demo provider; UnavailableVersionAdmin stands in when no
// storage subsystem is wired. Consumer-defined here (primitives only) so httpapi imports no
// storage subsystem — same pattern as DeviceReader/MuxerControl/DeviceOps. Returns an HTTP
// status so the handler maps outcomes without cross-package sentinel errors (202 = accepted).
type VersionAdmin interface {
	Delete(id string) (status int, err error)
}

// UnavailableVersionAdmin is the VersionAdmin used when no storage subsystem is wired: delete
// reports 503 honestly (no silent no-op).
type UnavailableVersionAdmin struct{}

func (UnavailableVersionAdmin) Delete(string) (int, error) {
	return http.StatusServiceUnavailable, nil
}

// DeviceOps drives the pair/validate/encryption device operations and the Op lifecycle
// (contracts §1/§2). The real implementation is *deviceops.Manager (non-demo) or the demo
// provider (--demo); UnavailableDeviceOps stands in when neither is wired. Consumer-defined
// here (primitives + wire.Op only) so httpapi imports no deviceops subsystem — same pattern
// as DeviceReader/MuxerControl. Pair/Encryption/Validate return an HTTP status + reason so the
// handler maps outcomes without cross-package sentinel errors (202/200 = success).
type DeviceOps interface {
	Pair(ctx context.Context, udid string) (opID string, status int, reason string)
	Validate(ctx context.Context, udid string) (paired bool, status int, reason string)
	Encryption(ctx context.Context, udid, action, password, oldPassword, newPassword string) (opID string, status int, reason string)
	WifiSync(ctx context.Context, udid, action string) (opID string, status int, reason string)
	Op(opID string) (wire.Op, bool)
}

// UnavailableDeviceOps is the DeviceOps used when no device-ops subsystem is wired: every
// action reports 503 honestly (no silent no-op), and no op is ever found.
type UnavailableDeviceOps struct{}

func (UnavailableDeviceOps) Pair(context.Context, string) (string, int, string) {
	return "", http.StatusServiceUnavailable, "device operations are unavailable"
}
func (UnavailableDeviceOps) Validate(context.Context, string) (bool, int, string) {
	return false, http.StatusServiceUnavailable, "device operations are unavailable"
}
func (UnavailableDeviceOps) Encryption(context.Context, string, string, string, string, string) (string, int, string) {
	return "", http.StatusServiceUnavailable, "device operations are unavailable"
}
func (UnavailableDeviceOps) WifiSync(context.Context, string, string) (string, int, string) {
	return "", http.StatusServiceUnavailable, "device operations are unavailable"
}
func (UnavailableDeviceOps) Op(string) (wire.Op, bool) { return wire.Op{}, false }

// MuxerControl drives POST /api/devices/rescan and reports muxer-supervision health for
// /api/health (qn.2b; qn.4c made it plural — quince may supervise usbmuxd AND netmuxd). The real
// implementation is the muxsup.Group (devices.manage_muxer: true); UnmanagedMuxer stands in for
// --demo, where quince owns no muxer at all. Consumer-defined here so httpapi imports no muxer
// subsystem — same pattern as DeviceReader.
type MuxerControl interface {
	// Rescan restarts the managed USB muxer (netmuxd is never restarted — it would tear a live
	// Wi-Fi backup, (bz)); accepted → 202, else 409 with reason.
	Rescan(ctx context.Context) (accepted bool, reason string)
	// MuxersHealth reports one entry per configured muxer daemon for the health payload.
	MuxersHealth() []MuxerHealth
}

// UnmanagedMuxer is the MuxerControl for --demo, where there are no muxers at all: rescan is
// always refused (409) and health honestly reports an empty list. (An external-but-dialed muxer
// is NOT this case — it appears in the list with managed:false; see muxsup.Group.)
type UnmanagedMuxer struct{}

func (UnmanagedMuxer) Rescan(context.Context) (bool, string) {
	return false, "no muxer is managed by quince (devices.manage_muxer: false, or --demo)"
}
func (UnmanagedMuxer) MuxersHealth() []MuxerHealth { return []MuxerHealth{} }

// ReconcileReporter answers "is a reconciliation pass queued or running" for GET /api/health
// (qn.6i). Consumer-defined here so httpapi imports no storage subsystem — the same pattern as
// DeviceReader and MuxerControl. `*storage.Runner` satisfies it.
//
// THERE IS NO Unavailable… STAND-IN FOR THIS ONE, unlike every other optional dep above, and the
// asymmetry is deliberate: those refuse with a 503 because "no subsystem wired" is a degraded answer
// to a real question. Here the absence IS the answer — a daemon with no runner never serves a
// provisional registry — so a nil check reporting `false` is honest, where a stand-in type would be
// ceremony around one boolean.
type ReconcileReporter interface {
	Reconciling() bool
}

// DeviceReader serves the device REST reads.
type DeviceReader interface {
	Devices() []wire.Device
	Device(udid string) (wire.Device, bool)
}

// JobReader serves the job REST reads. Jobs returns a page plus the next cursor ("" = last
// page). udid "" means all devices. JobLog returns the full-so-far log text for a job
// (contracts §1: GET /api/jobs/{id}/log — the live tail is the WS job.log stream); a known
// job with no log yet returns ("", true), an unknown job ("", false).
type JobReader interface {
	Jobs(udid, cursor string, limit int) (jobs []wire.Job, nextCursor string)
	Job(id string) (wire.Job, bool)
	JobLog(id string) (log string, ok bool)
}

// VersionReader serves the version REST reads. udid "" means all devices.
type VersionReader interface {
	Versions(udid string) []wire.Version
}

// Empty is the no-op reader used when not in --demo mode: real providers land in qn.2+.
// It reports empty results honestly (never nil slices → JSON []).
type Empty struct{}

func (Empty) Devices() []wire.Device                        { return []wire.Device{} }
func (Empty) Device(string) (wire.Device, bool)             { return wire.Device{}, false }
func (Empty) Jobs(string, string, int) ([]wire.Job, string) { return []wire.Job{}, "" }
func (Empty) Job(string) (wire.Job, bool)                   { return wire.Job{}, false }
func (Empty) JobLog(string) (string, bool)                  { return "", false }
func (Empty) Versions(string) []wire.Version                { return []wire.Version{} }

// StorageReader serves the storage REST surface (contracts §1 GET /api/storages, POST
// /api/storages/{id}/recheck; qn.6c). Consumer-defined here in primitives + wire types only, so
// httpapi imports no storage subsystem — the same pattern as VersionReader.
//
// Recheck is the reachability check, never the backend-selection probe: it creates nothing and
// selects nothing, which is what keeps G5b intact (quince#438).
type StorageReader interface {
	Storages(udid string) []wire.Storage
	Recheck(name string) (wire.Storage, bool)
	// JobsOn reports the jobs currently bound to a storage, so `DELETE /api/config/storage/{name}`
	// can refuse while a backup is running on it (qn.6g, Operator ruling 2026-08-06 — quince#577).
	// A READ, which is why it sits here rather than on JobControl.
	JobsOn(storageID string) []string
}

// UnavailableStorages stands in when no storage subsystem is wired: an empty list rather than a
// 503, because "quince knows about no storages" is a truthful answer a client can render, and the
// demo/degraded paths already rely on reads degrading rather than failing.
type UnavailableStorages struct{}

func (UnavailableStorages) Storages(string) []wire.Storage { return nil }
func (UnavailableStorages) Recheck(string) (wire.Storage, bool) {
	return wire.Storage{}, false
}

// No storage subsystem means no jobs bound to one, so a forget is never refused for liveness here.
// Nil rather than an error, for the same reason the reads above degrade rather than fail.
func (UnavailableStorages) JobsOn(string) []string { return nil }

// PasswordAdmin drives PUT and DELETE /api/auth/password (qn.6m D6, contracts §1) — changing the
// admin password, and removing it to go passwordless.
//
// THE DEMO CARVE-OUT IS AN UNWIRED CAPABILITY, NOT AN `if demo` BRANCH, and that shape is quince#841's
// instruction rather than a preference: "quince has no demo flag at the API layer … no handler
// contains an `if demo` branch". The public demo presets a shared password that every visitor is
// told, so letting one visitor change or remove it would lock out everybody else — but the refusal
// belongs in the wiring, beside the four stand-ins already here, so that adding a second pattern
// does not become the first.
//
// The real implementation is *auth.Service; UnavailablePasswordAdmin stands in when it is not wired
// (--demo). Returns an HTTP status + reason so the handler maps outcomes without reaching for
// cross-package sentinels — the same shape as WorkingReset and JobControl above.
type PasswordAdmin interface {
	// CHANGED BY qn.6n: it takes the proof store, what was PRESENTED, and the session — rules 1 and
	// 3 mean a session alone no longer authorises a password change, and the proof must be checked
	// against the session that minted it.
	ChangePassword(proofs *auth.Proofs, pres auth.Presented, next, sessionID, clientIP string) error
	// AND SO DOES REMOVAL, since qn.6n rule 2: the password cannot authorise its own removal, so
	// this now takes what was presented and refuses everything except a passkey proof.
	RemovePassword(proofs *auth.Proofs, pres auth.Presented, rpID, sessionID, clientIP string) error
}

// UnavailablePasswordAdmin is the PasswordAdmin used when none is wired (--demo): both operations
// refuse with 503 and a STATED REASON, rather than the surface quietly hiding its buttons.
//
// `no silent caps or fallbacks` — the UI is expected to show the controls and report this sentence,
// because a demo visitor who cannot find the setting learns nothing, where one who is told why
// learns what the real thing does.
type UnavailablePasswordAdmin struct{}

// ErrPasswordAdminUnavailable is what both refusals return. A sentinel rather than a fresh error per
// call, so the handler maps it to 503 by identity and cannot mistake it for an internal failure.
var ErrPasswordAdminUnavailable = errors.New(
	"the admin password cannot be changed here: this is the public demo, and its password is shared " +
		"with every visitor")

func (UnavailablePasswordAdmin) ChangePassword(*auth.Proofs, auth.Presented, string, string, string) error {
	return ErrPasswordAdminUnavailable
}

func (UnavailablePasswordAdmin) RemovePassword(*auth.Proofs, auth.Presented, string, string, string) error {
	return ErrPasswordAdminUnavailable
}

// zfsKeyDir is the directory helper keys live in — `ZFSKeyDir` when a test set one, otherwise
// quince's own. One accessor rather than the same two lines at each call site, since quince#1038
// gave it a second caller and a third would have been the drift.
func (d Deps) zfsKeyDir() string {
	if d.ZFSKeyDir != "" {
		return d.ZFSKeyDir
	}
	return config.DefaultZFSKeyDir
}
