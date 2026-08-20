package deviceops

import (
	"context"
	"errors"
	"testing"

	"github.com/novkostya/quince/core/internal/muxaddr"
	"github.com/novkostya/quince/core/internal/muxd"
)

// THE ARM THAT DECIDES WHETHER QUINCE CAN VERIFY AT ALL. The exchange itself is muxd's to test —
// it has a scripted socket and a live probe — but resolution needs no socket, and if it is wrong
// every pair returns `pairing_unverified` (quince#1337 review).
func TestPairRecordEndpointResolution(t *testing.T) {
	boom := errors.New("no source for this device")
	cases := []struct {
		name     string
		muxerFor MuxerFor
		wantErr  error // nil means "some error", checked as non-nil
	}{
		{"no resolver wired", nil, nil},
		{"resolver refuses", func(string, string) (muxaddr.Endpoint, error) { return muxaddr.Endpoint{}, boom }, boom},
		{"resolver returns a zero endpoint", func(string, string) (muxaddr.Endpoint, error) {
			return muxaddr.Endpoint{}, nil
		}, muxaddr.ErrEmpty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tl := NewTools(c.muxerFor, discard())
			if c.muxerFor == nil {
				tl.muxerFor = nil // NewTools may not store a nil as nil; be explicit
			}
			_, err := tl.pairRecordEndpoint("UDID", TransportUSB)
			if err == nil {
				t.Fatal("resolution succeeded where it should have failed")
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
		})
	}
}

// THE COLLAPSE, which is the half that decides what the user is told. A resolver failure must
// read as PairRecordUnknown — *quince could not ask* — and never as Absent, because a false
// Absent takes the absent→present arm and bypasses the digest comparison entirely.
func TestUnresolvableMuxerIsUnknownNotAbsent(t *testing.T) {
	tl := NewTools(func(string, string) (muxaddr.Endpoint, error) {
		return muxaddr.Endpoint{}, errors.New("nope")
	}, discard())

	got := tl.readPairRecord(context.Background(), "UDID", TransportUSB)
	if got.State != muxd.PairRecordUnknown {
		t.Fatalf("state = %v, want unknown", got.State)
	}

	// And the op says so: unverified, not not-recorded. The remedies differ.
	ok, code, _ := pairOutcomeFromRecords(got, got)
	if ok || code != "pairing_unverified" {
		t.Fatalf("ok=%v code=%q, want false/pairing_unverified", ok, code)
	}
}

// A resolver that WORKS still yields a real endpoint, so the failure cases above are not passing
// because everything fails (every negative check needs a control).
func TestPairRecordEndpointResolvesWhenTheResolverWorks(t *testing.T) {
	ep := mustEP("/var/run/usbmuxd")
	tl := NewTools(StaticMuxer(ep), discard())
	got, err := tl.pairRecordEndpoint("UDID", TransportUSB)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}
	if got.IsZero() {
		t.Fatal("resolved to a zero endpoint")
	}
}
