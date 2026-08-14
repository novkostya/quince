package storage

import (
	_ "embed"
	"strings"
)

// The constrained forced-command helper an operator installs on the ZFS host, shipped IN THE BINARY
// so quince can hand it over rather than sending the operator to a document.
//
// IT IS THE SAME BYTES THE GATE RUNS. qn.6e's G8 executes this file against a stubbed `zfs`
// (`hookcheck_test.go`), so the script quince serves is the script the suite proves — not a copy of
// it. That equivalence is the whole reason the script became a file: it used to live in a fenced
// block in `deploy/storage.md`, where nothing could embed it and shellcheck never opened it
// (quince#818 piece C).
//
// AND IT IS NOW THE SAME BYTES FOR EVERY INSTALL. The dataset arrives from the forced command
// (quince#985), so this file is no longer rendered per storage — which is what stops a second zfs
// storage on one host from overwriting the first's helper and breaking it silently.
//
//go:embed zfshelper/quince-zfs-helper
var zfsHelperScript string

// zfsHelperParentLine is the line that reads the parent out of the forced command. Its exact text is
// load-bearing: `authorizedKeysLine` promises the operator that the dataset in `command="…"` is what
// confines the helper, and this is the line that keeps that promise.
//
// ASSERTED BY A BUILD-TIME TEST RATHER THAN AT RUNTIME, which is a change from what stood here.
// While the script was rendered there was a substitution that could silently no-op, so serving it
// had to be able to fail — `ErrHelperPlaceholder` and a `500`. A static file cannot fail that way:
// nothing is substituted, so the only reachable defect is the script and the key line disagreeing
// about the mechanism, and that is a property of the tree rather than of a request.
const zfsHelperParentLine = `PARENT="${1:-}"`

// ZFSHelperScript returns the helper exactly as it is installed.
//
// NO PARAMETER, AND THAT IS THE POINT OF quince#985. It used to take the operator's dataset and
// interpolate it into a `PARENT=` assignment, which made every install's file different and made the
// single install path (`ZFSHelperPath`) a collision: a second storage's helper overwrote the first's,
// and the first failed at its next commit with nothing pointing back here.
func ZFSHelperScript() string { return zfsHelperScript }

// helperReadsParentFromForcedCommand reports whether the embedded script still takes its parent from
// $1. Used by the build-time test; kept beside the constant so the two cannot drift.
func helperReadsParentFromForcedCommand() bool {
	return strings.Contains(zfsHelperScript, zfsHelperParentLine)
}
