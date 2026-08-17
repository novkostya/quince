import type { Job } from "@/lib/types";
import { Progress } from "@/components/ui/progress";
import { formatBytes, formatDuration, formatPercent } from "@/lib/format";
import { elapsedSeconds, useServerNow } from "@/lib/useTicker";
import { displayPercent, isFinishingUp, isTerminalJob, jobStatusLabel } from "./state";
import { useJobNote, type JobNote } from "./useJobNote";

// The tone travels with the note now (see useJobNote): a routine wait renders muted, and only a
// caution the user may need to act on gets the warn colour. Deriving it here from `state`/`phase`
// could not express "this stall is normal" versus "this silence has gone on too long", which are
// the same phase and different messages.
function noteClass(note: JobNote): string {
  return note.tone === "muted" ? "text-muted" : "text-warn";
}

// currentTransfer renders the tool's own size pair. It is NOT whole-backup progress and never was:
// `backup_total_size` is item 3 of the CURRENT DLMessageUploadFiles message and `backup_real_size`
// is a local reset per message (idevicebackup2.c:1017, tag 1.4.0), so the pair describes one batch.
// Measured 2026-08-16, it fell 20 times in one backup — worst 2,684,354,560 → 73,216 (quince#808).
//
// SHOWN ANYWAY, BUT ONLY HERE AND ONLY WHILE A TRANSFER IS RUNNING. Two Operator rulings, a day
// apart, and both are needed to land where this is:
//
//   2026-08-16 — the first draft DELETED it as dishonest. Reversed: it is the only figure that
//     MOVES while one large item transfers, so removing it would have deepened the "seems stuck"
//     reading this whole change exists to fix. Measured next day: a 2.68 GB batch ran 3m20s while
//     the overall percent sat at 1. Do not delete it again on purity grounds.
//   2026-08-17 — it came OFF the card. Beside a whole-job percentage it reads as a second,
//     contradictory progress ("57.3 MB / 2.7 GB" next to "1%"), and the extra row made the card
//     taller than its neighbours in the grid.
//
// The defect was never the number; it was presenting a per-batch figure as if it were the job's,
// and leaving it on screen after the batch it describes has ended.
function currentTransfer(job: Job): string | null {
  // NOT WHILE FINISHING, AND NOT ONCE TERMINAL (Operator, 2026-08-17: "super confusing").
  // These fields are never cleared, so the last batch's pair sits there frozen — "2.0 GB / 2.1 GB"
  // beside "Finishing up", which reads as a transfer still running at 95%. Nothing is
  // transferring: the tool is moving and removing files, and quince has verify and commit to do.
  //
  // Same rule as `displayPercent` withholding the 100: a figure that was true of a finished step
  // is not a claim about the current one. The elapsed clock keeps the surface alive instead.
  if (isFinishingUp(job) || isTerminalJob(job)) return null;
  const { bytes_done, bytes_total } = job.progress;
  if (bytes_total <= 0) return null;
  return `${formatBytes(bytes_done)} / ${formatBytes(bytes_total)}`;
}

// The live age of a running job. This is the honest motion for every window where quince has no
// percentage to show: it is derived from `started_at`, which is already on the wire, so it needs
// nothing from the tool and cannot be wrong about what it claims.
function useElapsedLabel(job: Job): string | null {
  const running = !isTerminalJob(job);
  const now = useServerNow(running);
  if (!running) return null;
  const secs = elapsedSeconds(job.started_at, now);
  return secs === null ? null : formatDuration(secs);
}

// Inline mini-progress for the dashboard device card.
//
// THE CARD CARRIES NO PER-TRANSFER FIGURE, and that is a reversal of how this shipped first
// (Operator, 2026-08-17, after using it on a phone). The size pair is per-batch, so beside a
// whole-job percentage it reads as a second, contradictory progress — "57.3 MB / 2.7 GB" next to
// "1%". It belongs on the details page, which is where you go when you suspect a stall anyway.
//
// Height is the other half of the ruling: these cards sit in a grid, so an extra row here makes
// one card taller than its neighbours and the row ragged. Elapsed therefore shares the label's
// row rather than taking its own, and the percentage is omitted entirely when there is none —
// a "—" where a number goes is noise, and the indeterminate bar already says it.
export function JobProgressInline({ job }: { job: Job }) {
  const note = useJobNote(job, "card");
  const pct = displayPercent(job);
  const elapsed = useElapsedLabel(job);
  return (
    <div>
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="min-w-0 truncate font-medium text-fg">
          {jobStatusLabel(job)}
          {elapsed ? (
            <span className="ml-2 font-mono font-normal tabular-nums text-subtle">{elapsed}</span>
          ) : null}
        </span>
        {pct === null ? null : (
          <span className="shrink-0 font-mono tabular-nums text-muted">{formatPercent(pct)}</span>
        )}
      </div>
      <Progress percent={pct} className="mt-1.5" />
      {note ? <div className={`mt-1.5 text-xs ${noteClass(note)}`}>{note.text}</div> : null}
    </div>
  );
}

// Full progress panel for the device-details page.
export function JobProgressFull({ job }: { job: Job }) {
  const note = useJobNote(job, "full");
  const pct = displayPercent(job);
  const elapsed = useElapsedLabel(job);
  const transfer = currentTransfer(job);
  return (
    <div className="rounded-card border border-line bg-card p-5">
      <div className="flex items-center justify-between">
        <div className="text-sm font-semibold">{jobStatusLabel(job)}</div>
        {/* Omitted when there is none, matching the card. A "—" where a number goes is noise, and
            it appeared in exactly the state the Operator called confusing: "Finishing up" with a
            dash, a full-looking bar and a stale byte pair, all saying different things. */}
        {pct === null ? null : (
          <div className="font-mono text-sm tabular-nums text-muted">{formatPercent(pct)}</div>
        )}
      </div>
      <Progress percent={pct} className="mt-3" />
      {/* THIS is where the per-transfer figure lives — the page you open when you suspect a stall.
          Labelled, because unlabelled it competes with the percentage above it: on 2026-08-17 a
          2.68 GB batch ran for 3m20s while the overall percent stayed at 1, and the pair climbing
          from 57 MB to 1.7 GB was the only true motion on screen. */}
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-xs tabular-nums text-subtle">
        {elapsed ? <span>{elapsed}</span> : null}
        {transfer ? <span>current transfer {transfer}</span> : null}
      </div>
      {note ? <div className={`mt-2 text-xs ${noteClass(note)}`}>{note.text}</div> : null}
    </div>
  );
}
