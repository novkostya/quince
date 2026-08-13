package storage

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
)

// The constrained forced-command helper an operator installs on the ZFS host, shipped IN THE BINARY
// so quince can hand it over with the operator's own parent dataset already filled in.
//
// IT IS THE SAME BYTES THE GATE RUNS. qn.6e's G8 executes this file against a stubbed `zfs`
// (`hookcheck_test.go`), so the script quince serves is the script the suite proves — not a copy of
// it. That equivalence is the whole reason the script became a file: it used to live in a fenced
// block in `deploy/storage.md`, where nothing could embed it and shellcheck never opened it
// (quince#818 piece C).
//
//go:embed zfshelper/quince-zfs-helper
var zfsHelperScript string

// The line an operator used to have to edit by hand. Substituting it is the entire product of this
// file, and its exact text is therefore load-bearing — see RenderZFSHelper.
const zfsHelperPlaceholder = `PARENT="pool/path/to/iphone-backup"`

// ErrHelperPlaceholder means the embedded script no longer carries the line this code substitutes.
//
// IT IS A REFUSAL, NOT A FALLBACK, and that is the point of naming it. If the placeholder is ever
// renamed in the script, the substitution silently no-ops and quince serves a helper that still says
// `PARENT="pool/path/to/iphone-backup"` — which an operator would install verbatim, sending every
// backup to a dataset that is not theirs, or to one that does not exist. Refusing turns a
// silent-wrong-artifact into a 500 with a sentence, and `TestZFSHelperPlaceholderExists` turns it
// into a red build before it can ever reach a user.
var ErrHelperPlaceholder = errors.New("storage: the embedded zfs helper no longer carries the PARENT placeholder")

// RenderZFSHelper returns the helper with PARENT set to the operator's dataset.
//
// THE DATASET IS VALIDATED BEFORE IT IS INTERPOLATED, and here that guard is doing more work than it
// does elsewhere in this package. Everywhere else `datasetPattern` keeps a name safe in an ARGV;
// this call site puts it inside a double-quoted assignment in a SHELL SCRIPT THE OPERATOR WILL RUN
// AS ROOT ON ANOTHER MACHINE. A name containing `"` would close the quote and everything after it
// would be script — so the pattern's exclusion of quotes, spaces and metacharacters is what makes
// this function safe to exist at all, rather than a tidiness check.
func RenderZFSHelper(parent string) (string, error) {
	if !datasetPattern.MatchString(parent) {
		return "", fmt.Errorf("storage: invalid dataset name %q", parent)
	}
	if !strings.Contains(zfsHelperScript, zfsHelperPlaceholder) {
		return "", ErrHelperPlaceholder
	}
	// Exactly one replacement: the placeholder appears once, and a second occurrence would mean the
	// script grew a shape this function does not understand.
	return strings.Replace(zfsHelperScript, zfsHelperPlaceholder, `PARENT="`+parent+`"`, 1), nil
}
