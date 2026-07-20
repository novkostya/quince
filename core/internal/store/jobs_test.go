package store

import (
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }

func TestJobInsertGetUpdate(t *testing.T) {
	st := openTemp(t)
	start := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	j := JobRow{
		ID: "j1", UDID: "UDID0", Kind: "backup", Transport: "usb", State: "backing_up",
		Phase: "receiving", Percent: f64(12.5), BytesDone: 100, BytesTotal: 800, FilesReceived: 4,
		Liveness: "active", StartedAt: start, IntentID: "j1", Attempt: 1,
	}
	if err := st.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.GetJob("j1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.State != "backing_up" || got.Phase != "receiving" || got.Percent == nil || *got.Percent != 12.5 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.FinishedAt != nil || got.ErrorCode != "" || got.VersionID != nil {
		t.Fatalf("expected null finished/error/version, got %+v", got)
	}

	// Terminal update: succeeded with a version and finished_at.
	fin := start.Add(time.Minute)
	ver := "v1"
	j.State, j.Phase, j.Percent, j.FinishedAt, j.VersionID = "succeeded", "done", f64(100), &fin, &ver
	if err := st.UpdateJob(j); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.GetJob("j1")
	if got.State != "succeeded" || got.VersionID == nil || *got.VersionID != "v1" || got.FinishedAt == nil {
		t.Fatalf("post-update mismatch: %+v", got)
	}
}

func TestJobListCursorAndFilter(t *testing.T) {
	st := openTemp(t)
	base := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"j1", "j2", "j3"} {
		udid := "A"
		if id == "j3" {
			udid = "B"
		}
		if err := st.InsertJob(JobRow{ID: id, UDID: udid, Kind: "backup", Transport: "usb",
			State: "succeeded", StartedAt: base, IntentID: id, Attempt: 1, Liveness: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	// Newest-first pagination, limit 2.
	page, next, err := st.ListJobs("", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != "j3" || page[1].ID != "j2" || next != "j2" {
		t.Fatalf("page1=%v next=%q", jobIDs(page), next)
	}
	page2, next2, _ := st.ListJobs("", next, 2)
	if len(page2) != 1 || page2[0].ID != "j1" || next2 != "" {
		t.Fatalf("page2=%v next=%q", jobIDs(page2), next2)
	}
	// udid filter.
	only, _, _ := st.ListJobs("A", "", 10)
	if len(only) != 2 {
		t.Fatalf("udid A jobs = %d, want 2", len(only))
	}
}

func TestListNonTerminalJobs(t *testing.T) {
	st := openTemp(t)
	base := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	rows := []JobRow{
		{ID: "j1", State: "backing_up"}, {ID: "j2", State: "succeeded"},
		{ID: "j3", State: "committing"}, {ID: "j4", State: "connection_lost"},
	}
	for _, r := range rows {
		r.UDID, r.Kind, r.Transport, r.StartedAt, r.IntentID, r.Attempt, r.Liveness =
			"U", "backup", "usb", base, r.ID, 1, "active"
		if err := st.InsertJob(r); err != nil {
			t.Fatal(err)
		}
	}
	live, err := st.ListNonTerminalJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 2 {
		t.Fatalf("non-terminal = %v, want [j1 j3]", jobIDs(live))
	}
	for _, j := range live {
		if JobIsTerminal(j.State) {
			t.Fatalf("%s is terminal but returned", j.ID)
		}
	}
}

func jobIDs(rows []JobRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}
