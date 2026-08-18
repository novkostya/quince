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
// SHOWN AT ALL, AND ONLY HERE. Four Operator rulings, and every one is needed to land where this
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
//   2026-08-18 — the WORD came off the card, and the figure joined the other numbers in a
//     right-aligned cluster the same day (quince#1228). On the card every other
//     token is data (`11s · 1.87 GB` beside `52%`) and `received` was the one English word in a
//     monospace data run — Operator: *"I don't quite like long received word on this card."* The
//     full panel KEEPS the word at its own call site: with no denominator on the wire, `received`
//     is what stops a bare figure reading as the backup's total size, and that panel is the
//     stall-investigation page where the label earns its room. So this function now returns the
//     bare figure and the caller that wants the word attaches it.
function received(job: Job): string | null {
  // ONLY WHILE BYTES CAN STILL ARRIVE. `backing_up` is the one state where they can; `verifying`,
  // `committing` and every terminal state describe work that happens AFTER the transfer, and this
  // field is never cleared — so it would sit frozen beside them and read as a transfer still
  // running. Written as one positive condition rather than a list of exclusions, so a state added
  // later is silently EXCLUDED rather than silently included.
  if (job.state !== "backing_up" || isFinishingUp(job)) return null;
  const { bytes_done } = job.progress;
  if (bytes_done <= 0) return null;
  // TWO DECIMALS, which is legibility rather than precision theatre (Operator, 2026-08-17). The
  // underlying figure updates up to twice a second, but at GB scale ONE decimal only changes every
  // few seconds — so the number sat still while gigabytes were arriving, which is the exact
  // impression this line exists to dispel.
  return formatBytes(bytes_done, 2);
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
// THE OVERFLOW POLICY IS THE SPLIT ITSELF (quince#1228). The label is the only prose on the row and
// the only thing allowed to truncate; the numbers — clock, received figure, percent — are a
// `shrink-0` cluster on the right that can never clip. Earlier shapes had all four in one
// truncating span and chose which token the ellipsis ate first; splitting by KIND removes the
// question, because a clipped word reads as a cut corner and a clipped digit reads as a bug.
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
        {/* THE LABEL ALONE TRUNCATES; EVERY NUMBER SITS IN A `shrink-0` CLUSTER ON THE RIGHT
            (quince#1228, Operator: *"align all numbers together to the right"*). This also retires
            the digit-clipping trade recorded here earlier the same day: when the numbers shared the
            label's truncating span, a narrow card clipped them mid-number — now nothing numeric can
            clip, and the label, the one token that is prose, is the one that gives way.

            NO `·` INSIDE THE CLUSTER: the gap separates mono tokens on its own, and the Operator's
            sketch — `12s 1.87GB 52%` — has plain spaces. The percent keeps the stronger tone; it is
            the figure derived from the tool's own progress, where the other two are quince's.  */}
        <span className="min-w-0 truncate font-medium text-fg">{jobStatusLabel(job)}</span>
        {elapsed || transferred || pct !== null ? (
          <span className="flex shrink-0 items-center gap-2 font-mono tabular-nums">
            {elapsed ? <span className="text-subtle">{elapsed}</span> : null}
            {transferred ? <span className="text-subtle">{transferred}</span> : null}
            {pct === null ? null : <span className="text-muted">{formatPercent(pct)}</span>}
          </span>
        ) : null}
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
        {transferred ? <span>{transferred} received</span> : null}
      </div>
      {note ? <div className={`mt-2 text-xs ${noteClass(note)}`}>{note.text}</div> : null}
    </div>
  );
}
