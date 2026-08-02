package tlsx

import (
	"bufio"
	"net"
	"sync"
	"time"
)

// tlsHandshakeByte is the first byte of every TLS record of type `handshake` (RFC 8446 §5.1,
// ContentType.handshake = 22 = 0x16). Every HTTP/1.x request begins with an ASCII method
// letter and HTTP/2's preface begins with `P`, so one byte discriminates the two protocols
// with no ambiguity in the range that matters.
const tlsHandshakeByte = 0x16

// peekTimeout bounds how long a connection may sit having sent nothing before we give up on
// classifying it. Without it, a client that opens a socket and stalls holds a goroutine and
// a file descriptor for as long as it likes — the cheapest denial there is against a
// protocol-sniffing listener, and the reason the sniff runs per-connection in its own
// goroutine rather than inline in Accept.
const peekTimeout = 10 * time.Second

// Mux splits one net.Listener into two: connections whose first byte is a TLS ClientHello go
// to TLS(), everything else to Plain(). Both are ordinary net.Listeners, so each can be
// handed to its own http.Server.
//
// ONE PORT IS THE RULING (gap A, Operator 2026-08-02, option (c)), and it is not about
// saving a port number. The onboarding URL never changes: open `http://host:PORT`, complete
// step 1, obtain a certificate, and the same URL keeps working — upgraded in place. There is
// no "now go to a different port", which is the worst moment in any self-hosted first run,
// and no saved bookmark that starts returning a TLS error, which browsers render as "sent an
// invalid response" and a user cannot tell apart from the app being broken.
//
// It also matters on the deployment that matters: Wi-Fi needs `network_mode: host` for mDNS,
// so there is no port forwarding and a second listener is a second host bind on a box where
// nothing can be remapped.
//
// VENDORED RATHER THAN `github.com/soheilhy/cmux`, measured at the ruling: cmux's newest
// published release is v0.1.5 from 2021-02-05, and its one later commit is untagged, so
// adopting it means pinning a pseudo-version to a dependency five years older than the
// decision — in exchange for a general matcher framework this needs none of.
type Mux struct {
	inner net.Listener

	tlsConns   chan net.Conn
	plainConns chan net.Conn

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewMux starts serving from inner. Close it through Close, never by closing inner directly.
func NewMux(inner net.Listener) *Mux {
	m := &Mux{
		inner:      inner,
		tlsConns:   make(chan net.Conn),
		plainConns: make(chan net.Conn),
		done:       make(chan struct{}),
	}
	m.wg.Add(1)
	go m.accept()
	return m
}

// TLS returns the listener yielding TLS connections. Wrap it in tls.NewListener.
func (m *Mux) TLS() net.Listener { return &side{mux: m, conns: m.tlsConns, addr: m.inner.Addr()} }

// Plain returns the listener yielding non-TLS connections.
func (m *Mux) Plain() net.Listener { return &side{mux: m, conns: m.plainConns, addr: m.inner.Addr()} }

// Close stops accepting and closes the underlying listener. Idempotent, and safe to call
// while both http.Servers are shutting down — which is the point, because two servers over
// one real listener would otherwise race to close it and the loser gets
// `use of closed network connection` as if something had gone wrong.
func (m *Mux) Close() error {
	var err error
	m.closeOnce.Do(func() {
		close(m.done)
		err = m.inner.Close()
	})
	m.wg.Wait()
	return err
}

func (m *Mux) accept() {
	defer m.wg.Done()
	for {
		c, err := m.inner.Accept()
		if err != nil {
			select {
			case <-m.done: // ordinary shutdown
			default:
			}
			return
		}
		m.wg.Add(1)
		// PER-CONNECTION GOROUTINE, not an inline peek. A client that connects and sends
		// nothing would otherwise block every other client behind it — the accept loop is
		// serial, so one silent socket is a complete outage rather than one stuck request.
		go m.classify(c)
	}
}

func (m *Mux) classify(c net.Conn) {
	defer m.wg.Done()

	if err := c.SetReadDeadline(time.Now().Add(peekTimeout)); err != nil {
		_ = c.Close()
		return
	}
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		_ = c.Close() // never sent a byte, or went away: nothing to route
		return
	}
	// CLEAR THE DEADLINE BEFORE HAND-OFF. http.Server sets its own read deadlines from
	// ReadTimeout; a deadline left over from the peek would be inherited and would kill
	// long-lived connections — the WebSocket at /api/ws first, ten seconds in, with no
	// error anybody could trace back to here.
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		_ = c.Close()
		return
	}

	wrapped := &replayConn{Conn: c, r: br}
	target := m.plainConns
	if first[0] == tlsHandshakeByte {
		target = m.tlsConns
	}
	select {
	case target <- wrapped:
	case <-m.done:
		_ = c.Close()
	}
}

// side is one half of the split, presented as a net.Listener.
type side struct {
	mux   *Mux
	conns chan net.Conn
	addr  net.Addr
}

func (s *side) Accept() (net.Conn, error) {
	select {
	case c := <-s.conns:
		return c, nil
	case <-s.mux.done:
		return nil, net.ErrClosed
	}
}

// Close closes THE WHOLE MUX, which is what http.Server.Shutdown expects of the listener it
// was given, and is safe because Mux.Close is idempotent. Closing only this side would leave
// the accept loop running and the port bound.
func (s *side) Close() error { return s.mux.Close() }

func (s *side) Addr() net.Addr { return s.addr }

// replayConn hands back the byte the classifier peeked.
//
// bufio.Reader has already consumed from the socket, so reading the raw net.Conn afterwards
// would skip whatever the reader buffered — the first byte at least, and in practice the
// whole request line, because bufio fills its buffer rather than reading one byte. Every
// read goes through the reader; everything else is the embedded Conn's.
type replayConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *replayConn) Read(p []byte) (int, error) { return c.r.Read(p) }
