// Package demo is the in-memory provider behind `quince serve --demo` (stack D9): it emits
// fixture devices, a scripted backup job, and fixture versions so the UI track can build
// every screen against live data with no hardware. The same state backs the REST reads and
// the WS event stream, so a browser reload after live churn shows consistent data. Fixture
// data is deterministic and presentable — README/release screenshots come from here.
package demo

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/wire"
)

// Provider holds the mutable demo world. It implements httpapi's DeviceReader/JobReader/
// JobControl/VersionReader interfaces structurally.
type Provider struct {
	mu       sync.RWMutex
	bus      *bus.Bus
	log      *slog.Logger
	baseCtx  context.Context // set by Run; scripted jobs stop when it is cancelled
	devices  map[string]wire.Device
	order    []string // device display order
	jobs     map[string]wire.Job
	jobLog   map[string][]string // per-job accumulated log lines (GET /api/jobs/{id}/log)
	running  map[string]*demoRun // in-flight scripted jobs by UDID (single-flight, qn.4b)
	versions map[string]wire.Version
	verOrder []string           // version display order (newest first)
	ops      map[string]wire.Op // pair/encryption ops (GET /api/ops/{id}; qn.3 DeviceOps)
}

// NewProvider builds a provider seeded with deterministic fixtures. It does NOT start the
// live timeline — call Run for that (golden tests use the static seed only).
func NewProvider(b *bus.Bus, log *slog.Logger) *Provider {
	p := &Provider{
		bus:      b,
		log:      log,
		devices:  map[string]wire.Device{},
		jobs:     map[string]wire.Job{},
		jobLog:   map[string][]string{},
		running:  map[string]*demoRun{},
		versions: map[string]wire.Version{},
		ops:      map[string]wire.Op{},
	}
	p.seed()
	return p
}

// Devices returns the fixture devices in display order.
func (p *Provider) Devices() []wire.Device {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]wire.Device, 0, len(p.order))
	for _, udid := range p.order {
		if d, ok := p.devices[udid]; ok {
			out = append(out, d)
		}
	}
	return out
}

// Device returns one device by UDID.
func (p *Provider) Device(udid string) (wire.Device, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.devices[udid]
	return d, ok
}

// Jobs returns jobs (optionally filtered by udid), newest first. The fixture set is small,
// so the cursor is ignored and next_cursor is always "".
func (p *Provider) Jobs(udid, _ string, limit int) ([]wire.Job, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]wire.Job, 0, len(p.jobs))
	for _, j := range p.jobs {
		if udid == "" || j.UDID == udid {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, ""
}

// Job returns one job by id.
func (p *Provider) Job(id string) (wire.Job, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	j, ok := p.jobs[id]
	return j, ok
}

// JobLog returns the full-so-far log text for a job (GET /api/jobs/{id}/log). A known job
// with no log yet returns ("", true); an unknown job returns ("", false) → 404.
func (p *Provider) JobLog(id string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.jobs[id]; !ok {
		return "", false
	}
	lines := p.jobLog[id]
	if len(lines) == 0 {
		return "", true
	}
	return strings.Join(lines, "\n") + "\n", true
}

// Versions returns versions (optionally filtered by udid) in display order.
func (p *Provider) Versions(udid string) []wire.Version {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]wire.Version, 0, len(p.verOrder))
	for _, id := range p.verOrder {
		v, ok := p.versions[id]
		if !ok {
			continue
		}
		if udid == "" || v.UDID == udid {
			out = append(out, v)
		}
	}
	return out
}

// Delete removes a fixture version (satisfies httpapi.VersionAdmin so --demo exercises the
// destructive path). Returns 202 on success, 404 for an unknown id.
func (p *Provider) Delete(id string) (int, error) {
	p.mu.Lock()
	v, ok := p.versions[id]
	if !ok {
		p.mu.Unlock()
		return http.StatusNotFound, nil
	}
	delete(p.versions, id)
	for i, vid := range p.verOrder {
		if vid == id {
			p.verOrder = append(p.verOrder[:i], p.verOrder[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventVersionDeleted, v)
	return http.StatusAccepted, nil
}

func strptr(s string) *string   { return &s }
func f64ptr(f float64) *float64 { return &f }

// Storages serves the demo's fixture storage list (qn.6c story 5).
//
// TWO storages, one deliberately UNREACHABLE — the spec's fixture, and it is the interesting one:
// a demo where everything is plugged in cannot show the state the rung exists to model, and the
// selector in story 9 has to render a disabled option with a reason attached.
//
// `will_be_full` appears only when a udid is asked for, matching the ruled device-independence of
// the list. The demo answers `true` for the shuttle because no version of any device lives there,
// which is exactly the warning a user should see before choosing it.
func (p *Provider) Storages(udid string) []wire.Storage {
	p.mu.RLock()
	defer p.mu.RUnlock()

	internalFull, shuttleFull := false, true
	if udid != "" {
		internalFull = true
		for _, id := range p.verOrder {
			if v, ok := p.versions[id]; ok && v.UDID == udid && !v.Missing {
				internalFull = false
				break
			}
		}
	}
	code, reason := "missing_medium", "the path is readable but carries no quince storage marker — "+
		"if this is a removable disk, it is not mounted"

	out := []wire.Storage{{
		ID: demoStorageInternal, Name: "internal", Path: "/backups", Backend: "reflink",
		Default: true, Reachable: true,
	}, {
		ID: demoStorageShuttle, Name: "shuttle", Path: "/mnt/shuttle", Backend: "unknown",
		Default: false, Reachable: false,
		UnreachableCode: &code, UnreachableReason: &reason,
	}}
	if udid != "" {
		out[0].WillBeFull = &internalFull
		out[1].WillBeFull = &shuttleFull
	}
	return out
}

// Recheck re-probes one demo storage. The shuttle STAYS unreachable: a demo whose disk appears
// because you pressed a button would teach the operator that the button fixes things, when what it
// actually does is look again.
func (p *Provider) Recheck(id string) (wire.Storage, bool) {
	for _, s := range p.Storages("") {
		if s.ID == id {
			return s, true
		}
	}
	return wire.Storage{}, false
}

// Stable fixture ids, so the e2e selector can address a storage by id rather than by position.
const (
	demoStorageInternal = "01JSTORAGEDEMOINTERNAL00"
	demoStorageShuttle  = "01JSTORAGEDEMOSHUTTLE000"
)
