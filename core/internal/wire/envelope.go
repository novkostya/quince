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
type JobLogChunk struct {
	JobID string `json:"job_id"`
	Chunk string `json:"chunk"`
}

// SessionLocked is the data for session.locked.
type SessionLocked struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"` // user | ttl | vault_crash
}
