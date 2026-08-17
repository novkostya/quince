import { cn } from "@/lib/cn";

// A plain token-styled progress bar. percent null → an INDETERMINATE track: a segment that
// travels, saying "working, and I cannot tell you how far".
//
// This comment promised an indeterminate track from the start and the code rendered a zero-width
// fill — a bar sitting at 0%, indistinguishable from a stalled transfer, which is the exact
// reading quince#376 was filed about. Measured on the lab rig 2026-08-16: 184 s of a 12m42s
// backup rendered that way while the tool moved 263 MB and read 518 MB.
//
// The rule this settles: we never fake a MEASUREMENT, but stillness is its own false claim.
// An animated segment asserts only "something is happening", which is what we actually know.
export function Progress({ percent, className }: { percent: number | null; className?: string }) {
  const indeterminate = percent === null;
  const clamped = indeterminate ? 0 : Math.max(0, Math.min(100, percent));
  return (
    // role="progressbar" so the bar is addressable — to a screen reader, and to a gate. qn.6d's
    // G1b asks e2e to prove a storage card renders a FILL BAR, and until this role existed there
    // was no way to assert the bar itself: only the percentage text beside it, which is a
    // different claim and would still pass with the bar deleted.
    //
    // aria-valuenow is OMITTED when percent is null — this component's indeterminate state, and
    // the one it exists for, because Apple's protocol goes silent for minutes and we narrate
    // rather than fake motion. An indeterminate bar reporting 0 would be a measurement, and a
    // wrong one. data-indeterminate is the assertable counterpart: a gate cannot see an
    // animation, and "no aria-valuenow" is absence rather than evidence.
    <div
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={indeterminate ? undefined : clamped}
      data-indeterminate={indeterminate ? "true" : undefined}
      className={cn("h-1.5 w-full overflow-hidden rounded-full bg-elevated", className)}
    >
      {indeterminate ? (
        // A third of the track, travelling. Width is fixed so the animation can be a pure
        // transform — no layout per frame, and nothing for a slow device to jank on.
        <div className="quince-indeterminate h-full w-1/3 rounded-full bg-accent" />
      ) : (
        <div
          className="h-full rounded-full bg-accent transition-[width] duration-500"
          style={{ width: `${clamped}%` }}
        />
      )}
    </div>
  );
}
