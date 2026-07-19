import type { Job } from "@/lib/types";
import { groupByIntent } from "./groupByIntent";
import { humanJobState } from "./state";
import { formatRelativeTime } from "@/lib/format";
import { Badge } from "@/components/ui/badge";

function attemptTone(state: Job["state"]): "ok" | "danger" | "neutral" {
  if (state === "succeeded") return "ok";
  if (state === "failed" || state === "connection_lost") return "danger";
  return "neutral";
}

export function JobHistory({ jobs }: { jobs: Job[] }) {
  const groups = groupByIntent(jobs);
  if (groups.length === 0) {
    return <div className="text-sm text-muted">No backups yet for this device.</div>;
  }
  return (
    <div className="flex flex-col gap-2">
      {groups.map((g) => (
        <div key={g.intentId} className="rounded-card border border-line bg-card p-4">
          <div className="flex items-center justify-between">
            <div className="text-sm font-medium">{g.summary}</div>
            <span className="font-mono text-xs text-subtle">
              {formatRelativeTime(g.latest.started_at)}
            </span>
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
