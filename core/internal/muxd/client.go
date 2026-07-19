package muxd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"howett.net/plist"
)

// Transport names (quince's per-transport presence keys, contracts §2). The muxer's
// ConnectionType is "USB" or "Network"; everything non-USB is Wi-Fi for our purposes.
const (
	TransportUSB  = "usb"
	TransportWiFi = "wifi"
)

// EventKind distinguishes an attach edge from a detach edge.
type EventKind int

const (
	Attached EventKind = iota
	Detached
)

// Event is one presence edge, resolved from the muxer to a UDID + transport (design §2/§3).
// The registry (next increment) folds a stream of these into the device table.
type Event struct {
	Kind      EventKind
	UDID      string
	Transport string
}

func mapTransport(connType string) string {
	if strings.EqualFold(connType, "USB") {
		return TransportUSB
	}
	return TransportWiFi // "Network" (netmuxd mDNS / usbmuxd) → wifi
}

// Sink receives one muxer connection's presence lifecycle. On each successful (re)connect
// the client calls Reset() — the consumer (the device registry) drops this source's edges so
// a device that detached while we were disconnected doesn't linger as a phantom — then
// Apply() for every edge the muxer replays and, thereafter, each live edge.
type Sink interface {
	Reset()
	Apply(ev Event)
}

// listen performs the Listen handshake on conn, then reads attach/detach messages until the
// connection errors, resolving each to an Event. Detached carries ONLY a DeviceID (a
// per-connection integer, reassigned across reconnects — stack D2 / qn.2 spec), so a
// connection-local DeviceID→Event map resolves it back to a UDID+transport; the map lives
// and dies with this one connection. Undecodable or unknown messages are logged and skipped,
// never fatal (design §2: unknown lines are logged, never fatal).
func listen(ctx context.Context, conn io.ReadWriter, log *slog.Logger, emit func(Event)) error {
	req := listenRequest{
		MessageType:         "Listen",
		ClientVersionString: "quince",
		ProgName:            "quince",
		LibUSBMuxVersion:    3,
	}
	if err := writePlist(conn, 1, req); err != nil {
		return err
	}

	attached := map[int]Event{} // DeviceID → the Attached event, for Detached resolution
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		body, _, err := readPlist(conn)
		if err != nil {
			return err
		}
		var msg reply
		if _, uerr := plist.Unmarshal(body, &msg); uerr != nil {
			log.Warn("muxd: undecodable message, skipping", "error", uerr)
			continue
		}
		switch msg.MessageType {
		case "Result":
			if msg.Number != 0 {
				return fmt.Errorf("muxd: Listen refused (result %d)", msg.Number)
			}
		case "Attached":
			if msg.Properties.SerialNumber == "" {
				log.Warn("muxd: Attached without SerialNumber, skipping", "device_id", msg.DeviceID)
				continue
			}
			ev := Event{Kind: Attached, UDID: msg.Properties.SerialNumber, Transport: mapTransport(msg.Properties.ConnectionType)}
			attached[msg.DeviceID] = ev
			emit(ev)
		case "Detached":
			if ev, ok := attached[msg.DeviceID]; ok {
				delete(attached, msg.DeviceID)
				emit(Event{Kind: Detached, UDID: ev.UDID, Transport: ev.Transport})
			}
		default:
			log.Warn("muxd: unknown message type, skipping", "type", msg.MessageType)
		}
	}
}

// Client maintains a Listen connection to one muxer socket, reconnecting with capped
// exponential backoff. addr is a Unix socket path (usbmuxd) or a host:port (netmuxd TCP);
// the form selects the dial network.
type Client struct {
	addr string
	log  *slog.Logger
}

// NewClient returns a Client for the given muxer address.
func NewClient(addr string, log *slog.Logger) *Client { return &Client{addr: addr, log: log} }

const (
	dialTimeout    = 5 * time.Second
	backoffInitial = 500 * time.Millisecond
	backoffMax     = 30 * time.Second
)

// Run dials and Listens in a loop until ctx is cancelled, feeding presence edges to sink.
// Each reconnect starts a fresh listen (fresh per-connection DeviceID map) and begins with
// sink.Reset() BEFORE the muxer's replay, so the registry can drop this source's stale edges
// and let the replay re-add only what's still attached — a device that vanished while we were
// disconnected is thereby cleared, not left as a phantom (qn.2 spec). A no-flicker variant
// (buffer the replay burst into an atomic snapshot) is a documented future refinement.
func (c *Client) Run(ctx context.Context, sink Sink) {
	delay := backoffInitial
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := c.dial(ctx)
		if err != nil {
			c.log.Warn("muxd: dial failed", "addr", c.addr, "error", err)
			if !sleep(ctx, delay) {
				return
			}
			delay = nextBackoff(delay)
			continue
		}
		delay = backoffInitial // a successful connection resets the backoff
		sink.Reset()           // (re)connect: drop this source's edges; the replay re-adds live ones
		err = listen(ctx, conn, c.log, sink.Apply)
		_ = conn.Close()
		if ctx.Err() != nil {
			return
		}
		c.log.Warn("muxd: listen ended, reconnecting", "addr", c.addr, "error", err)
		if !sleep(ctx, delay) {
			return
		}
		delay = nextBackoff(delay)
	}
}

// dial connects to the muxer: a leading "/" (or no ":") means a Unix socket path, otherwise
// a TCP host:port.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	network := "tcp"
	if strings.HasPrefix(c.addr, "/") {
		network = "unix"
	}
	d := net.Dialer{Timeout: dialTimeout}
	return d.DialContext(ctx, network, c.addr)
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > backoffMax {
		return backoffMax
	}
	return d
}

// sleep waits for d or ctx cancellation; it reports false if ctx was cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
