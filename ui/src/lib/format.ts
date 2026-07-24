// Shared humanizers (ui.design.md §5): numbers are monospace/tabular, units spaced, sizes
// decimal (SI, /1000) to match the design examples ("3.6 GB", "7.5 KB/s"). Pure functions
// so they are trivially unit-tested.

const SIZE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  let v = n;
  let i = 0;
  while (v >= 1000 && i < SIZE_UNITS.length - 1) {
    v /= 1000;
    i += 1;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${SIZE_UNITS[i]}`;
}

export function formatSpeed(bytesPerSec: number): string {
  return `${formatBytes(bytesPerSec)}/s`;
}

export function formatPercent(p: number | null): string {
  if (p === null || !Number.isFinite(p)) return "—";
  return `${Math.round(p)}%`;
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const DIVISIONS: { amount: number; unit: Intl.RelativeTimeFormatUnit }[] = [
  { amount: 60, unit: "second" },
  { amount: 60, unit: "minute" },
  { amount: 24, unit: "hour" },
  { amount: 7, unit: "day" },
  { amount: 4.34524, unit: "week" },
  { amount: 12, unit: "month" },
  { amount: Number.POSITIVE_INFINITY, unit: "year" },
];

// formatRelativeTime turns an RFC3339 timestamp into "2 hours ago". Sub-minute ages collapse to
// "just now" — exact seconds are noise and would churn a live label every tick (qn.6a). Returns "—"
// for empty. Pair with <RelativeTime> (useNow) so the label advances live; the exact time is on hover.
export function formatRelativeTime(iso: string, now: number = Date.now()): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "—";
  let duration = (then - now) / 1000; // seconds, negative = past
  if (Math.abs(duration) < 60) return "just now";
  for (const division of DIVISIONS) {
    if (Math.abs(duration) < division.amount) {
      return rtf.format(Math.round(duration), division.unit);
    }
    duration /= division.amount;
  }
  return "—";
}

const dtf = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

export function formatDateTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : dtf.format(d);
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const s = Math.floor(seconds % 60);
  const m = Math.floor((seconds / 60) % 60);
  const h = Math.floor(seconds / 3600);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}
