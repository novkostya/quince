package muxd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"howett.net/plist"

	"github.com/novkostya/quince/core/internal/muxaddr"
)

// PAIRING RECORDS LIVE WITH THE MUXER, SO ONLY THE MUXER CAN ANSWER WHETHER ONE WAS WRITTEN
// (qn.6r D3). `userpref_read/save/delete_pair_record` are each a message to the muxer with no
// filesystem fallback, so a probe of quince's own filesystem decides nothing.
//
// THIS ASKS; IT NEVER WRITES. `SavePairRecord` is the only message that would answer *can a record
// be written*, and it overwrites unconditionally — destructive against a real UDID, and against a
// sentinel it leaves a file netmuxd cannot delete (it models no `DeletePairRecord`) which the
// muxer then caches as a phantom paired device. That is why there is no pre-check and why this
// file exposes a read only.
//
// THE RECORD BODY IS REDUCED TO A HASH AND DROPPED (D4). It is private-key-grade material (design
// §6): the host identity and a device record together let the holder talk to the phone as a
// trusted host. Nothing here logs, serves, persists or parses the body, and its length is not
// reported either — a size is a fact about a secret.

// PairRecordState is what the muxer said about one device's record.
type PairRecordState int

const (
	// PairRecordUnknown means quince could not ask — the dial failed, the re-dial failed, or the
	// deadline expired. It is NOT "no record": the remedies differ and merging them is what
	// *Troubleshooting is ACTIONABLE* forbids.
	PairRecordUnknown PairRecordState = iota
	// PairRecordAbsent means the muxer answered that it holds no record for this device.
	PairRecordAbsent
	// PairRecordPresent means the muxer returned a record; Digest identifies it.
	PairRecordPresent
)

// PairRecord is one answer from the muxer. Digest is set only when State is PairRecordPresent,
// and is a SHA-256 of the reply body — never the body itself.
type PairRecord struct {
	State  PairRecordState
	Digest [sha256.Size]byte
}

// Recorded reports whether a pair between the two observations wrote a record. It is deliberately
// a method on the BEFORE value: the question is *did this change*, not *is something there*.
//
// PRESENCE IS NOT THE QUESTION, AND THAT IS THE WHOLE POINT. A device whose record is stale — the
// phone was reset, or trust was revoked — still has a file in the store, and `contracts.md`'s
// `paired` is a lockdown validation rather than record presence, so quince offers Pair for exactly
// that device. A presence check then finds the OLD record and reports the pairing recorded.
//
// Unknown on either side yields false: quince does not know, and must say that rather than claim
// a pairing it cannot see.
func (before PairRecord) Recorded(after PairRecord) bool {
	if before.State == PairRecordUnknown || after.State == PairRecordUnknown {
		return false
	}
	if after.State == PairRecordAbsent {
		return false
	}
	if before.State == PairRecordAbsent {
		return true // absent → present
	}
	return before.Digest != after.Digest // present → a different record
}

// readPairRecordRequest is the plist body of a ReadPairRecord message.
type readPairRecordRequest struct {
	MessageType         string `plist:"MessageType"`
	PairRecordID        string `plist:"PairRecordID"`
	ClientVersionString string `plist:"ClientVersionString"`
	ProgName            string `plist:"ProgName"`
	LibUSBMuxVersion    uint32 `plist:"kLibUSBMuxVersion"`
}

// pairRecordReply decodes either answer the muxers give. usbmuxd returns a Result with a non-zero
// Number when it holds no record; netmuxd returns the record under PairRecordData, and answers
// "no record" by closing the connection without writing anything at all.
type pairRecordReply struct {
	MessageType    string `plist:"MessageType"`
	Number         int    `plist:"Number"`
	PairRecordData []byte `plist:"PairRecordData"`
}

// usbmuxdNoRecord is what usbmuxd answers when it holds no record for the requested udid:
// `send_pair_record` calls `send_result(client, tag, ENOENT)` (usbmuxd `src/client.c`), and
// ENOENT is 2. A MISSING `PairRecordID` gets EINVAL from the same function, and every other
// non-zero number is some other failure — so "non-zero means absent" would read a malformed
// request, or a code nobody has seen, as a confident *no record*. Every one of those must be
// PairRecordUnknown, because a false Absent is the one direction that bypasses Recorded()'s
// comparison rather than tripping it: the absent→present arm never reaches the digest.
const usbmuxdNoRecord = 2

// ReadPairRecord asks one muxer whether it holds a pairing record for udid.
//
// THREE MEANINGS, FOUR SHAPES ON THE WIRE (D5), and the silent arm is why this is not a plain
// error check. netmuxd answers *no record* by closing with no reply — but a muxer that DIES
// mid-request closes the same way, and one that is RESTARTED leaves a new process accepting on
// the same socket path.
//
// SO THE SECOND ASK IS A RE-ASK, NOT A PING. A bare dial observes a socket accepting; it cannot
// observe a silence, and it cannot tell the original process from its replacement. `muxsup`
// restarting the daemon is not a race — it is the supervised path, and the store is a directory
// that survives the restart, so a ping would report Absent with full confidence about a record
// that is still there (quince#1336 review).
//
// A SHORT-LIVED CONNECTION OF ITS OWN, not the Listen stream — that is a long-lived subscription
// whose framing carries no request/response discipline.
func ReadPairRecord(ctx context.Context, ep muxaddr.Endpoint, udid string, timeout time.Duration) PairRecord {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rec, err := askPairRecord(ctx, ep, udid)
	if err == nil {
		return rec
	}
	if !errors.Is(err, errNoReply) {
		return PairRecord{State: PairRecordUnknown}
	}

	// Silent once. Ask the whole question again rather than checking the socket is up.
	rec, err = askPairRecord(ctx, ep, udid)
	switch {
	case err == nil:
		return rec // it answered this time — take the answer over the silence
	case errors.Is(err, errNoReply):
		return PairRecord{State: PairRecordAbsent} // silent twice, and reachable twice
	default:
		return PairRecord{State: PairRecordUnknown}
	}
}

// errNoReply is the muxer accepting a request and closing before writing ANY byte of a frame.
// A close part-way through one is not this: it is the muxer dying while answering, which is the
// strongest available evidence that it WAS answering.
var errNoReply = errors.New("muxd: muxer closed without replying")

// readFrame reads one framed message, distinguishing a clean close before any byte from a
// truncated frame. `readPlist` cannot: `binary.Read` and `io.ReadFull` each yield io.EOF or
// io.ErrUnexpectedEOF, so three distinct wire events arrive as one and two of them are deaths.
func readFrame(conn net.Conn) ([]byte, error) {
	var raw [16]byte
	n, err := io.ReadFull(conn, raw[:])
	if err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return nil, errNoReply
		}
		return nil, fmt.Errorf("muxd: truncated pair-record reply header after %d byte(s): %w", n, err)
	}
	var h header
	if err := binary.Read(bytes.NewReader(raw[:]), binary.LittleEndian, &h); err != nil {
		return nil, err
	}
	if h.Length < 16 || h.Length > 16+maxPayload {
		return nil, fmt.Errorf("muxd: implausible pair-record reply length %d", h.Length)
	}
	body := make([]byte, h.Length-16)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, fmt.Errorf("muxd: truncated pair-record reply body: %w", err)
	}
	return body, nil
}

func dialEndpoint(ctx context.Context, ep muxaddr.Endpoint) (net.Conn, error) {
	network, address := ep.DialArgs()
	d := net.Dialer{Timeout: dialTimeout}
	return d.DialContext(ctx, network, address)
}

func askPairRecord(ctx context.Context, ep muxaddr.Endpoint, udid string) (PairRecord, error) {
	conn, err := dialEndpoint(ctx, ep)
	if err != nil {
		return PairRecord{}, err
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	req := readPairRecordRequest{
		MessageType:         "ReadPairRecord",
		PairRecordID:        udid,
		ClientVersionString: "quince",
		ProgName:            "quince",
		LibUSBMuxVersion:    3,
	}
	if err := writePlist(conn, 1, req); err != nil {
		return PairRecord{}, err
	}

	body, err := readFrame(conn)
	if err != nil {
		return PairRecord{}, err
	}

	var msg pairRecordReply
	if _, uerr := plist.Unmarshal(body, &msg); uerr != nil {
		return PairRecord{}, fmt.Errorf("muxd: decode ReadPairRecord reply: %w", uerr)
	}
	if len(msg.PairRecordData) > 0 {
		// THE ONLY PLACE THE BODY IS TOUCHED. Hashed here and never returned, logged or measured.
		return PairRecord{State: PairRecordPresent, Digest: sha256.Sum256(msg.PairRecordData)}, nil
	}
	if msg.Number == usbmuxdNoRecord {
		return PairRecord{State: PairRecordAbsent}, nil
	}
	return PairRecord{}, fmt.Errorf("muxd: ReadPairRecord answered result %d, which is not a record and not a no-record", msg.Number)
}
