package demo

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/novkostya/quince/core/internal/wire"
)

// The eviction half of quince#465. The single-flight half (singleflight_test.go) bounds how many
// ops can be IN FLIGHT; this bounds how many are RETAINED. quince#467 measured the defect as "one
// request buys one permanent map entry" — `delete(p.ops, …)` appeared nowhere in this package.
//
// These seed p.ops directly rather than scripting 200 ops. A scripted op sleeps ~2.7s by design,
// so driving the cap through the public API would take ~9 minutes of wall clock to assert a
// property of one predicate. The real path is still exercised once, in the first test.

func usbDemoUDID(t *testing.T, p *Provider) string {
	t.Helper()
	for _, d := range p.Devices() {
		if d.Transports.USB != nil {
			return d.UDID
		}
	}
	t.Fatal("no USB device in the demo fixtures — the pair route cannot be exercised")
	return ""
}

// seedTerminalOps fills p.ops with n succeeded ops, as a long demo session would.
func seedTerminalOps(p *Provider, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("SEEDED-TERMINAL-%04d", i)
		p.ops[id] = wire.Op{ID: id, UDID: "SEEDED-UDID", Kind: "pair", State: "succeeded"}
	}
}

// TestDemoOpsMapIsBounded drives the REAL path: with the map at the cap, starting one more op
// collects the terminal residue instead of growing forever.
func TestDemoOpsMapIsBounded(t *testing.T) {
	p := newRunningProvider(t)
	udid := usbDemoUDID(t, p)
	seedTerminalOps(p, demoOpsSoftCap)

	p.mu.RLock()
	before := len(p.ops)
	p.mu.RUnlock()
	if before != demoOpsSoftCap {
		t.Fatalf("seed = %d ops (want %d)", before, demoOpsSoftCap)
	}

	opID, status, reason := p.Pair(context.Background(), udid)
	if status != http.StatusAccepted {
		t.Fatalf("Pair = %d %q (want 202)", status, reason)
	}

	p.mu.RLock()
	after := len(p.ops)
	_, newOpKept := p.ops[opID]
	p.mu.RUnlock()

	if after >= before {
		t.Fatalf("ops went %d → %d across one op start (want the terminal residue collected)", before, after)
	}
	if !newOpKept {
		t.Fatal("the op that triggered the prune was itself collected — a caller would poll a 404 for live work")
	}
}

// TestDemoPruneKeepsInFlightOps — the predicate is what makes the cap safe. Collecting a running
// op would drop work still in progress, and GET /api/ops/{id} would 404 on it mid-flight.
func TestDemoPruneKeepsInFlightOps(t *testing.T) {
	p := newRunningProvider(t)
	seedTerminalOps(p, demoOpsSoftCap)

	live := map[string]string{
		"LIVE-RUNNING": "running",
		"LIVE-WAITING": "waiting_for_user",
	}
	p.mu.Lock()
	for id, state := range live {
		p.ops[id] = wire.Op{ID: id, UDID: "SEEDED-UDID", Kind: "pair", State: state}
	}
	p.pruneOpsLocked()
	p.mu.Unlock()

	p.mu.RLock()
	defer p.mu.RUnlock()
	for id, state := range live {
		if _, ok := p.ops[id]; !ok {
			t.Errorf("prune collected a %s op (%s) — only terminal ops may be dropped", state, id)
		}
	}
	if len(p.ops) != len(live) {
		t.Fatalf("after prune %d ops remain (want exactly the %d non-terminal ones)", len(p.ops), len(live))
	}
}

// TestDemoPruneIsANoOpBelowTheCap — a short demo session keeps its history, so a visitor can still
// open an op they started a minute ago. The bound exists for the pathological case, not the normal
// one.
func TestDemoPruneIsANoOpBelowTheCap(t *testing.T) {
	p := newRunningProvider(t)
	seedTerminalOps(p, demoOpsSoftCap-1)

	p.mu.Lock()
	p.pruneOpsLocked()
	p.mu.Unlock()

	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ops) != demoOpsSoftCap-1 {
		t.Fatalf("prune dropped %d ops below the cap (want none)", demoOpsSoftCap-1-len(p.ops))
	}
}
