package demo

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// The demo provider's copy of quince#465's ruling. It is a copy rather than shared code — the two
// providers share no types — so it gets its own gate: a rule that holds in the product and not in
// the demo would make the public demo teach a refusal the product does not give.

// busyDemoDevice returns a running provider and the udid of a device with a pair op in flight.
// scriptPair sleeps 700ms before its first transition, which is the window these tests use.
func busyDemoDevice(t *testing.T) (*Provider, string) {
	t.Helper()
	p := newRunningProvider(t)
	udid := ""
	for _, d := range p.Devices() {
		if d.Transports.USB != nil {
			udid = d.UDID
			break
		}
	}
	if udid == "" {
		t.Fatal("no USB device in the demo fixtures — the pair route cannot be exercised")
	}
	if _, status, reason := p.Pair(context.Background(), udid); status != http.StatusAccepted {
		t.Fatalf("first Pair = %d %q (want 202)", status, reason)
	}
	return p, udid
}

// TestDemoRefusesASecondOpWhateverItsKind is what quince#467 measured going the other way: 20 pair
// requests, 20 accepted, 20 distinct op_ids. The cross-kind rows are the ruling's point.
func TestDemoRefusesASecondOpWhateverItsKind(t *testing.T) {
	p, udid := busyDemoDevice(t)

	for _, tc := range []struct {
		kind string
		call func() (string, int, string)
	}{
		{"pair", func() (string, int, string) { return p.Pair(context.Background(), udid) }},
		{"encryption", func() (string, int, string) {
			return p.Encryption(context.Background(), udid, "enable", "test", "", "")
		}},
		{"wifi_sync", func() (string, int, string) { return p.WifiSync(context.Background(), udid, "enable") }},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			opID, status, reason := tc.call()
			if status != http.StatusConflict {
				t.Fatalf("%s while an op is in flight = %d %q (want 409)", tc.kind, status, reason)
			}
			if reason != inFlightMsg {
				t.Fatalf("%s refusal = %q (want the single-flight message the real manager gives)", tc.kind, reason)
			}
			if opID != "" {
				t.Fatalf("%s refused but handed out op id %q", tc.kind, opID)
			}
		})
	}
}

// TestDemoBurstBuysOneOpNotTwenty is quince#467's measurement inverted into a gate. The finding
// was "one request buys one permanent map entry"; with the guard, twenty buy one.
func TestDemoBurstBuysOneOpNotTwenty(t *testing.T) {
	p, udid := busyDemoDevice(t)

	for i := 0; i < 19; i++ {
		if _, status, _ := p.Pair(context.Background(), udid); status != http.StatusConflict {
			t.Fatalf("Pair #%d of a 20-request burst = %d (want 409)", i+2, status)
		}
	}

	p.mu.RLock()
	ops, inflight := len(p.ops), len(p.opInflight)
	p.mu.RUnlock()
	if ops != 1 {
		t.Fatalf("20 pair requests recorded %d ops (want 1 — this is quince#467's measurement)", ops)
	}
	if inflight != 1 {
		t.Fatalf("opInflight = %d (want 1)", inflight)
	}
}

// TestDemoSlotIsFreedWhenTheScriptEnds — the demo must not strand its own fixture device, or the
// public demo becomes unusable for every visitor after the first until it resets.
func TestDemoSlotIsFreedWhenTheScriptEnds(t *testing.T) {
	p, udid := busyDemoDevice(t)

	deadline := time.Now().Add(15 * time.Second)
	for {
		opID, status, reason := p.Pair(context.Background(), udid)
		if status == http.StatusAccepted && opID != "" {
			return
		}
		if status != http.StatusConflict {
			t.Fatalf("Pair after the scripted op ended = %d %q (want 202, or 409 while it runs)", status, reason)
		}
		if time.Now().After(deadline) {
			t.Fatal("the demo's single-flight slot was never released — the device is stranded until the demo resets")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
