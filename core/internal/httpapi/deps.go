package httpapi

import (
	"log/slog"

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
	Versions       VersionReader
	AllowedOrigins []string
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
