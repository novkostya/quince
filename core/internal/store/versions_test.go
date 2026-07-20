package store

import (
	"testing"
	"time"
)

func strp(s string) *string { return &s }

func TestVersionsInsertListGetDelete(t *testing.T) {
	st := openTemp(t)
	base := time.Date(2026, 7, 18, 2, 30, 11, 0, time.UTC)
	sv := base.Add(time.Minute)

	older := VersionRow{
		ID: "01OLDER", UDID: "UDID-A", Backend: "reflink",
		CreatedAt: base, JobID: strp("job-1"), Kind: "full", Encrypted: true,
		IsLatest: false, StructureVerifiedAt: &sv, LogicalBytes: 100, PhysicalBytes: 10,
	}
	newer := VersionRow{
		ID: "01NEWER", UDID: "UDID-A", Backend: "reflink",
		CreatedAt: base.Add(time.Hour), JobID: strp("job-2"), Kind: "incremental", Encrypted: true,
		IsLatest: true, StructureVerifiedAt: &sv, LogicalBytes: 120, PhysicalBytes: 5,
	}
	other := VersionRow{
		ID: "01OTHER", UDID: "UDID-B", Backend: "zfs",
		ZFSSnapshot: strp("tank/x/UDID-B@quince-01OTHER-2026-07-18"),
		CreatedAt:   base, JobID: nil, Kind: "unknown", Encrypted: true, IsLatest: true,
	}
	for _, v := range []VersionRow{older, newer, other} {
		if err := st.InsertVersion(v); err != nil {
			t.Fatalf("insert %s: %v", v.ID, err)
		}
	}

	// ListVersions(udid) is newest-first and udid-scoped.
	a, err := st.ListVersions("UDID-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 || a[0].ID != "01NEWER" || a[1].ID != "01OLDER" {
		t.Fatalf("UDID-A list = %+v, want [01NEWER, 01OLDER]", ids(a))
	}
	// "" returns all devices.
	all, err := st.ListVersions("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all list = %d, want 3", len(all))
	}

	// Nullable round-trips: adopted (job_id nil), zfs_snapshot, verified-time pointers.
	got, ok, err := st.GetVersion("01OTHER")
	if err != nil || !ok {
		t.Fatalf("get 01OTHER: ok=%v err=%v", ok, err)
	}
	if got.JobID != nil {
		t.Fatalf("adopted job_id = %v, want nil", *got.JobID)
	}
	if got.ZFSSnapshot == nil || *got.ZFSSnapshot == "" {
		t.Fatalf("zfs_snapshot did not round-trip: %v", got.ZFSSnapshot)
	}
	if got.ContentVerifiedAt != nil {
		t.Fatalf("content_verified_at = %v, want nil", got.ContentVerifiedAt)
	}
	gotA, _, _ := st.GetVersion("01OLDER")
	if gotA.StructureVerifiedAt == nil || !gotA.StructureVerifiedAt.Equal(sv) {
		t.Fatalf("structure_verified_at round-trip: %v, want %v", gotA.StructureVerifiedAt, sv)
	}

	// Delete removes exactly one.
	if err := st.DeleteVersion("01OLDER"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.GetVersion("01OLDER"); ok {
		t.Fatal("01OLDER still present after delete")
	}
}

func TestPromoteLatestIsExclusive(t *testing.T) {
	st := openTemp(t)
	base := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"01A", "01B", "01C"} {
		if err := st.InsertVersion(VersionRow{
			ID: id, UDID: "U", Backend: "copy",
			CreatedAt: base.Add(time.Duration(i) * time.Hour), IsLatest: i == 0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PromoteLatest("U", "01C"); err != nil {
		t.Fatal(err)
	}
	vs, _ := st.ListVersions("U")
	var latest []string
	for _, v := range vs {
		if v.IsLatest {
			latest = append(latest, v.ID)
		}
	}
	if len(latest) != 1 || latest[0] != "01C" {
		t.Fatalf("latest = %v, want exactly [01C]", latest)
	}
}

func TestMarkVersionMissingAndContentVerified(t *testing.T) {
	st := openTemp(t)
	if err := st.InsertVersion(VersionRow{
		ID: "01M", UDID: "U", Backend: "reflink", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkVersionMissing("01M", true); err != nil {
		t.Fatal(err)
	}
	got, _, _ := st.GetVersion("01M")
	if !got.Missing {
		t.Fatal("missing flag not set")
	}
	when := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	if err := st.SetContentVerified("01M", when); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.GetVersion("01M")
	if got.ContentVerifiedAt == nil || !got.ContentVerifiedAt.Equal(when) {
		t.Fatalf("content_verified_at = %v, want %v", got.ContentVerifiedAt, when)
	}
}

func ids(vs []VersionRow) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.ID
	}
	return out
}
