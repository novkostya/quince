import { formatDateTime, formatRelativeTime } from "@/lib/format";
import { useNow } from "@/lib/useNow";

// RelativeTime renders a live-updating "2 hours ago" that advances on its own (shared clock, useNow)
// and shows the exact local date-time on hover (qn.6a). Use it anywhere a timestamp is displayed.
export function RelativeTime({ iso, className }: { iso: string; className?: string }) {
  const now = useNow();
  if (!iso) return <span className={className}>—</span>;
  return (
    <time dateTime={iso} title={formatDateTime(iso)} className={className}>
      {formatRelativeTime(iso, now)}
    </time>
  );
}
