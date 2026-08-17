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

// received renders how much has arrived so far. `bytes_done` is CUMULATIVE and monotonic as of
// quince#808 — the engine banks each finished batch — so it is finally a fact about the job rather
// than about whichever protocol message happened to be in flight.
//
// NO DENOMINATOR, and that is the protocol's limit rather than a UI choice: every total
// idevicebackup2 exposes is per-message (`backup_total_size` is item 3 of the current
// DLMessageUploadFiles), so `bytes_total` is now 0 = unknown and there is nothing honest to divide
// by. A bare rising figure says exactly what is known.
//
// SHOWN AT ALL, AND ONLY HERE. Three Operator rulings, and all three are needed to land where this
// is — the shape is worth keeping because each reversal was for a different reason:
//
//   2026-08-16 — the first draft DELETED this as dishonest. Reversed: it is the only figure that
//     MOVES while one large item transfers, so removing it deepened the "seems stuck" reading the
//     work exists to fix. A 2.68 GB batch once ran 3m20s while the overall percent sat at 1.
//     Do not delete it again on purity grounds.
//   2026-08-17 — it came OFF the card. Beside a whole-job percentage it read as a second,
//     contradictory progress, and the extra row made the card taller than its neighbours.
//   2026-08-17 — the pair became a single cumulative figure, once the numbers themselves were
//     fixed rather than merely presented more carefully.
function received(job: Job): string | null {
  // NOT WHILE FINISHING, AND NOT ONCE TERMINAL. The field is never cleared, so it would sit frozen
  // beside "Finishing up" and read as a transfer still running. Nothing is transferring then: the
  // tool is moving and removing files, and quince has verify and commit to do.
  if (isFinishingUp(job) || isTerminalJob(job)) return null;
  const { bytes_done } = job.progress;
  if (bytes_done <= 0) return null;
  return `${formatBytes(bytes_done)} received`;
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
// THE CARD CARRIES THE RECEIVED FIGURE AGAIN, and the round trip is worth recording because it was
// removed deliberately and is back for a reason rather than by drift:
//
//   2026-08-17, REMOVED — as a per-batch PAIR it read as a second, contradictory progress beside
//     the percentage ("57.3 MB / 2.7 GB" next to "1%"), and it occupied a whole extra row, which
//     made this card taller than its neighbours in the grid.
//   2026-08-17, RESTORED (Operator) — quince#808 made the figure cumulative with no denominator, so
//     it can no longer contradict the percent; and it now shares the label's row, so the height
//     objection does not apply either. Both reasons for removing it were FIXED, not overruled.
//
// ORDER IS THE OVERFLOW POLICY. `truncate` cuts from the end, so on a narrow phone the received
// figure is dropped first, then the clock, and the state label — the one thing that must always be
// readable — survives longest. That is the priority anyone would choose, and it is why all three
// share one truncating span instead of sitting in separate columns.
//
// The percentage is still omitted entirely when there is none: a "—" where a number goes is noise,
// and the indeterminate bar already says it.
export function JobProgressInline({ job }: { job: Job }) {
  const note = useJobNote(job, "card");
  const pct = displayPercent(job);
  const elapsed = useElapsedLabel(job);
  const transferred = received(job);
  return (
    <div>
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="min-w-0 truncate font-medium text-fg">
          {jobStatusLabel(job)}
          {elapsed ? (
            <span className="ml-2 font-mono font-normal tabular-nums text-subtle">{elapsed}</span>
          ) : null}
          {transferred ? (
            <span className="ml-2 font-mono font-normal tabular-nums text-subtle">
              · {transferred}
            </span>
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
  const transferred = received(job);
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
      {/* THIS is where the received figure lives — the page you open when you suspect a stall.
          It carries its own word rather than competing with the percentage above it: on 2026-08-17
          a 2.68 GB batch ran 3m20s while the overall percent stayed at 1, and this figure was the
          only true motion on screen. It is now cumulative, so it also never falls. */}
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-xs tabular-nums text-subtle">
        {elapsed ? <span>{elapsed}</span> : null}
        {transferred ? <span>{transferred}</span> : null}
      </div>
      {note ? <div className={`mt-2 text-xs ${noteClass(note)}`}>{note.text}</div> : null}
    </div>
  );
}
