import { cn } from "@/lib/cn";

// A plain token-styled progress bar. percent null → an indeterminate slim track (Apple's
// protocol goes silent for minutes; we never fake motion, we narrate — see JobProgress).
export function Progress({ percent, className }: { percent: number | null; className?: string }) {
  const clamped = percent === null ? 0 : Math.max(0, Math.min(100, percent));
  return (
    // role="progressbar" so the bar is addressable — to a screen reader, and to a gate. qn.6d's
    // G1b asks e2e to prove a storage card renders a FILL BAR, and until this role existed there
    // was no way to assert the bar itself: only the percentage text beside it, which is a
    // different claim and would still pass with the bar deleted.
    //
    // aria-valuenow is OMITTED when percent is null — this component's indeterminate state, and
    // the one it exists for, because Apple's protocol goes silent for minutes and we narrate
    // rather than fake motion. An indeterminate bar reporting 0 would be a measurement, and a
    // wrong one.
    <div
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percent === null ? undefined : clamped}
      className={cn("h-1.5 w-full overflow-hidden rounded-full bg-elevated", className)}
    >
      <div
        className="h-full rounded-full bg-accent transition-[width] duration-500"
        style={{ width: `${clamped}%` }}
      />
    </div>
  );
}
