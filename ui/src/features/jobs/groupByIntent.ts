import type { Job } from "@/lib/types";
import { humanJobState } from "./state";

export interface IntentGroup {
  intentId: string;
  attempts: Job[]; // ordered by attempt ascending
  latest: Job;
  summary: string;
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
    groups.push({ intentId, attempts, latest, summary: summarize(attempts, latest) });
  }
  groups.sort((a, b) => (a.latest.started_at < b.latest.started_at ? 1 : -1));
  return groups;
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
