package wire

// The `data.code` vocabulary of the vault surface. Two documents freeze it between them:
// contracts §4 names the codes the SEAM answers with, and §1's status table names the two
// the join and the HTTP layer add on top (`busy`, `unavailable`) plus the class refusal
// (`unsupported_version`).
//
// IT LIVES HERE SO THERE IS ONE LIST RATHER THAN TWO. Both the producer (`vaultsvc`, and
// `UnavailableVaultBrowse` beside it) and the consumer (`httpapi`'s status mapper) already
// import this package, and the VaultBrowse interface's own doc comment gives the reason in
// as many words: putting the taxonomy in two places is putting it in one place and one copy
// that drifts. A code added at an emit site without a line here is a compile-time nothing;
// a code added here without a status is a test failure, which is the direction that helps.
const (
	VaultCodeBadPassword     = "bad_password"
	VaultCodeCorruptManifest = "corrupt_manifest"
	VaultCodeIO              = "io"
	VaultCodeNotFound        = "not_found"
	VaultCodeUnsupportedIOS  = "unsupported_ios"
	VaultCodeNotAFile        = "not_a_file"
	VaultCodeLocked          = "locked"

	// VaultCodeBusy — the session is real and OCCUPIED: a file stream is open against it,
	// and the registry holds it until the reader closes. Distinct from `locked`, whose
	// session is gone or was never unlocked; the remedies differ (wait vs. unlock again).
	VaultCodeBusy = "busy"

	// VaultCodeUnsupportedVersion — the version is a class this build cannot open. It is
	// answered before any password is checked, so it is not a credential failure.
	VaultCodeUnsupportedVersion = "unsupported_version"

	// VaultCodeUnavailable — no vault subsystem is wired at all (`--demo`, or no storage).
	// A property of the deployment rather than of the version asked for.
	VaultCodeUnavailable = "unavailable"
)

// VaultErrorCodes is every code the vault surface can answer with, and it is what makes the
// HTTP status mapping ASSERTABLE rather than trusted: a mapper that has no case for one of
// these answers 500, which reports "something below failed and quince will not guess what"
// about a request nothing failed on. That is the collapsed-diagnostic shape the
// troubleshooting rule forbids, and it shipped twice — `unsupported_version` and `busy` both
// reached the wire with no case (quince#1375).
//
// Success is the empty code and is deliberately absent: it is not an error code, and an
// incomplete file travels as a FIELD rather than through this vocabulary (contracts §4).
var VaultErrorCodes = []string{
	VaultCodeBadPassword,
	VaultCodeCorruptManifest,
	VaultCodeIO,
	VaultCodeNotFound,
	VaultCodeUnsupportedIOS,
	VaultCodeNotAFile,
	VaultCodeLocked,
	VaultCodeBusy,
	VaultCodeUnsupportedVersion,
	VaultCodeUnavailable,
}
