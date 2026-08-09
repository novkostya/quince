package storage

import (
	"context"
	"strings"
	"time"
)

// HookOutcome is what a `Test helper` press learned. FOUR states, not ok/failed, because the
// remedies differ and a user cannot guess between them: a missing helper, an un-migrated helper and
// a mistyped parent dataset all present as "it did not work".
type HookOutcome string

const (
	// HookOK — both verbs answered. The key, the forced command and the parent all line up.
	HookOK HookOutcome = "ok"
	// HookNotMigrated — `capacity` was refused and `list` answered, which is exactly an operator
	// who installed the helper before qn.6d added the `capacity)` arm. Storage cards will read
	// "free space unavailable" until they add it; nothing else breaks.
	HookNotMigrated HookOutcome = "not_migrated"
	// HookParentMismatch — `capacity` answered and `list <typed parent>` was refused. The helper
	// works; the parent dataset typed into the form is not the one baked into it.
	//
	// THIS IS THE STATE DERIVATION WOULD NOT HAVE CAUGHT. Reading the dataset off the filesystem
	// proves what the FILESYSTEM thinks; only the helper can say what the HELPER was configured
	// with, and a disagreement between them is silent until a backup fails at commit.
	HookParentMismatch HookOutcome = "parent_mismatch"
	// HookUnreachable — neither verb answered, or there was no transport binary at all. WHAT
	// PRODUCES IT IS ENUMERATED ONCE, in hookUnreachableCauses below; this comment deliberately
	// does not restate the list.
	//
	// It restated it until quince#799, and the copy went stale: this line and the user-facing
	// remedy both named the key, the forced command and the host, and both omitted the host key —
	// which quince#796 then measured as the thing that actually produced this outcome on a rig
	// where all three of the named causes were fine. Two descriptions of one set, drifting because
	// nothing tied them together, is quince#782's shape.
	HookUnreachable HookOutcome = "unreachable"
)

// hookUnreachableCauses is the ONE enumeration of what makes the helper unreachable, so the doc
// comment above and the Reason the user reads cannot disagree again.
//
// THE HOST KEY WAS THE MISSING ONE, and it is the first thing a new install hits. Measured on a lab
// rig (quince#796): with a correct key, a correct forced command, a reachable host and a correct
// parent dataset, a `hook_cmd` carrying no host-key options answered `unreachable` with `Host key
// verification failed.` A container's known_hosts is empty at first install — which is exactly when
// an operator fills this field in — so the remedy was pointing at the three things that were
// already right.
//
// THE MECHANISM IS DELIBERATELY NOT ASSERTED. The documented `hook_cmd` carries `BatchMode=yes`,
// under which ssh cannot prompt and fails outright; an operator who omits it and has no tty fails
// too, by a different route. Naming the CAUSE is what a reader needs, and which route they took is
// the one thing here nobody has measured across both shapes.
const hookUnreachableCauses = "the key, the forced command in authorized_keys, the host key in " +
	"known_hosts, and that the host is up"

// HookCheck is the verdict plus what the transport actually said.
type HookCheck struct {
	Outcome HookOutcome
	// Reason is quince's own sentence, safe to render anywhere.
	Reason string
	// Detail is the TRANSPORT'S OWN OUTPUT, verbatim, and it is what makes a failure diagnosable —
	// ssh's "Permission denied (publickey)" is the whole answer to why a key does not work.
	//
	// IT MAY NAME THE OPERATOR'S HOST, so it is subject to one rule: it is shown to the
	// authenticated admin in their own browser, and it must NEVER be logged, put in a fixture, or
	// pasted into a PR or an issue. That is the privacy gate's actual scope — committed files,
	// commit messages and forge text — rather than a redaction rule on a running product, and
	// blanking it here would remove the only diagnostic the user has for their own machine.
	//
	// THE ARGV IS NEVER INCLUDED. `hook_cmd` carries `user@host` directly; the transport's output
	// may mention a host, but the command line always does.
	Detail string
}

// hookCheckTimeout bounds one press of `Test helper`.
//
// An unreachable host is the ORDINARY failing case here — that is what the button is for — and ssh's
// own connect timeout can be minutes. A form that hangs that long reads as broken quince rather than
// as an unreachable host, which inverts what the check is telling you.
const hookCheckTimeout = 20 * time.Second

// CheckHook fires the constrained helper's two read-only verbs and classifies the result.
//
// WHY THIS EXISTS AT ALL: without it, "did I install the helper correctly?" is answered by a failed
// multi-hour Wi-Fi transfer at commit time. The helper's key, its forced command and its baked-in
// $PARENT are three things that must line up and none of them is observable from the path.
//
// THE ORDER IS PART OF THE ANSWER (qn.6e rung-ruled decision 6). `capacity` takes NO caller argument
// — deploy/storage.md's own comment calls that "TIGHTER than the arms above" — so a failure there is
// unambiguously about reachability. Running it first means `list`'s failure can only be about the
// parent. Reversed, a single refusal would be two hypotheses.
//
// BOTH VERBS ARE READ-ONLY AND PATH-GUARDED, which is what makes this safe to fire from a form.
// `capacity` runs `zfs list -H -p -o used,available $PARENT`; `list` runs `zfs list -t snapshot -H
// -o name -r <target>` behind a `case "$target" in "$PARENT"|"$PARENT"/*` guard. Nothing here can
// create, destroy or write.
func CheckHook(ctx context.Context, parentDataset, hookCmd string) HookCheck {
	if strings.TrimSpace(hookCmd) == "" {
		return HookCheck{Outcome: HookUnreachable, Reason: "no hook command is configured"}
	}
	if !datasetPattern.MatchString(parentDataset) {
		return HookCheck{Outcome: HookUnreachable,
			Reason: "that is not a valid ZFS dataset name, so quince did not run anything"}
	}

	ctx, cancel := context.WithTimeout(ctx, hookCheckTimeout)
	defer cancel()

	cli := newZFSCLI(parentDataset, "hook", hookCmd, "")

	capOut, capErr := cli.run(ctx, cli.argv("capacity"))
	listOut, listErr := cli.run(ctx, cli.argv("list", parentDataset))

	switch {
	case capErr == nil && listErr == nil:
		// EMPTY `list` OUTPUT IS SUCCESS, and getting this backwards fails on first run and ONLY on
		// first run. `list` returns the @quince-* snapshots under the parent, and a storage with no
		// backups yet has none — so the correct, working, freshly-installed case answers exit 0 with
		// nothing on stdout. Only the error matters here; the output is never inspected for content.
		return HookCheck{Outcome: HookOK,
			Reason: "the helper answered and its parent dataset matches — quince can snapshot here",
			Detail: strings.TrimSpace(capOut)}

	case capErr != nil && listErr == nil:
		return HookCheck{Outcome: HookNotMigrated,
			Reason: "the helper works but has no `capacity` verb — add the `capacity)` case from " +
				"deploy/storage.md, or storage cards will read \"free space unavailable\". " +
				"Backups, commits, snapshots and retention are unaffected.",
			Detail: strings.TrimSpace(capOut)}

	case capErr == nil && listErr != nil:
		return HookCheck{Outcome: HookParentMismatch,
			Reason: "the helper answered, but it refused this parent dataset — the value here does " +
				"not match the $PARENT baked into the helper on the host",
			Detail: strings.TrimSpace(listOut)}

	default:
		return HookCheck{Outcome: HookUnreachable,
			Reason: "quince could not reach the helper — check " + hookUnreachableCauses,
			Detail: strings.TrimSpace(firstNonEmpty(capOut, listOut))}
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
