import type { Job } from "@/lib/types";
import { humanJobState, isTerminalJob } from "./state";

export interface IntentGroup {
  intentId: string;
  attempts: Job[]; // ordered by attempt ascending
  latest: Job;
  summary: string;
  // `at` is the group's ONE instant: what the row displays AND what the list sorts on. `atIsStart`
  // says which fact it is, because the two cannot be told apart from the string and the renderer
  // must word them differently. See groupInstant.
  at: string;
  atIsStart: boolean;
}

// groupByIntent folds a job list into user-level operations (contracts §2 UI contract):
// a failed→retried→succeeded night renders as one "Backup completed after 1 retry", with
// the individual attempts available for diagnostics.
export function groupByIntent(jobs: Job[]): IntentGroup[] {
  const byIntent = new Map<string, Job[]>();
  for (const j of jobs) {
    const arr = byIntent.get(j.intent_id) ?? [];
    arr.push(j);
    byIntent.set(j.intent_id, arr);
  }

  const groups: IntentGroup[] = [];
  for (const [intentId, attempts] of byIntent) {
    attempts.sort((a, b) => a.attempt - b.attempt);
    const latest = attempts[attempts.length - 1];
    const { at, atIsStart } = groupInstant(attempts, latest);
    groups.push({ intentId, attempts, latest, summary: summarize(attempts, latest), at, atIsStart });
  }
  // Sorted on `at` — the same field the row displays. Keying the order on one instant while
  // labelling from another lets two OVERLAPPING intents render in an order their own visible
  // timestamps contradict (quince#813): a long backup started first and finished second.
  //
  // Lexical compare is chronological here, and that is a measured property rather than a hope:
  // both timestamps reach the client through core/internal/backup/backup.go's
  // `fmtRFC` = t.UTC().Format(time.RFC3339), so every one is second-precision and Z-suffixed.
  // Equal instants return 0 rather than -1 so the comparator is consistent and Array.sort's
  // stability holds: two backups that finish in the same SECOND then keep the order they arrived
  // in, instead of an arbitrary one. Ties were near-impossible while the key was a start time and
  // are merely rare now.
  groups.sort((a, b) => (a.at === b.at ? 0 : a.at < b.at ? 1 : -1));
  return groups;
}

// groupInstant answers "when did this operation happen" with the fact the group's own label
// describes (quince#813, architect ruling): an outcome label dates from the outcome, a progress
// label from the beginning. Getting this wrong is not cosmetic — the error EQUALS the backup's
// duration, so "Backup completed 57 minutes ago" was said of a transfer still running 57 minutes
// ago, on a rig where the true figure was 28.
//
// The start is attempts[0]'s, never `latest`'s: a retried night is ONE operation and it began at
// the first attempt. Reading the last attempt's start discards however long the earlier ones ran.
//
// The fallback — terminal, but no finished_at — should not occur (contracts §2 sets finished_at on
// terminate) and is nullable on the wire, so it is handled rather than asserted. It is NOT silent:
// atIsStart is true, so the row words it as a start and reads oddly instead of lying.
function groupInstant(attempts: Job[], latest: Job): { at: string; atIsStart: boolean } {
  if (isTerminalJob(latest) && latest.finished_at) {
    return { at: latest.finished_at, atIsStart: false };
  }
  return { at: attempts[0].started_at, atIsStart: true };
}

function summarize(attempts: Job[], latest: Job): string {
  const retries = attempts.length - 1;
  const retryText = retries > 0 ? ` after ${retries} ${retries === 1 ? "retry" : "retries"}` : "";
  switch (latest.state) {
    case "succeeded":
      return `Backup completed${retryText}`;
    case "failed":
    case "connection_lost":
      return `Backup needs attention${retryText}`;
    case "cancelled":
      return "Backup cancelled";
    default:
      return `${humanJobState(latest.state)}${retryText}`;
  }
}
