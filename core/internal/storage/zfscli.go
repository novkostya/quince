package storage

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// datasetPattern guards a ZFS dataset name before it reaches an argv (design §6). ZFS names are
// path-like (pool/child/child) plus the usual safe punctuation — no shell metacharacters,
// spaces, or '@' (snapshots are built separately from a validated short name).
var datasetPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:/-]{0,255}$`)

// snapShortPattern guards the snapshot short name (@<this>). quince only ever makes
// quince-<ulid>-<date> names, but adopted/foreign scans see arbitrary ones — validate anyway.
var snapShortPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]{0,127}$`)

// zfsCLI runs host ZFS operations. mode "exec" runs `zfs …` directly (delegated privileges);
// mode "hook" runs the operator's forced-command (e.g. an SSH key to a constrained helper).
// Either way argv is an array, never a shell string; dataset/snap names are validated before
// they reach it; DATASET DESTROY IS NEVER ISSUED (design §5 — quince prints the human command).
// run is overridable so tests inject a fake that records argv and simulates the fs effect.
type zfsCLI struct {
	parent   string // storage.zfs.parent_dataset, e.g. pool/path/iphone-backup
	mode     string // exec | hook
	bin      string // "zfs" for exec
	hookArgv []string
	run      func(ctx context.Context, argv []string) (string, error)
}

func newZFSCLI(parent, mode, hookCmd, bin string) *zfsCLI {
	c := &zfsCLI{parent: parent, mode: mode, bin: bin, run: execRun}
	if bin == "" {
		c.bin = "zfs"
	}
	if mode == "hook" {
		c.hookArgv = strings.Fields(hookCmd) // operator-configured; argv, never a shell string
	}
	return c
}

func (c *zfsCLI) dataset(udid string) string { return c.parent + "/" + udid }

// argv builds the full argv for a zfs operation per mode.
func (c *zfsCLI) argv(op string, args ...string) []string {
	if c.mode == "hook" {
		return append(append(append([]string{}, c.hookArgv...), op), args...)
	}
	return append([]string{c.bin, op}, args...)
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

// Mirror runs the constrained host-side `mirror` verb (HOOK mode only; stack D5 ladder (i),
// (bi)): the helper rebuilds latest/ from working/ via `cp -a --reflink=always` under the job
// lock + atomic swap — touching ONLY the derived latest/, never snapshots (bounded blast radius)
// — where FICLONE works even though the container's unprivileged userns forbids it. The helper
// reports whether the clone actually shared blocks (host-side, a reliable pool-level channel:
// `zfs list -o avail` or `zpool get bclone*` delta), printed as SHARED / COPIED; quince maps
// that to the honest space claim.
func (c *zfsCLI) Mirror(ctx context.Context, udid string) (sharingResult, error) {
	ds := c.dataset(udid)
	if !datasetPattern.MatchString(ds) {
		return sharingUnknown, fmt.Errorf("storage: invalid dataset name %q", ds)
	}
	out, err := c.run(ctx, c.argv("mirror", ds))
	if err != nil {
		return sharingUnknown, fmt.Errorf("zfs mirror %s: %w: %s", ds, err, strings.TrimSpace(out))
	}
	switch {
	case strings.Contains(out, "SHARED"):
		return sharingYes, nil
	case strings.Contains(out, "COPIED"):
		return sharingNo, nil
	default:
		return sharingUnknown, nil // helper gave no verdict → honest UNVERIFIED
	}
}

// snapNameFor builds quince's snapshot short name: quince-<versionID>-<YYYY-MM-DD>.
func snapNameFor(versionID string, created time.Time) string {
	return "quince-" + versionID + "-" + created.UTC().Format(snapDateLayout)
}
