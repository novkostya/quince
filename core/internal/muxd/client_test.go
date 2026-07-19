package muxd

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// recordingSink is a muxd.Sink that records the ordered Reset/Apply calls a Client makes, so a
// test can assert the reconnect contract (Reset before the muxer's replay, then re-apply only
// what the muxer re-sends). The registry's own reconcile-on-Reset is proven separately in
// package device (TestResetReconcileClearsPhantom); here we prove the CLIENT drives that path.
type recordingSink struct {
	mu  sync.Mutex
	log []string
}

func (s *recordingSink) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, "reset")
}

func (s *recordingSink) Apply(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := "attach"
	if ev.Kind == Detached {
		kind = "detach"
	}
	s.log = append(s.log, fmt.Sprintf("%s:%s:%s", kind, ev.UDID, ev.Transport))
}

func (s *recordingSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

func countResets(log []string) int {
	n := 0
	for _, e := range log {
		if e == "reset" {
			n++
		}
	}
	return n
}

// listenerFor returns a listening socket + the address form a Client would dial: a filesystem
// path (→ unix dial) or a host:port (→ tcp dial), covering both dial() branches.
func listenerFor(t *testing.T, network string) (net.Listener, string) {
	t.Helper()
	if network == "unix" {
		p := filepath.Join(t.TempDir(), "m.sock")
		ln, err := net.Listen("unix", p)
		if err != nil {
			t.Fatalf("listen unix: %v", err)
		}
		return ln, p
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	return ln, ln.Addr().String()
}

// serveScripts runs one scripted connection per element of scripts: accept, answer the client's
// Listen with Result(0), run the script (which writes Attached/Detached frames), briefly let the
// client drain, then close the connection to force a reconnect. After the last script it closes
// the listener so further dials fail fast and the Client harmlessly backoff-loops until ctx
// cancel. Errors return silently (no t.Fatal from this goroutine — it may outlive the test body).
func serveScripts(t *testing.T, ln net.Listener, scripts []func(net.Conn)) {
	t.Helper()
	go func() {
		for _, script := range scripts {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if _, _, err := readPlist(c); err != nil { // the client's Listen request
				_ = c.Close()
				return
			}
			if err := writePlist(c, 1, map[string]any{"MessageType": "Result", "Number": 0}); err != nil {
				_ = c.Close()
				return
			}
			script(c)
			time.Sleep(50 * time.Millisecond) // let the client drain the writes before close
			_ = c.Close()
		}
		_ = ln.Close()
	}()
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// TestClientRunReconnectResetsAndReplays exercises the real Client.Run loop (dial + handshake +
// listen + capped-backoff reconnect) against a fake muxer socket that drops the connection and,
// on re-Listen, replays a REDUCED attached set. It proves the client's half of story 3: on each
// (re)connect it calls Reset() BEFORE the replay, then re-applies only what the muxer re-sends —
// so a device that detached while we were disconnected is not re-added (the registry, given this
// Reset+reduced-replay, clears the phantom — proven in package device). Runs over both a unix
// socket and a tcp address to cover both dial() network branches.
func TestClientRunReconnectResetsAndReplays(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			ln, addr := listenerFor(t, network)
			serveScripts(t, ln, []func(net.Conn){
				func(c net.Conn) { // first connect: A and B both attached on USB
					_ = writePlist(c, 0, attachedMsg(1, udidA, "USB"))
					_ = writePlist(c, 0, attachedMsg(2, udidB, "USB"))
				},
				func(c net.Conn) { // reconnect replay: only A returns (B vanished while away)
					_ = writePlist(c, 0, attachedMsg(1, udidA, "USB"))
				},
			})

			sink := &recordingSink{}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				NewClient(addr, testLog()).Run(ctx, sink)
			}()

			// Wait until the second connect's Reset + replay have landed.
			waitFor(t, func() bool {
				log := sink.snapshot()
				return countResets(log) >= 2 && log[len(log)-1] == "attach:"+udidA+":"+TransportUSB
			}, 5*time.Second, "reconnect Reset + A replay")

			cancel()
			<-done // clean shutdown: Run returns from the backoff sleep on ctx cancel

			log := sink.snapshot()
			if n := countResets(log); n != 2 {
				t.Fatalf("reset count = %d (want exactly 2: initial connect + one reconnect); log=%v", n, log)
			}
			// Split the log at the second reset: before = first session, after = the replay.
			second := -1
			seen := 0
			for i, e := range log {
				if e == "reset" {
					seen++
					if seen == 2 {
						second = i
						break
					}
				}
			}
			first, replay := log[:second], log[second+1:]
			if !contains(first, "attach:"+udidA+":"+TransportUSB) || !contains(first, "attach:"+udidB+":"+TransportUSB) {
				t.Fatalf("first session missing A and/or B: %v", first)
			}
			if len(replay) != 1 || replay[0] != "attach:"+udidA+":"+TransportUSB {
				t.Fatalf("reconnect replay = %v (want only A re-applied — B must NOT reappear)", replay)
			}
		})
	}
}

func contains(s []string, want string) bool {
	for _, e := range s {
		if e == want {
			return true
		}
	}
	return false
}

// TestReadPlistRejectsImplausibleLength guards the pre-allocation length check: a corrupt header
// claiming a body larger than maxPayload (or smaller than the header itself) is rejected before
// any make([]byte, …), so a bad frame can't drive a huge allocation.
func TestReadPlistRejectsImplausibleLength(t *testing.T) {
	var tooBig bytes.Buffer
	_ = binary.Write(&tooBig, binary.LittleEndian, header{Length: 16 + maxPayload + 1, Version: protocolVersion, Request: messagePlist})
	if _, _, err := readPlist(&tooBig); err == nil {
		t.Fatal("readPlist accepted an implausibly large length")
	}

	var tooSmall bytes.Buffer
	_ = binary.Write(&tooSmall, binary.LittleEndian, header{Length: 8, Version: protocolVersion, Request: messagePlist})
	if _, _, err := readPlist(&tooSmall); err == nil {
		t.Fatal("readPlist accepted a sub-header length")
	}
}

// TestReadPlistTruncatedBody: a header promising more body than the stream delivers must error
// (io.ReadFull), not return a short body — a length-prefixed stream can't be resynced, so this
// ends the connection and the Client reconnects (story 5: truncated frames).
func TestReadPlistTruncatedBody(t *testing.T) {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, header{Length: 16 + 100, Version: protocolVersion, Request: messagePlist})
	buf.Write([]byte("only ten..")) // 10 bytes; the header promised 100
	if _, _, err := readPlist(&buf); err == nil {
		t.Fatal("readPlist accepted a truncated body")
	}
}

// TestListenResultRefused: a non-zero Listen Result terminates listen() with an error (which the
// Client turns into a reconnect), rather than proceeding to read attach/detach messages.
func TestListenResultRefused(t *testing.T) {
	cli, mux := net.Pipe()
	go func() {
		defer func() { _ = mux.Close() }()
		if _, _, err := readPlist(mux); err != nil { // the client's Listen request
			return
		}
		_ = writePlist(mux, 1, map[string]any{"MessageType": "Result", "Number": 1}) // refused
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := listen(ctx, cli, testLog(), func(Event) { t.Error("no event expected on a refused Listen") })
	_ = cli.Close()
	if err == nil {
		t.Fatal("listen returned nil on a refused Result")
	}
}

// TestListenSkipsUnknownType: an unknown MessageType is logged and skipped (story 5), and valid
// messages before and after it still land.
func TestListenSkipsUnknownType(t *testing.T) {
	events := runListen(t, func(mux net.Conn) {
		_ = writePlist(mux, 0, attachedMsg(1, udidA, "USB"))
		_ = writePlist(mux, 0, map[string]any{"MessageType": "Paired", "DeviceID": 9}) // unknown → skip
		_ = writePlist(mux, 0, attachedMsg(2, udidB, "Network"))
	})
	if a := recvEvent(t, events); a.UDID != udidA {
		t.Fatalf("first event = %+v", a)
	}
	if b := recvEvent(t, events); b.UDID != udidB || b.Transport != TransportWiFi {
		t.Fatalf("event after unknown type = %+v (unknown type not tolerated?)", b)
	}
}

// TestListenSkipsAttachedWithoutSerial: an Attached carrying no SerialNumber (no resolvable UDID)
// is skipped, never emitted as a phantom device; a valid Attached still lands.
func TestListenSkipsAttachedWithoutSerial(t *testing.T) {
	events := runListen(t, func(mux net.Conn) {
		_ = writePlist(mux, 0, map[string]any{
			"MessageType": "Attached", "DeviceID": 1,
			"Properties": map[string]any{"ConnectionType": "USB"}, // no SerialNumber
		})
		_ = writePlist(mux, 0, attachedMsg(2, udidA, "USB"))
	})
	if a := recvEvent(t, events); a.UDID != udidA {
		t.Fatalf("serial-less Attached not skipped; got %+v", a)
	}
}
