import { useReconciling } from "@/lib/health";

// qn.6i — WHILE QUINCE IS RE-READING ITS STORAGES, IT SAYS SO.
//
// `no silent caps or fallbacks` names the UI explicitly: degraded modes are surfaced in the UI **and**
// the logs. A version list that quince knows may be SHORT is a degraded mode, and until this rung it
// could not be one — reconciliation finished before anything was served, so every list was complete
// by the time a user saw it. Making it asynchronous is what created the state; this is the half that
// keeps it honest (contracts §1, Operator ruling 2026-08-08 on quince#731 blocker 2; in scope for
// this rung by the ruling at spec review, quince#769).
//
// IT RENDERS NOTHING WHEN FALSE, which is almost always. A permanent element that merely changes
// wording would train a user to stop reading it, and this sentence is only worth anything on the
// occasions it appears.
//
// THE WORDING SAYS WHAT MAY BE WRONG, NOT WHAT THE DAEMON IS DOING. "Reconciling storages" is a
// status; "some backups may not be listed yet" is the thing a user can act on — it is what stops
// them concluding a disk is empty. The distinction is contracts §1's in as many words: a declared
// provisional state, not an empty result.
//
// NOT A SPINNER AND NOT BLOCKING. Everything on the page works; a subset of it may be incomplete for
// a few seconds. Disabling the UI over that would trade a small honesty problem for a large
// availability one, which is the trade the ruling refused when it rejected "refuse until reconciled".
export function ReconcilingNotice() {
  if (!useReconciling()) return null;
  return (
    <div
      // `status`, not `alert`: a screen reader should hear this in turn rather than have its user
      // interrupted. Nothing is wrong — a list is merely still filling in.
      // THE PROJECT'S TOKENS, NOT shadcn's DEFAULTS. This shipped as
      // `rounded-md border-border bg-muted text-muted-foreground` and rendered as a pale slab against
      // the dark cards — reported from a real screen.
      //
      // `border-border` and `text-muted-foreground` are not in `index.css`, so Tailwind emitted
      // nothing for them: no border colour, no text colour. **`bg-muted` is the dangerous one** — it
      // IS defined, as `--color-muted: var(--fg-muted)`, a FOREGROUND colour. Used as a background it
      // fills the box with the muted TEXT colour, which is why the result was light grey rather than
      // simply unstyled. A name that exists and means something else fails LOUDLY; the other two
      // failed silently.
      //
      // The house surface is `rounded-card border border-line bg-card` — `card.tsx`, `dialog.tsx`,
      // `JobHistory` and `VersionList` all use it. Matching it is what makes this read as part of the
      // page rather than as something pasted on top of it.
      role="status"
      className="mb-4 rounded-card border border-line bg-card px-3 py-2 text-sm text-muted"
    >
      Checking storages for backups — <strong>some backups may not be listed yet.</strong> This
      clears on its own.
    </div>
  );
}
