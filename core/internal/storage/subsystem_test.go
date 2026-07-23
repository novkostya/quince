package storage

import (
	"net/http"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Story 7: registry + API — Versions never nil, Delete removes artifact + row + audits + 404s.
func TestManagerVersionsAndDelete(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	commitGoodTree(t, m, testUDID)
	goodEncryptedFull(t, seedTree(t, m, testUDID, "job2"))
	if _, err := m.CommitJob(testUDID, "job2"); err != nil {
		t.Fatal(err)
	}

	vs := m.Versions(testUDID)
	if len(vs) != 2 {
		t.Fatalf("want 2 versions, got %d", len(vs))
	}
	if got := m.Versions("no-such-udid"); got == nil {
		t.Fatal("Versions must never return nil (contract: [] )")
	}

	older := vs[1]
	status, err := m.Delete(older.ID)
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("delete: status=%d err=%v", status, err)
	}
	if len(m.Versions(testUDID)) != 1 {
		t.Fatalf("after delete want 1 version, got %d", len(m.Versions(testUDID)))
	}
	if _, ok, _ := st.GetVersion(older.ID); ok {
		t.Fatal("deleted row still present")
	}
	audits, _ := st.ListAudit(10)
	if !containsAudit(audits, "version.delete") {
		t.Fatal("no version.delete audit row")
	}

	// Unknown id → 404.
	if status, _ := m.Delete("nope"); status != http.StatusNotFound {
		t.Fatalf("delete unknown → %d, want 404", status)
	}
}

// Story 7: version.created / version.deleted events fire.
func TestManagerEmitsVersionEvents(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	sub := m.bus.Subscribe(16)
	defer m.bus.Unsubscribe(sub)

	commitGoodTree(t, m, testUDID)
	if got := drainFor(sub, wire.EventVersionCreated); got == "" {
		t.Fatal("no version.created event")
	}
	vs := m.Versions(testUDID)
	if _, err := m.Delete(vs[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := drainFor(sub, wire.EventVersionDeleted); got == "" {
		t.Fatal("no version.deleted event")
	}
}

// Story 8: post-commit Prune keeps the policy set; adopted/latest protected.
func TestManagerPostCommitPrune(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, RetentionPolicy{KeepRecent: 2})
	for i := 1; i <= 4; i++ {
		job := "job" + string(rune('0'+i))
		goodEncryptedFull(t, seedTree(t, m, testUDID, job))
		if _, err := m.CommitJob(testUDID, job); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	// latest + KeepRecent(2) candidates = 3 remain; the oldest (v000001) is pruned.
	rows, _ := st.ListVersions(testUDID)
	if len(rows) != 3 {
		t.Fatalf("after prune want 3 versions, got %d (%v)", len(rows), ids2(rows))
	}
	if _, ok, _ := st.GetVersion("v000001"); ok {
		t.Fatal("oldest version should have been pruned")
	}
}

func containsAudit(rows []store.AuditEntry, event string) bool {
	for _, r := range rows {
		if r.Event == event {
			return true
		}
	}
	return false
}

// drainFor reads events until one of type typ arrives (or a short timeout), returning typ or "".
func drainFor(sub *bus.Subscription, typ string) string {
	timeout := time.After(2 * time.Second)
	for {
		select {
		case env := <-sub.C():
			if env.Type == typ {
				return typ
			}
		case <-timeout:
			return ""
		}
	}
}
