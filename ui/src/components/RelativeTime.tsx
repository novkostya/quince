import { formatDateTime, formatRelativeTime } from "@/lib/format";
import { useNow } from "@/lib/useNow";
import { useConnectionStore } from "@/stores/connection";

// RelativeTime renders a live-updating "2 hours ago" that advances on its own (shared clock, useNow)
// and shows the exact local date-time on hover (qn.6a). Use it anywhere a timestamp is displayed.
//
// CORRECTED FOR THE VIEWER'S CLOCK. Every timestamp here comes from the server, so subtracting a
// browser clock that is wrong makes every one of these labels wrong together, and silently.
// Measured on a real phone, 2026-08-17: 26 s of drift with "Set Automatically" off — enough to age
// a backup that had just started out of "just now". A larger drift puts a server timestamp in the
// future and yields "in 5 minutes" for something that already happened.
//
// `title` deliberately keeps the UNCORRECTED local time: that one is a wall-clock reading for the
// person looking at the screen, not a duration, so it should agree with the clock on their device.
export function RelativeTime({ iso, className }: { iso: string; className?: string }) {
  const offset = useConnectionStore((s) => s.serverOffsetMs);
  const now = useNow() + offset;
  if (!iso) return <span className={className}>—</span>;
  return (
    <time dateTime={iso} title={formatDateTime(iso)} className={className}>
      {formatRelativeTime(iso, now)}
    </time>
  );
}
