package storage

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// datasetPattern guards a ZFS dataset name before it reaches an argv (design §6). ZFS names are
// path-like (pool/child/child) plus the usual safe punctuation — no shell metacharacters,
// spaces, or '@' (snapshots are built separately from a validated short name).
var datasetPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:/-]{0,255}$`)

// snapShortPattern guards the snapshot short name (@<this>). quince only ever makes
// quince-<date>-<ulid> names (qn.5b), but adopted/foreign scans see arbitrary ones — validate anyway.
var snapShortPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]{0,127}$`)

// zfsCLI runs host ZFS operations through the operator's forced command (`storage.zfs.hook_cmd` —
// e.g. an SSH key to a constrained helper). argv is an array, never a shell string; dataset/snap
// names are validated before they reach it; DATASET DESTROY IS NEVER ISSUED (design §5 — quince
// prints the human command). run is overridable so tests inject a fake that records argv and
// simulates the fs effect.
//
// THERE IS ONE TRANSPORT. quince used to also offer `mode: exec`, running `zfs …` in the container
// against delegated privileges — removed by Operator ruling 2026-08-10 (quince#697, quince#793).
// Do not reintroduce a direct-exec path: the `zfs` CLI talks to the host kernel module over a
// versioned ioctl interface, so shipping the userland would tie the image to a host version that is
// not ours to control. That is the reason containerised tools reach for a helper in the first place.
type zfsCLI struct {
	parent   string // storage.zfs.parent_dataset, e.g. pool/path/iphone-backup
	hookArgv []string
	run      func(ctx context.Context, argv []string) (string, error)
}

// newZFSCLI takes the transport as an ARGV THAT IS ALREADY COMPOSED (quince#818).
//
// It used to take the operator's `hook_cmd` string and `strings.Fields` it. That was a whitespace
// split of free text, so a key path containing a space produced a silently wrong argv and there was
// no spelling in which the operator could have escaped it. `config.ZFSConfig.SSHArgv` builds the
// array from structured fields and this end simply receives it — the class is gone rather than
// guarded against.
func newZFSCLI(parent string, hookArgv []string) *zfsCLI {
	return &zfsCLI{
		parent:   parent,
		hookArgv: append([]string(nil), hookArgv...), // copied: a caller must not alias our prefix
		run:      execRun,
	}
}

func (c *zfsCLI) dataset(udid string) string { return c.parent + "/" + udid }

// argv prefixes the operator's forced command to the verb and its arguments.
func (c *zfsCLI) argv(op string, args ...string) []string {
	return append(append(append([]string{}, c.hookArgv...), op), args...)
}

// CreateDataset ensures the child dataset exists (idempotent — an "already exists" is success).
func (c *zfsCLI) CreateDataset(ctx context.Context, udid string) error {
	ds := c.dataset(udid)
	if !datasetPattern.MatchString(ds) {
		return fmt.Errorf("storage: invalid dataset name %q", ds)
	}
	out, err := c.run(ctx, c.argv("create", "-p", ds))
	if err != nil && !strings.Contains(strings.ToLower(out), "already exists") {
		return fmt.Errorf("zfs create %s: %w: %s", ds, err, strings.TrimSpace(out))
	}
	return nil
}

// Snapshot creates <dataset>@<snap> (idempotent on "already exists").
func (c *zfsCLI) Snapshot(ctx context.Context, udid, snap string) error {
	ds := c.dataset(udid)
	if !datasetPattern.MatchString(ds) || !snapShortPattern.MatchString(snap) {
		return fmt.Errorf("storage: invalid dataset/snapshot %q@%q", ds, snap)
	}
	full := ds + "@" + snap
	out, err := c.run(ctx, c.argv("snapshot", full))
	if err != nil && !strings.Contains(strings.ToLower(out), "already exists") {
		return fmt.Errorf("zfs snapshot %s: %w: %s", full, err, strings.TrimSpace(out))
	}
	return nil
}

// ListSnapshots returns the short names of @quince-* snapshots on the device's dataset.
func (c *zfsCLI) ListSnapshots(ctx context.Context, udid string) ([]string, error) {
	ds := c.dataset(udid)
	if !datasetPattern.MatchString(ds) {
		return nil, fmt.Errorf("storage: invalid dataset name %q", ds)
	}
	out, err := c.run(ctx, c.argv("list", "-t", "snapshot", "-H", "-o", "name", "-r", ds))
	if err != nil {
		// A dataset with no snapshots (or absent) is not an error for scanning purposes.
		if strings.Contains(strings.ToLower(out), "does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("zfs list %s: %w: %s", ds, err, strings.TrimSpace(out))
	}
	var snaps []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		short := snapName(line)
		if strings.HasPrefix(short, "quince-") {
			snaps = append(snaps, short)
		}
	}
	return snaps, nil
}

// DestroySnapshot destroys <dataset>@<snap>. This is a SNAPSHOT destroy (allowed in the
// constrained hook key); a DATASET destroy is never issued here.
func (c *zfsCLI) DestroySnapshot(ctx context.Context, udid, snap string) error {
	ds := c.dataset(udid)
	if !datasetPattern.MatchString(ds) || !snapShortPattern.MatchString(snap) {
		return fmt.Errorf("storage: invalid dataset/snapshot %q@%q", ds, snap)
	}
	full := ds + "@" + snap
	out, err := c.run(ctx, c.argv("destroy", full))
	if err != nil && !strings.Contains(strings.ToLower(out), "could not find") {
		return fmt.Errorf("zfs destroy %s: %w: %s", full, err, strings.TrimSpace(out))
	}
	return nil
}

// Rollback rolls <dataset> back to <dataset>@<snap>, discarding everything written since. It is
// qn.6h's ABANDON path and RepairWorkingCopy is its only caller: never after verify has passed, and
// never the automatic response to a failed job — a failed job KEEPS its dirty head so a retry
// resumes without re-transferring, and rolling back on failure would discard exactly that.
//
// NO -r, EVER, AND THE HELPER ENFORCES IT INDEPENDENTLY. Without -r, `zfs rollback` refuses any
// snapshot but the most recent, and -r/-R are what destroy NEWER snapshots — i.e. committed
// versions. The forced-command helper also discards every flag (it rebuilds the command as verb +
// last arg), so this is guarded on both sides: measured 2026-08-08 on a real pool, `rollback -r
// <snap>` reached zfs as a plain rollback and the newer snapshot survived.
//
// The refusal when a newer snapshot exists is EXPECTED, not exceptional, and its text is returned
// verbatim rather than folded into a category: `cannot rollback to '<snap>': more recent snapshots
// or bookmarks exist`. Any snapshotter running on the host produces it — which is why excluding
// quince's datasets from one is required setup (deploy/storage.md), and why the caller must name
// THAT remedy rather than a busy-mount one (qn.6h D4 answer C, gate G5c).
// Rollback validates STRICTLY where its siblings validate safely, and it keeps doing so now that
// the helper is the only transport. snapShortPattern is deliberately permissive — its comment says
// why: "adopted/foreign scans see arbitrary ones". That is right for reading and for the verbs a
// constrained helper re-checks. This check is the NEAR SIDE of two independent ones: the helper's
// `case "$target" in "$PARENT"/*@quince-*)` is the far side, and it lives in a file quince does not
// install and cannot read. Do not delete this as redundant with it — a helper predating quince#600
// is a real deployment, and rolling a device dataset back to somebody else's snapshot is not
// quince's to do. Hence: the quince prefix, and validUDID so a crafted name cannot leave $PARENT.
func (c *zfsCLI) Rollback(ctx context.Context, udid, snap string) error {
	if !validUDID(udid) {
		return fmt.Errorf("storage: invalid udid %q", udid)
	}
	ds := c.dataset(udid)
	if !datasetPattern.MatchString(ds) || !snapShortPattern.MatchString(snap) ||
		!strings.HasPrefix(snap, "quince-") {
		return fmt.Errorf("storage: invalid dataset/snapshot %q@%q — rollback targets quince's own snapshots only", ds, snap)
	}
	full := ds + "@" + snap
	// Deliberately NOT swallowing a class of failure the way Snapshot and DestroySnapshot do: every
	// failure here is something the operator has to see. Rolling back to an absent snapshot is a
	// real error, and the "more recent snapshots" refusal is the one whose text the caller surfaces.
	if out, err := c.run(ctx, c.argv("rollback", full)); err != nil {
		return fmt.Errorf("zfs rollback %s: %w: %s", full, err, strings.TrimSpace(out))
	}
	return nil
}

// snapNameFor builds quince's snapshot short name: quince-<YYYY-MM-DDTHH-MM>-<versionID> (qn.5b
// amendment B, decisions (co)). Date-first for readable `zfs list` ordering; the ULID (==
// versionID) kept as the collision-free tail — two same-minute commits get distinct names, and the
// name maps back to the version/marker/logs. The `quince-` prefix is preserved so the constrained
// hook glob `@quince-*` and ListSnapshots' HasPrefix are unaffected.
func snapNameFor(versionID string, created time.Time) string {
	return "quince-" + created.UTC().Format(snapDateLayout) + "-" + versionID
}

// Capacity reports the PARENT dataset's used+available, in bytes.
//
// THIS EXISTS BECAUSE `statfs` IS THE WRONG INSTRUMENT ON ZFS (quince#585, Operator ruling
// 2026-08-03). Backups live in per-device CHILD datasets, and `statfs` on the parent reports the
// parent's OWN used — 256 K against seventeen backups on the staging stand, so the card rendered
// "431.4 GB free of 431.4 GB" for a storage that was far from empty.
//
// zfs `used` on a parent ALREADY INCLUDES DESCENDANTS, so one call measures the quantity gap A
// already ruled: total = used + available, free = available. The field's MEANING was right; its
// measurement was wrong on one backend. That is why the ruling changed no contract text.
//
// `-p` is load-bearing: without it zfs prints human units ("399G") and this would have to parse
// them. With it the values are exact bytes.
func (c *zfsCLI) Capacity(ctx context.Context) (free, total uint64, err error) {
	if !datasetPattern.MatchString(c.parent) {
		return 0, 0, fmt.Errorf("storage: invalid parent dataset %q", c.parent)
	}
	// A DEDICATED `capacity` VERB IN HOOK MODE, never a flagged `list` — Operator ruling
	// 2026-08-03 (quince#600). This first shipped as `list -H -p -o used,available <parent>`,
	// which assumes the hook forwards argv to `zfs`. IT DOES NOT: `hook_cmd` is a forced command
	// whose arms run FIXED commands, and its `list` arm runs `zfs list -t snapshot`. Measured
	// against the deployed helper, that call **exits 0** and returns the snapshot list — a
	// SUCCEEDED command with wrong-shaped output, which is why nothing noticed for a release and
	// why the field-count check below is load-bearing rather than defensive boilerplate. Relax it
	// and this returns a confident capacity computed from snapshot names.
	//
	// `list` was deliberately NOT taught to forward flags: the same key would then take arbitrary
	// `zfs list` arguments, and "dataset destroy impossible via the key" would stop being
	// checkable by reading five case arms. The verb takes NO caller argument at all — the helper
	// uses its own configured $PARENT — which is tighter than the arms that accept a
	// pattern-guarded target. Operators upgrading MUST add the arm; see deploy/storage.md.
	out, err := c.run(ctx, c.argv("capacity"))
	if err != nil {
		return 0, 0, fmt.Errorf("zfs capacity %s: %w: %s", c.parent, err, strings.TrimSpace(out))
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("zfs capacity %s: want 2 fields (used, available), got %q", c.parent, strings.TrimSpace(out))
	}
	used, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("zfs capacity %s: parsing used %q: %w", c.parent, fields[0], err)
	}
	avail, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("zfs capacity %s: parsing available %q: %w", c.parent, fields[1], err)
	}
	return avail, used + avail, nil
}
