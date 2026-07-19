// Package demo is the in-memory provider behind `quince serve --demo` (stack D9): it emits
// fixture devices, a scripted backup job, and fixture versions so the UI track can build
// every screen against live data with no hardware. The same state backs the REST reads and
// the WS event stream, so a browser reload after live churn shows consistent data. Fixture
// data is deterministic and presentable — README/release screenshots come from here.
package demo

import (
	"log/slog"
	"sort"
	"sync"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/wire"
)

// Provider holds the mutable demo world. It implements httpapi's DeviceReader/JobReader/
// VersionReader interfaces structurally.
type Provider struct {
	mu       sync.RWMutex
	bus      *bus.Bus
	log      *slog.Logger
	devices  map[string]wire.Device
	order    []string // device display order
	jobs     map[string]wire.Job
	versions map[string]wire.Version
	verOrder []string // version display order (newest first)
}

// NewProvider builds a provider seeded with deterministic fixtures. It does NOT start the
// live timeline — call Run for that (golden tests use the static seed only).
func NewProvider(b *bus.Bus, log *slog.Logger) *Provider {
	p := &Provider{
		bus:      b,
		log:      log,
		devices:  map[string]wire.Device{},
		jobs:     map[string]wire.Job{},
		versions: map[string]wire.Version{},
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

func strptr(s string) *string   { return &s }
func f64ptr(f float64) *float64 { return &f }
