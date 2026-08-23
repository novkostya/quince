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

	// EventMessagesIndexing reports how far the Messages projection scan has got (qn.10 D2/D3).
	//
	// IT EXISTS BECAUSE THE ROUTE CANNOT CARRY IT. Opening the first conversation in a session
	// builds the projection — ~18 s on a real backup — and a synchronous JSON response has
	// nowhere to put progress. D3 named this socket as where it would come from, so the
	// reader's onProgress callback is the seam and this is the frame.
	//
	// IT IS DEVICE-BEARING, and that is a ruling rather than a shape (quince#1483): the event
	// describes one session, a session belongs to one version, a version to one device. See
	// the note in eventscope.go for why the global `session.locked` is NOT the precedent it
	// looks like.
	EventMessagesIndexing = "messages.indexing"
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

// MessagesIndexing is the data for messages.indexing: a live count of messages projected so far.
//
// THERE IS NO TOTAL, AND ITS ABSENCE IS THE DESIGN — the same reason messages.Progress has none.
// The parser does not count rows before streaming them, so any total here would be invented, and
// *state honesty* forbids inventing one. A client renders an indeterminate indicator carrying a
// live count, never a percentage. Indeterminate RENDERING is not the same as no event at all, and
// conflating the two is what briefly turned a settled decision back into a question (qn.10 D3).
//
// IT CARRIES ITS DEVICE for the same reason JobLogChunk does: the socket decides at send time
// which principal may receive a frame (qn.13 D8), and it cannot do that for a payload whose
// device is knowable only by looking the session up. The producer knows it for free.
type MessagesIndexing struct {
	SessionID string `json:"session_id"`
	UDID      string `json:"udid"`
	Messages  int64  `json:"messages"`
}

// DeviceUDID — see the note on `Device.DeviceUDID`.
func (m MessagesIndexing) DeviceUDID() string { return m.UDID }
