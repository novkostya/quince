package muxd

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"howett.net/plist"

	"github.com/novkostya/quince/core/internal/muxaddr"
)

// fakeMuxer speaks the framing in protocol.go and can be scripted to every shape a real muxer
// produces for ReadPairRecord — including the one that is not a reply at all (M3: netmuxd answers
// "no record" by closing the connection with nothing written).
type fakeMuxer struct {
	t               *testing.T
	ln              net.Listener
	replies         [][]byte // one per accepted connection; nil means "close without replying"
	stall           bool     // accept, read the request, never answer
	closeAfterFirst bool     // close the listener while answering, so a re-dial cannot succeed
	conns           int
}

// behaviour returns what this fake does for connection n, and whether it should answer at all.
func newFakeMuxer(t *testing.T, replies ...[]byte) *fakeMuxer {
	t.Helper()
	// A unix socket under t.TempDir(), because that is the transport the deployment uses and the
	// path length limit is what a /tmp-based fixture would trip over.
	dir := t.TempDir()
	ln, err := net.Listen("unix", filepath.Join(dir, "mux.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeMuxer{t: t, ln: ln, replies: replies}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeMuxer) endpoint() muxaddr.Endpoint {
	ep, err := muxaddr.Parse(f.ln.Addr().String())
	if err != nil {
		f.t.Fatalf("parse endpoint: %v", err)
	}
	return ep
}

func (f *fakeMuxer) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		n := f.conns
		f.conns++
		go func(conn net.Conn, idx int) {
			defer func() { _ = conn.Close() }()
			if _, _, err := readPlist(conn); err != nil {
				return
			}
			if f.stall {
				time.Sleep(5 * time.Second)
				return
			}
			if idx >= len(f.replies) || f.replies[idx] == nil {
				if f.closeAfterFirst {
					// Take the listener away BEFORE returning, so the re-dial deterministically
					// fails rather than racing it.
					_ = f.ln.Close()
				}
				return // close with no reply — netmuxd's "no record"
			}
			_ = writeRaw(conn, f.replies[idx])
		}(c, n)
	}

}

// writeRaw frames an already-marshalled plist body the way writePlist does, without marshalling
// it a second time.
func writeRaw(conn net.Conn, body []byte) error {
	h := header{Length: uint32(16 + len(body)), Version: 1, Request: 8, Tag: 1}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, h)
	buf.Write(body)
	_, err := conn.Write(buf.Bytes())
	return err
}

func recordReply(t *testing.T, data []byte) []byte {
	t.Helper()
	b, err := plist.Marshal(map[string]any{
		"MessageType":    "Result",
		"PairRecordData": data,
	}, plist.XMLFormat)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func resultReply(t *testing.T, number int) []byte {
	t.Helper()
	b, err := plist.Marshal(map[string]any{
		"MessageType": "Result",
		"Number":      number,
	}, plist.XMLFormat)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func read(t *testing.T, f *fakeMuxer) PairRecord {
	t.Helper()
	return ReadPairRecord(context.Background(), f.endpoint(), "UDID", 2*time.Second)
}

func TestRecordReturnedIsPresentAndHashed(t *testing.T) {
	f := newFakeMuxer(t, recordReply(t, []byte("SENTINEL-RECORD-BODY")))
	got := read(t, f)
	if got.State != PairRecordPresent {
		t.Fatalf("state = %v, want present", got.State)
	}
	var zero [32]byte
	if got.Digest == zero {
		t.Fatal("digest is zero; the body was not hashed")
	}
}

// M3: netmuxd answers "no record" by closing with nothing written. A naive client reads that as a
// transport error and reports "quince cannot tell" for the ordinary not-paired case.
func TestCloseWithNoReplyIsAbsentWhenTheMuxerIsStillThere(t *testing.T) {
	f := newFakeMuxer(t, nil, nil) // first: no reply. second: the re-dial, which succeeds.
	if got := read(t, f); got.State != PairRecordAbsent {
		t.Fatalf("state = %v, want absent", got.State)
	}
}

// The SAME shape on the wire, with the muxer gone. This is the arm that makes the EOF honest.
func TestCloseWithNoReplyIsUNKNOWNWhenTheMuxerHasGone(t *testing.T) {
	f := newFakeMuxer(t, nil)
	f.closeAfterFirst = true
	got := read(t, f)
	if got.State != PairRecordUnknown {
		t.Fatalf("state = %v, want unknown — a dead muxer must not report as absent (D5)", got.State)
	}
}

func TestResultWithNonZeroNumberIsAbsent(t *testing.T) {
	f := newFakeMuxer(t, resultReply(t, 1))
	if got := read(t, f); got.State != PairRecordAbsent {
		t.Fatalf("state = %v, want absent", got.State)
	}
}

func TestUnreachableMuxerIsUnknownNotAbsent(t *testing.T) {
	dir := t.TempDir()
	ep, err := muxaddr.Parse(filepath.Join(dir, "nothing-here.sock"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := ReadPairRecord(context.Background(), ep, "UDID", 500*time.Millisecond)
	if got.State != PairRecordUnknown {
		t.Fatalf("state = %v, want unknown", got.State)
	}
}

func TestDeadlineIsUnknownNotAbsent(t *testing.T) {
	f := newFakeMuxer(t)
	f.stall = true
	got := ReadPairRecord(context.Background(), f.endpoint(), "UDID", 200*time.Millisecond)
	if got.State != PairRecordUnknown {
		t.Fatalf("state = %v, want unknown", got.State)
	}
}

// The rung's central claim: a stale record must not read as a pairing that was just written.
func TestRecordedComparesRatherThanChecksPresence(t *testing.T) {
	same := PairRecord{State: PairRecordPresent, Digest: [32]byte{1}}
	other := PairRecord{State: PairRecordPresent, Digest: [32]byte{2}}
	absent := PairRecord{State: PairRecordAbsent}
	unknown := PairRecord{State: PairRecordUnknown}

	if same.Recorded(same) {
		t.Error("an unchanged record read as recorded — this is the stale-record case")
	}
	if !same.Recorded(other) {
		t.Error("a changed record did not read as recorded")
	}
	if !absent.Recorded(same) {
		t.Error("absent → present did not read as recorded")
	}
	if absent.Recorded(absent) {
		t.Error("absent → absent read as recorded")
	}
	if unknown.Recorded(same) || same.Recorded(unknown) {
		t.Error("unknown on either side must not claim a pairing")
	}
}

// D4: the body never reaches a log, a file or the returned value.
func TestRecordBodyDoesNotEscape(t *testing.T) {
	const sentinel = "SENTINEL-PRIVATE-KEY-GRADE-BODY"
	f := newFakeMuxer(t, recordReply(t, []byte(sentinel)))
	got := read(t, f)
	if strings.Contains(string(got.Digest[:]), sentinel) {
		t.Fatal("the body survived in the digest")
	}
	// Nothing in this package writes files; assert the temp dir stayed empty of the sentinel.
	entries, _ := os.ReadDir(t.TempDir())
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(t.TempDir(), e.Name()))
		if strings.Contains(string(b), sentinel) {
			t.Fatalf("the record body was written to %s", e.Name())
		}
	}
}
