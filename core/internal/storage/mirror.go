package storage

// The zfs latest/ mirror strategy ladder + honest space reporting (stack D5, ruled
// (bi)/(bj)/(bk) after a five-round investigation). Invariants encoded here:
//   - ALL sharing strategies clone from working/ under the job lock, NEVER from the .zfs mount
//     (reflink-from-snapshot is EXDEV at every layer — cross-superblock, kernel behavior).
//   - The ladder orders by RISK dominance, not space: reflink (independent clones) outranks
//     hardlink (aliases working/ — an in-place mutation would silently corrupt a hard-linked
//     latest/, hence matrix-gated), which outranks copy (always correct, cost surfaced).
//   - The sharing MEASUREMENT governs the honest CLAIM plus exactly ONE selection edge:
//     a measured-not-sharing reflink falls through to hardlink-under-matrix (downgrade-for-
//     space is allowed; blindly upgrading into aliasing risk is not). Absent a usable channel,
//     reflink wins on the risk asymmetry and the claim is reported UNVERIFIED — never a silent
//     zero-space claim.

// MirrorMode is the strategy a zfs latest/ mirror build used (surfaced for logs + /api/health).
type MirrorMode string

const (
	MirrorHookReflink MirrorMode = "hook-reflink" // (i)  host-side reflink via the constrained hook `mirror` verb
	MirrorReflink     MirrorMode = "reflink"      // (ii) in-container reflink from working/
	MirrorHardlink    MirrorMode = "hardlink"     // (iii) hardlink-under-safety-matrix (gate 12c)
	MirrorCopy        MirrorMode = "copy"         // (iv) full copy — always correct, cost surfaced
)

// MirrorReport is the surfaced outcome of a mirror build: the mode + an HONEST space claim.
// Never a silent zero-space assertion ((bj)/(bk)).
type MirrorReport struct {
	Mode  MirrorMode
	Claim string
}

// sharingResult is the outcome of a clone-sharing measurement. Conservative by construction:
// only a CONFIDENT not-sharing verdict is sharingNo (it is the sole trigger of the risky
// hardlink downgrade edge), and any ambiguity is sharingUnknown.
type sharingResult int

const (
	sharingUnknown sharingResult = iota // no usable/settled channel → report UNVERIFIED, keep reflink
	sharingYes                          // the clone shared blocks (free space unchanged)
	sharingNo                           // the clone consumed ~full size (FICLONE succeeded but copied)
)

// claimFor builds the honest space claim for an in-container reflink given the measurement.
func claimFor(s sharingResult) string {
	switch s {
	case sharingYes:
		return "zero-space verified (reflink shares blocks)"
	case sharingNo:
		return "reflink did not share — see downgrade" // should not surface: sharingNo downgrades
	default:
		return "sharing unverified in this topology — budget full-copy space cost"
	}
}
