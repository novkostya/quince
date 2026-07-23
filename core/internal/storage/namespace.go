package storage

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// namespaceBackend implements the reflink/hardlink/copy version model (design §5; reworked in
// qn.5b to share the unified lifecycle). latest/ is the newest verified backup, prior versions are
// versions/<ts>/ dirs; the writer fills a per-device working/<udid> seeded from latest/, and commit
// ATOMICALLY EXCHANGES it into latest/ (no unoccupied instant) then archives the displaced previous
// content to versions/<prev-ts>/. The only difference between the three is the clonetree strategy
// used for the seed — and it is the SAFE strategy (hardlink downgrades to copy, amendment A (co)).
type namespaceBackend struct {
	name       string
	strategy   clonetree.Strategy
	backups    string
	appVersion string
	log        *slog.Logger
}

func newNamespaceBackend(name string, strategy clonetree.Strategy, backups, appVersion string, log *slog.Logger) *namespaceBackend {
	return &namespaceBackend{name: name, strategy: strategy, backups: backups, appVersion: appVersion, log: log}
}

func (b *namespaceBackend) Name() string { return b.name }

func (b *namespaceBackend) Provision(udid string) error {
	if !validUDID(udid) {
		return fmt.Errorf("storage: invalid udid %q", udid)
	}
	// latest/ + versions/ are permanent; working/ is created per job by WorkDir.
	for _, d := range []string{latestDir(b.backups, udid), nsVersions(b.backups, udid)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// WorkDir returns the idevicebackup2 TARGET (workingParent) after seeding working/<udid> from
// latest/ (design §5 Seed, qn.5b). A non-empty working/<udid> is RESUMED as-is (a prior failed
// attempt); otherwise it is seeded via the backend's SAFE strategy (hardlink→copy, amendment A) or
// created empty on a first backup. The seed decision is recorded in the work sentinel.
func (b *namespaceBackend) WorkDir(udid, _ string) (string, error) {
	if !validUDID(udid) {
		return "", fmt.Errorf("storage: invalid udid %q", udid)
	}
	parent := workingParent(b.backups, udid)
	tree := workingTree(b.backups, udid)
	if !isEmptyDir(tree) {
		b.log.Info("storage: resuming dirty working", "backend", b.name, "udid", udid)
		return parent, nil // already seeded; kind recovered from the sentinel at commit
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	latest := latestDir(b.backups, udid)
	seeded := false
	if !isEmptyDir(latest) {
		safe := seedStrategy(b.strategy)
		if safe != b.strategy {
			b.log.Warn("storage: hardlink seed disabled-to-copy (gate 12c) — seeding working via copy",
				"backend", b.name, "udid", udid)
		}
		if err := clonetree.Clone(tree, latest, safe); err != nil {
			return "", fmt.Errorf("storage: seed working from latest: %w", err)
		}
		seeded = true
		b.log.Info("storage: seeded working from latest", "backend", b.name, "udid", udid, "strategy", safe)
	} else if err := os.MkdirAll(tree, 0o755); err != nil {
		return "", err
	}
	if err := writeWorkState(b.backups, udid, workState{SeededFromLatest: seeded}); err != nil {
		return "", err
	}
	return parent, nil
}

func (b *namespaceBackend) TreePath(udid, _ string) string {
	return workingTree(b.backups, udid)
}

func (b *namespaceBackend) Commit(req CommitReq) (Committed, error) {
	tree := workingTree(b.backups, req.UDID)
	if isEmptyDir(tree) {
		return Committed{}, fmt.Errorf("storage: working tree %s is empty — nothing to commit", tree)
	}
	svAt := req.CreatedAt
	if err := WriteMarker(tree, Marker{
		VersionID: req.VersionID, JobID: req.JobID, UDID: req.UDID, Backend: b.name,
		CreatedAt: fmtRFC(req.CreatedAt), Kind: req.Verify.Kind, Encrypted: req.Verify.Encrypted,
		StructureVerifiedAt: fmtRFC(svAt), AppVersion: b.appVersion,
	}); err != nil {
		return Committed{}, err
	}
	dev := deviceDir(b.backups, req.UDID)
	// Capture the previous latest's timestamp BEFORE the exchange (its marker moves into working/
	// after the swap) so a resume can archive it to the right versions/<prev-ts>/.
	prevTS := ""
	if m, err := ReadMarker(latestDir(b.backups, req.UDID)); err == nil {
		if t, terr := parseRFC(m.CreatedAt); terr == nil {
			prevTS = tsDir(t)
		}
	}
	j := Journal{
		VersionID: req.VersionID, UDID: req.UDID, Backend: b.name, JobID: req.JobID,
		Phase: PhasePrepared, CreatedAt: fmtRFC(req.CreatedAt), Kind: req.Verify.Kind,
		Encrypted: req.Verify.Encrypted, StructureVerifiedAt: fmtRFC(svAt),
		LogicalBytes: req.Verify.LogicalBytes, JobDir: tree, PrevTS: prevTS,
	}
	if err := writeJournal(dev, j); err != nil {
		return Committed{}, err
	}
	if err := b.finishRotation(&j); err != nil {
		return Committed{}, err
	}
	return b.committedFromLatest(req.UDID)
}

// finishRotation performs the atomic exchange then the archive of the displaced previous content,
// idempotently (roll-forward, shared by Commit and ResumeCommit). The exchange is the pivot: it
// swaps working/<udid> into latest/ in one syscall (NO unoccupied instant — the qn.5b fix), leaving
// the previous latest content in working/<udid>, which is then archived to versions/<prev-ts>/. The
// exchange is NOT idempotent, so it is guarded by the version-id marker on latest/.
func (b *namespaceBackend) finishRotation(j *Journal) error {
	dev := deviceDir(b.backups, j.UDID)
	latest := latestDir(b.backups, j.UDID)
	tree := j.JobDir // workingTree

	if j.Phase == PhasePrepared {
		if !latestHasVersion(latest, j.VersionID) {
			if err := os.MkdirAll(latest, 0o755); err != nil { // exchange needs both dirs to exist (first backup: empty latest)
				return err
			}
			if err := exchange(tree, latest); err != nil {
				return fmt.Errorf("storage: exchange working into latest: %w", err)
			}
		}
		j.Phase = PhaseExchanged
		if err := writeJournal(dev, *j); err != nil {
			return err
		}
	}

	if j.Phase == PhaseExchanged {
		// working/<udid> now holds the DISPLACED previous latest content (empty on a first backup).
		// Archive it to versions/<prev-ts>/ (rename is atomic; skip if the destination already
		// exists — a resume, or the rare same-second-prev collision). Then remove working/.
		if !isEmptyDir(tree) && j.PrevTS != "" {
			dst := nsVersionDir(b.backups, j.UDID, mustParseTSOrNow(j.PrevTS))
			if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
				if err := os.MkdirAll(nsVersions(b.backups, j.UDID), 0o755); err != nil {
					return err
				}
				if err := os.Rename(tree, dst); err != nil {
					return fmt.Errorf("storage: archive previous latest: %w", err)
				}
			}
		}
		_ = os.RemoveAll(workingParent(b.backups, j.UDID))
		removeWorkState(b.backups, j.UDID)
		j.Phase = PhaseArchived
		if err := writeJournal(dev, *j); err != nil {
			return err
		}
	}

	return removeJournal(dev)
}

func (b *namespaceBackend) ResumeCommit(j Journal) (Committed, bool, error) {
	if j.Phase == PhaseArchived {
		_ = removeJournal(deviceDir(b.backups, j.UDID))
		c, err := b.committedFromLatest(j.UDID)
		return c, true, err
	}
	if err := b.finishRotation(&j); err != nil {
		return Committed{}, false, err
	}
	c, err := b.committedFromLatest(j.UDID)
	return c, true, err
}

// Discard keeps the dirty working/ so a retry resumes into it (qn.5b — unified with zfs; the old
// namespace behaviour of deleting work/<job> silently restarted retries, finding (cj) #4/#5).
// Reports the last good version for the UI/log; Reset is the explicit discard.
func (b *namespaceBackend) Discard(udid, _ string) (string, error) {
	last := "none"
	if m, err := ReadMarker(latestDir(b.backups, udid)); err == nil {
		last = m.CreatedAt
	}
	return fmt.Sprintf("working copy kept dirty for retry; last good version = %s", last), nil
}

func (b *namespaceBackend) DeleteArtifact(a Artifact) error {
	created := mustParseTSOrNow(a.Marker.CreatedAt)
	dir := nsVersionDir(b.backups, a.UDID, created)
	if a.IsLatest {
		dir = latestDir(b.backups, a.UDID)
	}
	return os.RemoveAll(dir)
}

// RepairWorkingCopy is the qn.5b Reset op: DISCARD the dirty working area so the next backup
// re-seeds cleanly from latest/ (losing only the partial, never a version). The previous "reseed
// work from latest" is superfluous now — WorkDir seeds at job start.
func (b *namespaceBackend) RepairWorkingCopy(udid string) error {
	if err := os.RemoveAll(workingParent(b.backups, udid)); err != nil {
		return err
	}
	removeWorkState(b.backups, udid)
	b.log.Info("storage: reset — discarded dirty working copy", "backend", b.name, "udid", udid)
	return nil
}

func (b *namespaceBackend) Scan(udid string) ([]Artifact, error) {
	var out []Artifact
	if a, ok := b.scanDir(udid, latestDir(b.backups, udid), true); ok {
		out = append(out, a)
	}
	entries, err := os.ReadDir(nsVersions(b.backups, udid))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if a, ok := b.scanDir(udid, filepath.Join(nsVersions(b.backups, udid), e.Name()), false); ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// scanDir builds an Artifact from a version dir, surfacing (not silently skipping) a corrupt
// marker — a hash-failing marker is the one case roll-forward refuses (design §5, story 6).
func (b *namespaceBackend) scanDir(udid, dir string, isLatest bool) (Artifact, bool) {
	m, err := ReadMarker(dir)
	if errors.Is(err, ErrMarkerCorrupt) {
		b.log.Warn("storage: version marker failed its checksum — not adopting", "udid", udid, "dir", dir)
		return Artifact{}, false
	}
	if err != nil {
		return Artifact{}, false
	}
	return Artifact{UDID: udid, Backend: b.name, Marker: m, IsLatest: isLatest, PhysicalBytes: dirSize(dir)}, true
}

func (b *namespaceBackend) PendingJournals() ([]Journal, error) { return scanJournals(b.backups) }

// SweepWork is a no-op (qn.5b): a dirty working/ is first-class resumable state (a retry resumes
// into it), not an orphan to sweep — Reset is the only discard. Unified with zfs.
func (b *namespaceBackend) SweepWork(string) error { return nil }

// --- helpers ---

func (b *namespaceBackend) committedFromLatest(udid string) (Committed, error) {
	latest := latestDir(b.backups, udid)
	m, err := ReadMarker(latest)
	if err != nil {
		return Committed{}, fmt.Errorf("storage: read latest marker after commit: %w", err)
	}
	return committedFromMarker(m, dirSize(latest)), nil
}

func committedFromMarker(m Marker, physical int64) Committed {
	created, _ := parseRFC(m.CreatedAt)
	sv, _ := parseRFC(m.StructureVerifiedAt)
	var snap *string
	c := Committed{
		VersionID: m.VersionID, UDID: m.UDID, Backend: m.Backend, ZFSSnapshot: snap,
		CreatedAt: created, Kind: m.Kind, Encrypted: m.Encrypted, StructureVerifiedAt: sv,
		LogicalBytes: physical, PhysicalBytes: physical,
	}
	if m.JobID != "" {
		j := m.JobID
		c.JobID = &j
	}
	return c
}

func fmtRFC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseRFC(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

func mustParseTSOrNow(ts string) time.Time {
	// ts is a versions/<ts> dir name; parse it back, else best-effort now.
	if t, err := time.Parse(tsDirLayout, ts); err == nil {
		return t
	}
	if t, err := parseRFC(ts); err == nil {
		return t
	}
	return time.Now().UTC()
}
