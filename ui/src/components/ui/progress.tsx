import { cn } from "@/lib/cn";

// A plain token-styled progress bar. percent null → an indeterminate slim track (Apple's
// protocol goes silent for minutes; we never fake motion, we narrate — see JobProgress).
export function Progress({ percent, className }: { percent: number | null; className?: string }) {
  const clamped = percent === null ? 0 : Math.max(0, Math.min(100, percent));
  return (
    <div className={cn("h-1.5 w-full overflow-hidden rounded-full bg-elevated", className)}>
      <div
        className="h-full rounded-full bg-accent transition-[width] duration-500"
        style={{ width: `${clamped}%` }}
      />
    </div>
  );
}
