package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

// Deps is everything NewRouter needs. The read interfaces are consumer-defined here so
// httpapi imports neither the demo provider nor (later) the real device/job/version
// subsystems — they satisfy these structurally and are wired in main.
type Deps struct {
	Log            *slog.Logger
	Version        string
	Config         *config.Service
	Auth           *auth.Service
	Bus            *bus.Bus
	Devices        DeviceReader
	Jobs           JobReader
	JobControl     JobControl
	Versions       VersionReader
	VersionAdmin   VersionAdmin
	Muxer          MuxerControl
	Ops            DeviceOps
	WorkingReset   WorkingReset
	AllowedOrigins []string
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
	ResetWorking(udid string) (status int, reason string)
}

// UnavailableWorkingReset is the WorkingReset used when no backup engine is wired (--demo, or a
// misconfigured deploy): reset reports 503 honestly (no silent no-op).
type UnavailableWorkingReset struct{}

func (UnavailableWorkingReset) ResetWorking(string) (int, string) {
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
	StartBackup(udid, transport, retryOf string) (job wire.Job, status int, reason string)
	CancelJob(id string) (job wire.Job, status int, reason string)
}

// UnavailableJobControl is the JobControl used when no backup engine is wired (--demo, which loops
// scripted jobs for the read surface, or a misconfigured deploy): the command surface reports 503
// honestly (no silent no-op), never fabricating a job.
type UnavailableJobControl struct{}

func (UnavailableJobControl) StartBackup(string, string, string) (wire.Job, int, string) {
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
