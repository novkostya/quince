import { useState } from "react";
import type { Job } from "@/lib/types";
import { groupByIntent } from "./groupByIntent";
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

// JobHistory groups a device's backups by intent (contracts §2 UI contract; groups are newest-first).
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
  const shown = expanded ? groups : groups.slice(0, DEFAULT_SHOWN);
  return (
    <div className="flex flex-col gap-2">
      {shown.map((g, i) => (
        <div key={g.intentId} className="rounded-card border border-line bg-card p-4">
          <div className="flex items-center justify-between gap-3">
            <div className="text-sm font-medium">{g.summary}</div>
            <div className="flex items-center gap-2">
              <RelativeTime iso={g.latest.started_at} className="font-mono text-xs text-subtle" />
              {i === 0 && onRetry && needsAttention(g.latest) ? (
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
