import type { Job } from "@/lib/types";
import { Progress } from "@/components/ui/progress";
import { formatBytes, formatPercent } from "@/lib/format";
import { humanJobState, livenessNote } from "./state";

// The seeding note is informational (a clone is running), not a caution, so it renders muted rather
// than amber; every other note (silent/stall/passcode) stays a warn tone.
function noteClass(job: Job): string {
  return job.state === "seeding" ? "text-muted" : "text-warn";
}

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
      {note ? <div className={`mt-1.5 text-xs ${noteClass(job)}`}>{note}</div> : null}
    </div>
  );
}

// currentFile renders the tool's per-file transfer honestly — it is the CURRENT file's bytes, not the
// backup total (idevicebackup2 gives no reliable upfront backup-byte total), so it is labelled as such
// and only shown while there is a live transfer (gate-11 finding #10, (cj)). Overall progress is the
// percent + files count above it.
function currentFile(job: Job): string | null {
  const { bytes_done, bytes_total } = job.progress;
  if (bytes_total <= 0) return null;
  return `current file ${formatBytes(bytes_done)} / ${formatBytes(bytes_total)}`;
}

// Full progress panel for the device-details page.
export function JobProgressFull({ job }: { job: Job }) {
  const note = livenessNote(job);
  const file = currentFile(job);
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
        <span>{job.progress.files_received} files</span>
        {file ? <span>{file}</span> : null}
        <span>{job.progress.phase}</span>
      </div>
      {note ? <div className={`mt-2 text-xs ${noteClass(job)}`}>{note}</div> : null}
    </div>
  );
}
