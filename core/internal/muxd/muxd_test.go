package muxd

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

// Synthetic UDIDs only — a real muxer SerialNumber is the device UDID (personal data). See
// the qn.2 spec Fixtures note + the privacy commit gate.
const (
	udidA = "SYNTHETIC-UDID-AAAA-0001"
	udidB = "SYNTHETIC-UDID-BBBB-0002"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// syncBuffer is a bytes.Buffer safe to read while listen() is still writing to it from its own
// goroutine — the log tests below assert on records the client emits mid-stream, and -race
// otherwise reports the read against the write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// capturingLog logs at Debug so a test can assert on the level a record was emitted AT, which is
// the whole of quince#934: the same message at Warn and at Debug are different claims.
func capturingLog() (*slog.Logger, *syncBuffer) {
	var buf syncBuffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func attachedMsg(deviceID int, udid, connType string) map[string]any {
	return map[string]any{
		"MessageType": "Attached",
		"DeviceID":    deviceID,
		"Properties":  map[string]any{"SerialNumber": udid, "ConnectionType": connType},
	}
}

func detachedMsg(deviceID int) map[string]any {
	return map[string]any{"MessageType": "Detached", "DeviceID": deviceID}
}

// writeRawFrame writes a header + arbitrary (here: malformed) body, for the tolerance test.
func writeRawFrame(t *testing.T, w io.Writer, body []byte) {
	t.Helper()
	h := header{Length: uint32(16 + len(body)), Version: protocolVersion, Request: messagePlist, Tag: 0}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, h)
	buf.Write(body)
	if _, err := w.Write(buf.Bytes()); err != nil {
		t.Errorf("writeRawFrame: %v", err)
	}
}

// runListen stands up a fake muxer over net.Pipe (reply Result(0), then run script), drives
// listen() against it, and returns the channel of emitted events.
func runListen(t *testing.T, script func(mux net.Conn)) <-chan Event {
	t.Helper()
	return runListenWithLog(t, testLog(), script)
}

// runListenWithLog is runListen with the logger supplied, for the tests that assert on what the
// client logged and at which level.
func runListenWithLog(t *testing.T, log *slog.Logger, script func(mux net.Conn)) <-chan Event {
	t.Helper()
	cli, mux := net.Pipe()
	events := make(chan Event, 16)

	go func() {
		defer func() { _ = mux.Close() }()
		if _, _, err := readPlist(mux); err != nil { // the client's Listen request
			t.Errorf("fake mux: read Listen: %v", err)
			return
		}
		if err := writePlist(mux, 1, map[string]any{"MessageType": "Result", "Number": 0}); err != nil {
			t.Errorf("fake mux: write Result: %v", err)
			return
		}
		script(mux)
		time.Sleep(50 * time.Millisecond) // let the client drain before the pipe closes
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	go func() {
		defer cancel()
		defer func() { _ = cli.Close() }()
		_ = listen(ctx, cli, log, func(e Event) { events <- e })
	}()
	return events
}

func recvEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a muxd event")
		return Event{}
	}
}

func TestListenAttachDetachUSB(t *testing.T) {
	events := runListen(t, func(mux net.Conn) {
		_ = writePlist(mux, 0, attachedMsg(3, udidA, "USB"))
		_ = writePlist(mux, 0, detachedMsg(3)) // Detached carries only DeviceID → resolved via the map
	})
	if a := recvEvent(t, events); a.Kind != Attached || a.UDID != udidA || a.Transport != TransportUSB {
		t.Fatalf("attach event = %+v", a)
	}
	if d := recvEvent(t, events); d.Kind != Detached || d.UDID != udidA || d.Transport != TransportUSB {
		t.Fatalf("detach event = %+v", d)
	}
}

func TestListenNetworkMapsToWiFi(t *testing.T) {
	events := runListen(t, func(mux net.Conn) {
		_ = writePlist(mux, 0, attachedMsg(7, udidB, "Network"))
	})
	if a := recvEvent(t, events); a.Kind != Attached || a.UDID != udidB || a.Transport != TransportWiFi {
		t.Fatalf("wifi attach event = %+v", a)
	}
}

func TestListenSkipsMalformedFrame(t *testing.T) {
	events := runListen(t, func(mux net.Conn) {
		_ = writePlist(mux, 0, attachedMsg(3, udidA, "USB"))
		writeRawFrame(t, mux, []byte("not a plist at all")) // must be skipped, not fatal
		_ = writePlist(mux, 0, attachedMsg(4, udidB, "Network"))
	})
	if a := recvEvent(t, events); a.UDID != udidA {
		t.Fatalf("first event = %+v", a)
	}
	if b := recvEvent(t, events); b.UDID != udidB || b.Transport != TransportWiFi {
		t.Fatalf("event after malformed frame = %+v (malformed frame not tolerated?)", b)
	}
}
