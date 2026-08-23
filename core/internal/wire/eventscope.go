package wire

// EventDevice reports which DEVICE an envelope concerns, and whether it concerns one at all.
//
// WHY THIS EXISTS: qn.13. `/api/ws` broadcasts every envelope to every connected client, and until
// this rung that was correct — there was one principal, so "everyone" and "the admin" were the same
// set. A device-scoped credential makes them different, and a socket that streams every device's
// events to a confined holder defeats the confinement claim (spec D8) without anybody doing
// anything wrong.
//
// TOTAL OVER THE EVENT CONSTANTS, WHICH IS THE POINT. This is `qn.12`'s routing-table shape (its
// D4/G1) applied to the socket: every declared event type is classified here, and the gate asserts
// it, so a THIRTEENTH event added later fails the build instead of silently reaching a principal it
// was never considered for. A `default` that returned "not device-scoped" would be the same
// permissive default this rung has been removing all the way down — one that leaks by omission.
//
// THE SECOND RETURN IS NOT DECORATION. `("", false)` means *this event belongs to no device* and is
// a global fact — `hello`, `session.locked`, `config.updated`. `("", true)` cannot happen and is not
// a state to design for: an event that concerns a device always carries it. Callers must not read a
// bare empty string as "send it to everyone".
func EventDevice(env Envelope) (udid string, scoped bool) {
	switch env.Type {
	// GLOBAL — about quince itself, not about any one device.
	//
	// `session.locked` IS NOT THE PRECEDENT A SESSION-SHAPED EVENT SHOULD FOLLOW, and it looks
	// exactly like one. It names a session, as `messages.indexing` does, and lands in the other
	// class — so read the REASON rather than the subject. It is global because withholding it
	// would break the socket rather than confine it: a client must learn its own session ended,
	// and a scoped holder that never heard would keep showing decrypted views of a session that
	// is gone. Nothing breaks when a progress frame is withheld; the holder sees no count.
	//
	// So the two point the same way once the reason is read, and `messages.indexing` is
	// device-bearing (quince#1483). A future session-shaped event belongs here only if a client
	// that misses it is left WRONG rather than uninformed.
	case EventHello, EventSessionLocked, EventConfigUpdated:
		return "", false

	// DEVICE-BEARING. Each payload names its own device; the type switch below is what reads it.
	case EventDeviceAttached, EventDeviceDetached, EventDeviceUpdated,
		EventJobUpdated, EventJobLog, EventOpUpdated,
		EventVersionCreated, EventVersionDeleted, EventMessagesIndexing:
		return udidOf(env.Data), true
	}
	// AN UNCLASSIFIED EVENT IS TREATED AS DEVICE-SCOPED WITH NO DEVICE, so it reaches nobody but the
	// admin. Unreachable while the gate passes; it is the fail-CLOSED answer for the window between
	// somebody adding a constant and the gate telling them.
	return "", true
}

// udidOf digs the device out of a payload without the wire package needing to know every producer.
//
// AN INTERFACE RATHER THAN A TYPE SWITCH OVER EVERY STRUCT, because the payloads live in several
// packages and a switch here would make `wire` import them — a cycle, and a list to keep in step
// with a second list. Anything that knows its device says so.
func udidOf(data any) string {
	if d, ok := data.(interface{ DeviceUDID() string }); ok {
		return d.DeviceUDID()
	}
	return ""
}
