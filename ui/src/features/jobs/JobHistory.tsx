import type { Job } from "@/lib/types";
import { groupByIntent } from "./groupByIntent";
import { humanJobState } from "./state";
import { formatRelativeTime } from "@/lib/format";
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

// JobHistory groups a device's backups by intent (contracts §2 UI contract). onRetry, when given,
// renders a one-tap Retry on a group whose latest attempt failed — the new attempt inherits the
// intent and folds into this same group.
export function JobHistory({ jobs, onRetry }: { jobs: Job[]; onRetry?: (latest: Job) => void }) {
  const groups = groupByIntent(jobs);
  if (groups.length === 0) {
    return <div className="text-sm text-muted">No backups yet for this device.</div>;
  }
  return (
    <div className="flex flex-col gap-2">
      {groups.map((g) => (
        <div key={g.intentId} className="rounded-card border border-line bg-card p-4">
          <div className="flex items-center justify-between gap-3">
            <div className="text-sm font-medium">{g.summary}</div>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs text-subtle">
                {formatRelativeTime(g.latest.started_at)}
              </span>
              {onRetry && needsAttention(g.latest) ? (
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
    </div>
  );
}
