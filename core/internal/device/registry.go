package device

import (
	"log/slog"
	"sync"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/muxd"
	"github.com/novkostya/quince/core/internal/wire"
)

// Registry merges N muxer sources into one UDID-keyed device table. Each source feeds it
// through a muxd.Sink (see Sink); presence edges are keyed by (source, udid, transport) so
// one source dropping never clears a transport another source still holds. It implements
// httpapi.DeviceReader structurally (Devices/Device).
type Registry struct {
	mu  sync.RWMutex
	bus *bus.Bus
	log *slog.Logger
	// sourceID → udid → transport("usb"/"wifi") → last_seen (RFC3339 UTC)
	sources map[string]map[string]map[string]string
	order   []string // stable display order of udids (append on first appearance)
	// udid → lockdown identity overlaid on the muxd-minimal shell (qn.3 enrichment).
	identity map[string]Identity
	// lastBackup resolves Device.last_backup from the version registry (qn.4c finding (v)).
	// Nil until wired (e.g. --demo, tests): the field then stays null, never a guess.
	lastBackup func(udid string) (wire.LastBackup, bool)
}

// NewRegistry returns an empty registry publishing device.* events to b.
func NewRegistry(b *bus.Bus, log *slog.Logger) *Registry {
	return &Registry{
		bus:      b,
		log:      log,
		sources:  map[string]map[string]map[string]string{},
		identity: map[string]Identity{},
	}
}

// SetLastBackupSource wires where Device.last_backup comes from — the version registry
// (storage.Manager.LastBackup). It is READ on every merge rather than cached, so the field is
// automatically right after a restart, after an adopted version is discovered, and after a
// version is deleted; there is no second copy to go stale. Call once, before serving.
// Tradeoff, declared: the lookup runs while the registry lock is held, so a device read costs one
// indexed SQLite query per device (tens of microseconds against a 100 ms budget). Caching would
// buy nothing but a way to be wrong.
func (r *Registry) SetLastBackupSource(fn func(udid string) (wire.LastBackup, bool)) {
	r.mu.Lock()
	r.lastBackup = fn
	r.mu.Unlock()
}

// AnnounceBackup re-publishes a device because its backup history just changed (the engine calls
// it after a successful commit). The value itself comes from the source above; this only makes
// the change LIVE — the dashboard card updates without a page refresh (qn.4a findings (iv)+(v)).
// A device that is not currently present emits nothing: there is no device on screen to update,
// and the next read is honest anyway.
func (r *Registry) AnnounceBackup(udid string) {
	r.mu.RLock()
	dev, present := r.mergedLocked(udid)
	r.mu.RUnlock()
	if present {
		r.bus.PublishEvent(wire.EventDeviceUpdated, dev)
	}
}

// --- httpapi.DeviceReader ---

// Devices returns the merged devices in display order (never nil → JSON []).
func (r *Registry) Devices() []wire.Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]wire.Device, 0, len(r.order))
	for _, udid := range r.order {
		if dev, ok := r.mergedLocked(udid); ok {
			out = append(out, dev)
		}
	}
	return out
}

// Device returns one merged device by UDID.
func (r *Registry) Device(udid string) (wire.Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mergedLocked(udid)
}

// --- muxd.Sink factory ---

// Sink returns the muxd.Sink for one muxer source (sourceID = the muxer address, unique per
// client). The client calls Reset() on each (re)connect and Apply() per presence edge.
func (r *Registry) Sink(sourceID string) muxd.Sink { return sourceSink{reg: r, source: sourceID} }

type sourceSink struct {
	reg    *Registry
	source string
}

func (s sourceSink) Reset()              { s.reg.reset(s.source) }
func (s sourceSink) Apply(ev muxd.Event) { s.reg.apply(s.source, ev) }

// --- event application (demo lock discipline: mutate under lock, publish after unlock) ---

type emission struct {
	typ  string
	data any
}

// apply folds one presence edge into the table and emits at most one device.* event: an
// attach that makes a transport newly present in the merged table (device.attached), or a
// detach that removes the last holder of a transport (device.detached, dropping the device
// when it was its last transport). Edge refreshes and edges shadowed by another source are
// suppressed to keep the WS quiet.
func (r *Registry) apply(source string, ev muxd.Event) {
	r.mu.Lock()
	before := r.transportPresentLocked(ev.UDID, ev.Transport)
	switch ev.Kind {
	case muxd.Attached:
		r.setEdgeLocked(source, ev.UDID, ev.Transport, wire.Now())
	case muxd.Detached:
		r.clearEdgeLocked(source, ev.UDID, ev.Transport)
	}
	after := r.transportPresentLocked(ev.UDID, ev.Transport)

	var emit *emission
	switch {
	case ev.Kind == muxd.Attached && !before && after:
		r.ensureOrderLocked(ev.UDID)
		dev, _ := r.mergedLocked(ev.UDID)
		emit = &emission{wire.EventDeviceAttached, wire.DeviceEvent{Device: dev, Transport: ev.Transport}}
	case ev.Kind == muxd.Detached && before && !after:
		dev, ok := r.mergedLocked(ev.UDID)
		if !ok { // last transport gone → device leaves the table
			dev = r.deviceShellLocked(ev.UDID)
			r.dropFromOrderLocked(ev.UDID)
		}
		emit = &emission{wire.EventDeviceDetached, wire.DeviceEvent{Device: dev, Transport: ev.Transport}}
	}
	r.mu.Unlock()

	if emit != nil {
		r.bus.PublishEvent(emit.typ, emit.data)
	}
}

// reset drops all of source's edges (called on each (re)connect before the muxer's replay).
// For every edge that leaves the merged table (no other source holds that transport) it
// emits device.detached; a device that had no other source is dropped from the table. The
// replay's subsequent Apply calls re-add whatever is still attached — so a device that
// detached while we were disconnected stays gone (no phantom).
func (r *Registry) reset(source string) {
	r.mu.Lock()
	prev := r.sources[source]
	delete(r.sources, source)
	var emits []emission
	dropped := map[string]bool{}
	for udid, edges := range prev {
		for transport := range edges {
			if r.transportPresentLocked(udid, transport) {
				continue // another source still holds this transport
			}
			dev, ok := r.mergedLocked(udid)
			if !ok {
				dev = r.deviceShellLocked(udid)
				if !dropped[udid] {
					r.dropFromOrderLocked(udid)
					dropped[udid] = true
				}
			}
			emits = append(emits, emission{wire.EventDeviceDetached, wire.DeviceEvent{Device: dev, Transport: transport}})
		}
	}
	r.mu.Unlock()

	for _, e := range emits {
		r.bus.PublishEvent(e.typ, e.data)
	}
}

// --- locked helpers (caller holds r.mu for read or write) ---

// mergedLocked folds every source's edges for udid into one Device. A transport is present
// with the newest last_seen across the sources holding it; absent transports are omitted
// ("present keys only"). Returns false when the device has no live transport.
func (r *Registry) mergedLocked(udid string) (wire.Device, bool) {
	var usbSeen, wifiSeen string // newest RFC3339 (lexicographic == chronological for UTC Z)
	for _, byUDID := range r.sources {
		edges := byUDID[udid]
		if edges == nil {
			continue
		}
		if s, ok := edges[muxd.TransportUSB]; ok && s > usbSeen {
			usbSeen = s
		}
		if s, ok := edges[muxd.TransportWiFi]; ok && s > wifiSeen {
			wifiSeen = s
		}
	}
	if usbSeen == "" && wifiSeen == "" {
		return wire.Device{}, false
	}
	dev := r.deviceShellLocked(udid)
	lastSeen := ""
	if usbSeen != "" {
		dev.Transports.USB = &usbSeen
		if usbSeen > lastSeen {
			lastSeen = usbSeen
		}
	}
	if wifiSeen != "" {
		dev.Transports.WiFi = &wifiSeen
		if wifiSeen > lastSeen {
			lastSeen = wifiSeen
		}
	}
	dev.LastSeen = lastSeen
	return dev, true
}

// deviceShellLocked is the identity base for a UDID: the muxer-unknowable fields sit at
// their honest default, with any lockdown Identity (qn.3) overlaid on top. Paired/
// BackupEncryption default to the literal "unknown" (NOT the "" zero value, which would
// violate the contract enum); an overlay leaves a field at its default when its Identity
// value is empty ("not determined"), never guessing.
func (r *Registry) deviceShellLocked(udid string) wire.Device {
	dev := wire.Device{
		UDID:             udid,
		Paired:           "unknown",
		BackupEncryption: "unknown",
	}
	if id, ok := r.identity[udid]; ok {
		dev.Name = id.Name
		dev.Model = id.Model
		dev.IOSVersion = id.IOSVersion
		if id.Paired != "" {
			dev.Paired = id.Paired
		}
		if id.BackupEncryption != "" {
			dev.BackupEncryption = id.BackupEncryption
		}
	}
	if r.lastBackup != nil {
		if lb, ok := r.lastBackup(udid); ok {
			dev.LastBackup = &lb
		}
	}
	return dev
}

// Identity is the lockdown-derived identity qn.3 overlays onto the muxd-minimal shell
// (populated from ideviceinfo / idevicepair validate). An empty-string field means "not
// determined" and leaves the honest default in place — the registry never guesses. All
// fields are strings, so Identity is comparable and Enrich can cheaply detect real change.
type Identity struct {
	Name             string
	Model            string
	IOSVersion       string
	Paired           string // yes | no | unknown ("" → leave default)
	BackupEncryption string // on | off | unknown ("" → leave default)
}

// Enrich overlays lockdown identity onto the device keyed by udid. When the device is
// currently present AND a field actually changed, it emits device.updated (contracts §3);
// the overlay is retained even while the device is absent, so a later re-attach renders the
// known identity immediately (the enrichment driver refreshes it on each attach). Follows
// the registry's lock discipline: mutate under the lock, publish after unlock.
func (r *Registry) Enrich(udid string, id Identity) {
	r.mu.Lock()
	prev, had := r.identity[udid]
	changed := !had || prev != id
	if changed {
		r.identity[udid] = id
	}
	dev, present := r.mergedLocked(udid)
	r.mu.Unlock()

	if changed && present {
		r.bus.PublishEvent(wire.EventDeviceUpdated, dev)
	}
}

func (r *Registry) transportPresentLocked(udid, transport string) bool {
	for _, byUDID := range r.sources {
		if edges := byUDID[udid]; edges != nil {
			if _, ok := edges[transport]; ok {
				return true
			}
		}
	}
	return false
}

func (r *Registry) setEdgeLocked(source, udid, transport, seen string) {
	byUDID := r.sources[source]
	if byUDID == nil {
		byUDID = map[string]map[string]string{}
		r.sources[source] = byUDID
	}
	edges := byUDID[udid]
	if edges == nil {
		edges = map[string]string{}
		byUDID[udid] = edges
	}
	edges[transport] = seen
}

func (r *Registry) clearEdgeLocked(source, udid, transport string) {
	byUDID := r.sources[source]
	if byUDID == nil {
		return
	}
	edges := byUDID[udid]
	if edges == nil {
		return
	}
	delete(edges, transport)
	if len(edges) == 0 {
		delete(byUDID, udid)
	}
}

func (r *Registry) ensureOrderLocked(udid string) {
	for _, u := range r.order {
		if u == udid {
			return
		}
	}
	r.order = append(r.order, udid)
}

func (r *Registry) dropFromOrderLocked(udid string) {
	for i, u := range r.order {
		if u == udid {
			r.order = append(r.order[:i], r.order[i+1:]...)
			return
		}
	}
}
