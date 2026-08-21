package wire

import "time"

// Envelope is the single WebSocket frame shape (contracts §3): {type, ts, data}. The
// server pushes these; commands go via REST.
type Envelope struct {
	Type string `json:"type"`
	TS   string `json:"ts"`
	Data any    `json:"data"`
}

// WS event type strings (contracts §3).
const (
	EventHello          = "hello"
	EventDeviceAttached = "device.attached"
	EventDeviceDetached = "device.detached"
	EventDeviceUpdated  = "device.updated"
	EventJobUpdated     = "job.updated"
	EventJobLog         = "job.log"
	EventOpUpdated      = "op.updated"
	EventVersionCreated = "version.created"
	EventVersionDeleted = "version.deleted"
	EventSessionLocked  = "session.locked"
	// EventConfigUpdated says the configuration SURFACE changed — refetch `GET /api/config`
	// (Operator ruling 2026-08-17, quince#1162, option C).
	//
	// IT CARRIES NO DATA, DELIBERATELY. Putting the document on the wire would be a second copy of
	// what `GET /api/config` already serves, free to drift from it — and to be useful it would have
	// to carry `warnings`, `source`, `file_text` and `discarded` too, which is the endpoint. The
	// event says THAT it changed; the client asks WHAT.
	//
	// "SURFACE" RATHER THAN "the running configuration", and the word is load-bearing: this also
	// fires when a hand-edit is REFUSED, where `config` is deliberately unchanged and
	// `discarded`/`warnings`/`source` have flipped. That is the case an open page most needs to hear
	// about, because the banner it should be showing is the one saying the file is not in force.
	EventConfigUpdated = "config.updated"
)

// Now is the RFC3339 UTC timestamp stamped on envelopes and any live-generated wire time.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// NewEnvelope wraps a payload in a timestamped envelope.
func NewEnvelope(typ string, data any) Envelope {
	return Envelope{Type: typ, TS: Now(), Data: data}
}

// Hello is the first frame every client receives after the WS handshake (contracts §3).
type Hello struct {
	ServerVersion string `json:"server_version"`
	Time          string `json:"time"`
}

// DeviceEvent is the data for device.attached / device.detached: the Device plus the
// transport edge that changed (contracts §3).
type DeviceEvent struct {
	Device
	Transport string `json:"transport"` // usb | wifi
}

// JobLogChunk is the data for job.log.
//
// IT CARRIES THE DEVICE, AND THAT FIELD IS NEW IN qn.13. Every other device-bearing event
// already names its device — `Device.UDID`, `Job.UDID`, `Version.UDID`, `Op.UDID` — and this
// one named only the job. The socket now has to decide which principal an envelope may reach
// (spec D8), and it cannot do that for a frame whose device is knowable only by looking the
// job up. Resolving it at SEND time would put a store lookup on the hot path of a log stream
// and answer differently once the job is gone; the producer knows it for free.
//
// ADDITIVE ON THE WIRE: a client that ignores the field is unaffected.
type JobLogChunk struct {
	JobID string `json:"job_id"`
	UDID  string `json:"udid"`
	Chunk string `json:"chunk"`
}

// SessionLocked is the data for session.locked.
type SessionLocked struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"` // user | ttl | vault_crash
}

// DeviceUDID — see the note on `Device.DeviceUDID`.
//
// `DeviceEvent` embeds `Device`, so it ALREADY inherits that method and this declaration is
// redundant; it is written out anyway because the embedding is easy to remove by accident and
// the inherited version would vanish silently with it. `e.UDID` rather than `e.Device.UDID`
// because staticcheck QF1008 refuses the longer form.
func (e DeviceEvent) DeviceUDID() string { return e.UDID }
func (c JobLogChunk) DeviceUDID() string { return c.UDID }
