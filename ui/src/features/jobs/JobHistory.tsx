import { useState } from "react";
import type { Job } from "@/lib/types";
import { groupByIntent, type IntentGroup } from "./groupByIntent";
import { humanJobState } from "./state";
import { RelativeTime } from "@/components/RelativeTime";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

function attemptTone(state: Job["state"]): "ok" | "danger" | "neutral" {
  if (state === "succeeded") return "ok";
  if (state === "failed" || state === "connection_lost") return "danger";
  return "neutral";
}

// needsAttention marks a group whose latest attempt failed — the assisted model's one-tap retry
// point (stack D13): a failed→retried→succeeded night reads as one operation, not a wall of red.
function needsAttention(latest: Job): boolean {
  return latest.state === "failed" || latest.state === "connection_lost";
}

// latestIntentId names the intent that owns the Retry: the one whose latest attempt STARTED most
// recently. Deliberately NOT "the first row" (quince#813). The rows are now ordered by the instant
// each one displays, which for a terminal group is its finish — and DeviceCard.tsx:70 picks its
// needs-attention job by started_at. Two overlapping intents can rank differently under those two
// keys, so reading the Retry off the display order would have silently moved the button away from
// the attempt the device card is pointing at. Two different questions, two keys, stated separately.
function latestIntentId(groups: IntentGroup[]): string {
  return groups.reduce((best, g) => (g.latest.started_at > best.latest.started_at ? g : best)).intentId;
}

// JobHistory groups a device's backups by intent (contracts §2 UI contract; rows are ordered by the
// instant each displays — see groupByIntent).
// onRetry, when given, renders a one-tap Retry — but ONLY on the LATEST intent when its latest attempt
// failed. Retrying an OLD failed intent is just "back up now" with extra confusion, and it would match
// the device card, which surfaces needs-attention for the newest attempt only (finding #6). Older
// failures stay in the history as record, without a Retry.
// DEFAULT_SHOWN caps the history so a device with many backups doesn't bury the Versions list below
// it (qn.6a soak fix); "Show all" reveals the rest.
const DEFAULT_SHOWN = 3;

export function JobHistory({ jobs, onRetry }: { jobs: Job[]; onRetry?: (latest: Job) => void }) {
  const [expanded, setExpanded] = useState(false);
  const groups = groupByIntent(jobs);
  if (groups.length === 0) {
    return <div className="text-sm text-muted">No backups yet for this device.</div>;
  }
  const retryable = latestIntentId(groups);
  const shown = expanded ? groups : groups.slice(0, DEFAULT_SHOWN);
  return (
    <div className="flex flex-col gap-2">
      {shown.map((g) => (
        <div key={g.intentId} className="rounded-card border border-line bg-card p-4">
          <div className="flex items-center justify-between gap-3">
            <div className="text-sm font-medium">{g.summary}</div>
            <div className="flex items-center gap-2">
              {/* g.at, never a field picked here: the row and the sort must agree on which instant
                  this group happened at (quince#813). "started" is not decoration — without it,
                  "Backing up · 19 minutes ago" pairs a label with an instant it does not describe,
                  which is the reported defect one size smaller. */}
              <span className="flex items-center gap-1 font-mono text-xs text-subtle">
                {g.atIsStart ? <span>started</span> : null}
                <RelativeTime iso={g.at} />
              </span>
              {g.intentId === retryable && onRetry && needsAttention(g.latest) ? (
                <Button size="sm" variant="outline" onClick={() => onRetry(g.latest)} data-testid="retry-backup">
                  Retry
                </Button>
              ) : null}
            </div>
          </div>
          {g.attempts.length > 1 ? (
            <div className="mt-2 flex flex-wrap gap-1">
              {g.attempts.map((a) => (
                <Badge key={a.id} tone={attemptTone(a.state)}>
                  #{a.attempt} {humanJobState(a.state)}
                </Badge>
              ))}
            </div>
          ) : null}
        </div>
      ))}
      {groups.length > DEFAULT_SHOWN ? (
        <Button
          variant="ghost"
          size="sm"
          className="self-start"
          onClick={() => setExpanded((e) => !e)}
          data-testid="history-toggle"
        >
          {expanded ? "Show less" : `Show all ${groups.length}`}
        </Button>
      ) : null}
    </div>
  );
}
