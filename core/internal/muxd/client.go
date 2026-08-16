package muxd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"howett.net/plist"

	"github.com/novkostya/quince/core/internal/muxaddr"
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

// knownIgnoredTypes are listen-stream messages quince has a model for and deliberately does not
// act on. They are the middle of three cases, and the middle one is what makes the other two mean
// something: a WARN on this stream is now the claim *the muxer said a word this code has never
// heard*, which is worth a look on the managed-muxer profile. Before this list, `Paired` — which
// usbmuxd emits on every pairing — produced two WARNs during first-run onboarding, in the log the
// new user is most likely reading (quince#934).
//
// MEASURED at the pinned muxers rather than remembered, because it is a claim about another
// program (hard rule: interface facts are looked up live):
//
//   - usbmuxd 1.1.1_git20250201-r11, the Alpine 3.24 community package deploy/Dockerfile installs:
//     strings on the shipped binary gives exactly Attached, Detached, Paired, Result — and
//     src/client.c constructs the four in send_result, create_device_attached_plist,
//     send_device_remove and send_device_paired. There is no fifth.
//   - netmuxd v0.4.3 (NETMUXD_REF), which quince Listens to for Wi-Fi: Attached and Detached only.
//     It never sends Paired.
//
// So the set is closed at one entry today. Adding to it is the right move for a type a muxer
// documents and quince has nothing to do with; a type nobody can find in a muxer's source belongs
// in the WARN arm, where it will be noticed.
var knownIgnoredTypes = map[string]bool{
	// usbmuxd announces a successful pairing. quince carries no pairing state on this stream —
	// pairing is observed through lockdown, and the Attached that follows is what it acts on.
	"Paired": true,
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
			if knownIgnoredTypes[msg.MessageType] {
				log.Debug("muxd: known message type, nothing to do", "type", msg.MessageType)
				continue
			}
			log.Warn("muxd: unknown message type, skipping", "type", msg.MessageType)
		}
	}
}

// Client maintains a Listen connection to one muxer endpoint, reconnecting with capped
// exponential backoff.
type Client struct {
	ep  muxaddr.Endpoint
	log *slog.Logger

	// mu guards the health fields and the live connection, which Run writes and the HTTP
	// goroutine reads (Health) or closes (Reread). `go test -race` is what keeps this honest.
	mu        sync.Mutex
	connected bool
	detail    string   // why not connected: the dial or listen error, in the muxer's own words
	conn      net.Conn // the live Listen connection, so Reread can drop it from outside Run
}

// Reread drops the current connection so Run redials and the muxer replays its whole attached
// set (qn.6p D6). It is what POST /api/devices/rescan does now that quince supervises nothing.
//
// IT RESTARTS NOTHING, and the name says so. The old rescan restarted the managed usbmuxd to pick
// up devices an unprivileged container's absent hotplug never delivered. That problem did not go
// away — it MOVED to the muxer's own container, which quince cannot restart. What quince can still
// do is ask the muxer again: closing the connection makes Run reconnect, and a reconnect calls
// sink.Reset() BEFORE the replay, so the registry drops this source's edges and re-adds only what
// the muxer still reports. That is a real reconcile against the muxer's current truth, and it is
// all this may honestly claim.
//
// Safe when not connected: there is nothing to close and Run is already redialling.
func (c *Client) Reread() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		// Closing it under Run's feet IS the mechanism: the blocked read in listen() returns an
		// error, Run treats it as any other dropped connection, and the backoff is already at its
		// minimum because this connection had succeeded.
		_ = conn.Close()
	}
}

// setConn records the live connection for Reread, or clears it. Called only from Run.
func (c *Client) setConn(conn net.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = conn
}

// Health reports whether THIS client currently holds a Listen connection, and if not, what the
// last attempt said.
//
// It exists so /api/health can report an external muxer from the connection quince ACTUALLY
// DEPENDS ON, rather than from a second prober beside it (qn.6p D5). A prober is the obvious
// design and it is the wrong one: it can dial successfully while this client sits in a 30 s
// backoff after a protocol-level failure, so health would read `external` — fine — while no
// device could appear. That is the same defect as the asserted status it replaces (quince#897
// item 2), moved rather than fixed.
func (c *Client) Health() (connected bool, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected, c.detail
}

// setHealth records the outcome of a dial or a listen. Called only from Run.
func (c *Client) setHealth(connected bool, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected, c.detail = connected, detail
}

// NewClient returns a Client for an ALREADY-PARSED endpoint. It takes a muxaddr.Endpoint rather
// than a string so the grammar is decided once, at startup, where a bad address can refuse the
// process — rather than per-dial, where it could only be logged about once a second forever
// (qn.6p D3). Which network to dial is the endpoint's answer, not this package's.
func NewClient(ep muxaddr.Endpoint, log *slog.Logger) *Client { return &Client{ep: ep, log: log} }

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
			c.setHealth(false, err.Error())
			c.log.Warn("muxd: dial failed", "addr", c.ep, "error", err)
			if !sleep(ctx, delay) {
				return
			}
			delay = nextBackoff(delay)
			continue
		}
		c.setHealth(true, "")
		c.setConn(conn)        // published so Reread (rescan) can drop it from another goroutine
		delay = backoffInitial // a successful connection resets the backoff
		sink.Reset()           // (re)connect: drop this source's edges; the replay re-adds live ones
		err = listen(ctx, conn, c.log, sink.Apply)
		c.setConn(nil)
		_ = conn.Close()
		// The connection is gone whether or not ctx is done, so health says so BEFORE the
		// shutdown check — otherwise a cancelled serve leaves health claiming a live connection.
		c.setHealth(false, errText(err))
		if ctx.Err() != nil {
			return
		}
		c.log.Warn("muxd: listen ended, reconnecting", "addr", c.ep, "error", err)
		if !sleep(ctx, delay) {
			return
		}
		delay = nextBackoff(delay)
	}
}

// dial connects to the muxer. The network/address split is the endpoint's, decided once at
// parse time — this used to re-derive it from a leading "/", which is the half of quince#897
// item 1 that made `UNIX:/run/mux/usbmuxd` dial TCP.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	network, address := c.ep.DialArgs()
	d := net.Dialer{Timeout: dialTimeout}
	return d.DialContext(ctx, network, address)
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

// errText renders an error for health detail, tolerating nil (listen returns nil only if it ever
// stops without error; treat that as a plain disconnect rather than printing "<nil>").
func errText(err error) string {
	if err == nil {
		return "connection closed"
	}
	return err.Error()
}
