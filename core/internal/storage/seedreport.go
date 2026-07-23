package storage

// The zfs working/ SEED strategy + honest space reporting (qn.5b — the reflink moved from the old
// commit-time latest/ mirror to job-start seeding of working/<udid> from latest/, so latest/ is now
// the backup directly via the atomic exchange and there is no commit mirror at all). Invariants
// carried over from the stack-D5 (bi)/(bj)/(bk) investigation:
//   - The seed clones latest/ → working/<udid>. On the hook profile in-container FICLONE returns
//     EPERM in the unprivileged userns (gate-12 (bi)), so the hook does it host-side
//     (SeedHookReflink); the hookless/exec path clones in-container.
//   - The seed NEVER hardlinks (amendment A, decisions (co)): a hardlink seed would alias
//     working/<udid> to the committed latest/, so an in-place idevicebackup2 write to a file class
//     not yet on clonetree.MutatesInPlace could corrupt the committed version — the deferred-12c
//     hazard. So the in-container ladder is reflink → copy (surfaced), never hardlink.
//   - The sharing MEASUREMENT governs only the honest CLAIM; absent a usable channel the claim is
//     reported UNVERIFIED — never a silent zero-space claim ((bj)/(bk)).

// SeedMode is the strategy a zfs working/ seed used (surfaced for logs + /api/health).
type SeedMode string

const (
	SeedHookReflink SeedMode = "hook-reflink" // host-side reflink via the constrained hook `seed` verb
	SeedReflink     SeedMode = "reflink"      // in-container reflink from latest/
	SeedCopy        SeedMode = "copy"         // full copy — always correct, cost surfaced
)

// SeedReport is the surfaced outcome of a working/ seed: the mode + an HONEST space claim. Never a
// silent zero-space assertion ((bj)/(bk)).
type SeedReport struct {
	Mode  SeedMode
	Claim string
}

// sharingResult is the outcome of a clone-sharing measurement. Conservative by construction: only
// a CONFIDENT sharing verdict is sharingYes, and any ambiguity is sharingUnknown.
type sharingResult int

const (
	sharingUnknown sharingResult = iota // no usable/settled channel → report UNVERIFIED
	sharingYes                          // the clone shared blocks (free space unchanged)
	sharingNo                           // the clone consumed ~full size (FICLONE succeeded but copied)
)

// claimFor builds the honest space claim for an in-container reflink seed given the measurement.
func claimFor(s sharingResult) string {
	switch s {
	case sharingYes:
		return "zero-space verified (reflink shares blocks)"
	case sharingNo:
		return "reflink did not share — full-backup-size seed (surfaced)"
	default:
		return "sharing unverified in this topology — budget full-copy seed space cost"
	}
}

// hookClaim renders the honest space claim for a host-side hook `seed` given its measured verdict.
func hookClaim(shared sharingResult) string {
	switch shared {
	case sharingYes:
		return "zero-space verified (host-side reflink via hook; pool-level sharing confirmed)"
	case sharingNo:
		return "host-side reflink did not share — full-backup-size seed (surfaced)"
	default:
		return "host-side reflink via hook; sharing unverified — budget full-copy seed space cost"
	}
}
