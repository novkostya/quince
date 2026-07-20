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

// namespaceBackend implements the reflink/hardlink/copy version model (design §5): latest/ is
// the newest verified backup, prior versions are versions/<ts>/ dirs, the writer works in
// work/<job>/, and commit rotates by a journaled rename pair. The only difference between the
// three is the clonetree strategy used for seeding + (implicitly) nothing else.
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
	for _, d := range []string{nsLatest(b.backups, udid), nsVersions(b.backups, udid),
		filepath.Join(deviceDir(b.backups, udid), "work")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (b *namespaceBackend) WorkDir(udid, jobID string) (string, error) {
	if !validUDID(udid) {
		return "", fmt.Errorf("storage: invalid udid %q", udid)
	}
	work := nsWork(b.backups, udid, jobID)
	if err := os.RemoveAll(work); err != nil {
		return "", err
	}
	latest := nsLatest(b.backups, udid)
	if !isEmptyDir(latest) {
		// Seed a true incremental: clone latest/ → work/<job> (design §5 Seed).
		if err := clonetree.Clone(work, latest, b.strategy); err != nil {
			return "", fmt.Errorf("storage: seed work from latest: %w", err)
		}
		b.log.Info("storage: seeded work from latest", "backend", b.name, "udid", udid, "job", jobID)
	} else {
		if err := os.MkdirAll(work, 0o755); err != nil {
			return "", err
		}
	}
	return work, nil
}

func (b *namespaceBackend) TreePath(udid, jobID string) string {
	return nsWork(b.backups, udid, jobID)
}

func (b *namespaceBackend) Commit(req CommitReq) (Committed, error) {
	work := nsWork(b.backups, req.UDID, req.JobID)
	if isEmptyDir(work) {
		return Committed{}, fmt.Errorf("storage: work dir %s is empty — nothing to commit", work)
	}
	svAt := req.CreatedAt
	if err := WriteMarker(work, Marker{
		VersionID: req.VersionID, JobID: req.JobID, UDID: req.UDID, Backend: b.name,
		CreatedAt: fmtRFC(req.CreatedAt), Kind: req.Verify.Kind, Encrypted: req.Verify.Encrypted,
		StructureVerifiedAt: fmtRFC(svAt), AppVersion: b.appVersion,
	}); err != nil {
		return Committed{}, err
	}
	dev := deviceDir(b.backups, req.UDID)
	j := Journal{
		VersionID: req.VersionID, UDID: req.UDID, Backend: b.name, JobID: req.JobID,
		Phase: PhasePrepared, CreatedAt: fmtRFC(req.CreatedAt), Kind: req.Verify.Kind,
		Encrypted: req.Verify.Encrypted, StructureVerifiedAt: fmtRFC(svAt),
		LogicalBytes: req.Verify.LogicalBytes, JobDir: work,
	}
	if err := writeJournal(dev, j); err != nil {
		return Committed{}, err
	}
	if err := b.finishRotation(&j); err != nil {
		return Committed{}, err
	}
	return b.committedFromLatest(req.UDID)
}

// finishRotation performs the archive+promote rename pair from the journal's current phase,
// idempotently (each step checks fs state), journaling between and clearing at the end. Shared
// by Commit and ResumeCommit — the roll-forward path.
func (b *namespaceBackend) finishRotation(j *Journal) error {
	dev := deviceDir(b.backups, j.UDID)
	latest := nsLatest(b.backups, j.UDID)

	if j.Phase == PhasePrepared {
		// Archive the previous latest (if any) to versions/<prev-ts>/.
		if !isEmptyDir(latest) {
			prevTS := j.PrevTS
			if prevTS == "" {
				prevTS = b.prevLatestTS(latest)
			}
			dst := nsVersionDir(b.backups, j.UDID, mustParseTSOrNow(prevTS))
			if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
				if err := os.MkdirAll(nsVersions(b.backups, j.UDID), 0o755); err != nil {
					return err
				}
				if err := os.Rename(latest, dst); err != nil {
					return fmt.Errorf("storage: archive previous latest: %w", err)
				}
			}
			j.PrevTS = prevTS
		}
		j.Phase = PhasePreviousArchived
		if err := writeJournal(dev, *j); err != nil {
			return err
		}
	}

	if j.Phase == PhasePreviousArchived {
		// Promote work/<job> → latest/. latest/ is either absent (archived) or the empty
		// Provision placeholder — remove it first so the rename can create latest/ fresh
		// (rename onto an existing dir is not portably allowed). Non-empty latest = already
		// promoted (idempotent resume) → skip.
		if isEmptyDir(latest) {
			if err := os.RemoveAll(latest); err != nil {
				return err
			}
			if err := os.Rename(j.JobDir, latest); err != nil {
				return fmt.Errorf("storage: promote work to latest: %w", err)
			}
		}
		j.Phase = PhaseLatestPromoted
		if err := writeJournal(dev, *j); err != nil {
			return err
		}
	}

	// latest_promoted → fs consistent; clear the journal + sweep the (now-empty) work parent's
	// stragglers is left to reconciliation.
	return removeJournal(dev)
}

func (b *namespaceBackend) ResumeCommit(j Journal) (Committed, bool, error) {
	if j.Phase == PhaseLatestPromoted {
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

func (b *namespaceBackend) Discard(udid, jobID string) (string, error) {
	return "", os.RemoveAll(nsWork(b.backups, udid, jobID))
}

func (b *namespaceBackend) DeleteArtifact(a Artifact) error {
	created := mustParseTSOrNow(a.Marker.CreatedAt)
	dir := nsVersionDir(b.backups, a.UDID, created)
	if a.IsLatest {
		dir = nsLatest(b.backups, a.UDID)
	}
	return os.RemoveAll(dir)
}

func (b *namespaceBackend) RepairWorkingCopy(udid string) error {
	latest := nsLatest(b.backups, udid)
	if isEmptyDir(latest) {
		return fmt.Errorf("storage: no last-good version to rebuild the working copy from")
	}
	workRoot := filepath.Join(deviceDir(b.backups, udid), "work")
	if err := os.RemoveAll(workRoot); err != nil {
		return err
	}
	reseed := filepath.Join(workRoot, "current")
	if err := clonetree.Clone(reseed, latest, b.strategy); err != nil {
		return fmt.Errorf("storage: reseed work from latest: %w", err)
	}
	b.log.Info("storage: reseeded working copy from latest", "backend", b.name, "udid", udid)
	return nil
}

func (b *namespaceBackend) Scan(udid string) ([]Artifact, error) {
	var out []Artifact
	if a, ok := b.scanDir(udid, nsLatest(b.backups, udid), true); ok {
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

func (b *namespaceBackend) SweepWork(udid string) error {
	workRoot := filepath.Join(deviceDir(b.backups, udid), "work")
	entries, err := os.ReadDir(workRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(workRoot, e.Name())); err != nil {
			return err
		}
		b.log.Info("storage: swept orphaned work dir", "udid", udid, "name", e.Name())
	}
	return nil
}

// --- helpers ---

func (b *namespaceBackend) committedFromLatest(udid string) (Committed, error) {
	latest := nsLatest(b.backups, udid)
	m, err := ReadMarker(latest)
	if err != nil {
		return Committed{}, fmt.Errorf("storage: read latest marker after commit: %w", err)
	}
	return committedFromMarker(m, dirSize(latest)), nil
}

func (b *namespaceBackend) prevLatestTS(latest string) string {
	if m, err := ReadMarker(latest); err == nil {
		if t, err := parseRFC(m.CreatedAt); err == nil {
			return tsDir(t)
		}
	}
	if fi, err := os.Stat(latest); err == nil {
		return tsDir(fi.ModTime())
	}
	return tsDir(time.Now())
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
