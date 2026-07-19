import type { Job } from "@/lib/types";
import { Progress } from "@/components/ui/progress";
import { formatBytes, formatPercent } from "@/lib/format";
import { humanJobState, livenessNote } from "./state";

// Inline mini-progress for the dashboard device card.
export function JobProgressInline({ job }: { job: Job }) {
  const note = livenessNote(job);
  return (
    <div>
      <div className="flex items-center justify-between text-xs">
        <span className="font-medium text-fg">{humanJobState(job.state)}</span>
        <span className="font-mono tabular-nums text-muted">{formatPercent(job.progress.percent)}</span>
      </div>
      <Progress percent={job.progress.percent} className="mt-1.5" />
      {note ? <div className="mt-1.5 text-xs text-warn">{note}</div> : null}
    </div>
  );
}

// Full progress panel for the device-details page.
export function JobProgressFull({ job }: { job: Job }) {
  const note = livenessNote(job);
  return (
    <div className="rounded-card border border-line bg-card p-5">
      <div className="flex items-center justify-between">
        <div className="text-sm font-semibold">{humanJobState(job.state)}</div>
        <div className="font-mono text-sm tabular-nums text-muted">
          {formatPercent(job.progress.percent)}
        </div>
      </div>
      <Progress percent={job.progress.percent} className="mt-3" />
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-xs tabular-nums text-subtle">
        <span>
          {formatBytes(job.progress.bytes_done)} / {formatBytes(job.progress.bytes_total)}
        </span>
        <span>{job.progress.files_received} files</span>
        <span>{job.progress.phase}</span>
      </div>
      {note ? <div className="mt-2 text-xs text-warn">{note}</div> : null}
    </div>
  );
}
