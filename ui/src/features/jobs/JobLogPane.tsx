import { useEffect, useRef } from "react";
import { useShallow } from "zustand/react/shallow";
import { useJobsStore } from "@/stores/jobs";

// A tailing log pane. Auto-scrolls to the bottom as new chunks arrive.
export function JobLogPane({ jobId }: { jobId: string }) {
  const lines = useJobsStore(useShallow((s) => s.logByJobId[jobId] ?? []));
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);

  if (lines.length === 0) return null;

  return (
    <div
      ref={ref}
      data-testid="job-log"
      className="max-h-56 overflow-auto rounded-card border border-line bg-bg p-3 font-mono text-xs text-muted"
    >
      {lines.map((line, i) => (
        <div key={i} className="whitespace-pre-wrap">
          {line}
        </div>
      ))}
    </div>
  );
}
