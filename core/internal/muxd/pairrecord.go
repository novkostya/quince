package muxd

import (
	"context"
	"crypto/sha256"
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

// ReadPairRecord asks one muxer whether it holds a pairing record for udid.
//
// THREE MEANINGS, FOUR SHAPES ON THE WIRE (D5), and the EOF arm is why this is not a plain error
// check. netmuxd answers *no record* by closing with no reply — but a muxer that DIES mid-request
// closes exactly the same way, and so does one restarted underneath the exchange. Read naively an
// EOF is *the record is not there* wearing *quince cannot tell*'s clothes. So on EOF quince dials
// again: reachable means the muxer is alive and its silence was its answer; unreachable means it
// went away and quince does not know.
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
	// The muxer accepted the request and closed without answering. Ask again whether it is there
	// at all: that is what separates "its silence was the answer" from "it went away".
	conn, dialErr := dialEndpoint(ctx, ep)
	if dialErr != nil {
		return PairRecord{State: PairRecordUnknown}
	}
	_ = conn.Close()
	return PairRecord{State: PairRecordAbsent}
}

// errNoReply is the muxer accepting a request and closing without writing a frame.
var errNoReply = errors.New("muxd: muxer closed without replying")

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

	body, _, err := readPlist(conn)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return PairRecord{}, errNoReply
		}
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
	// A Result with a non-zero Number is usbmuxd's "no record". A zero Number with no data is not
	// an answer this call can use, and quince says it does not know rather than guessing absent.
	if msg.Number != 0 {
		return PairRecord{State: PairRecordAbsent}, nil
	}
	return PairRecord{}, fmt.Errorf("muxd: ReadPairRecord reply carried neither a record nor a result")
}
