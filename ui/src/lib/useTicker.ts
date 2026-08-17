import { useEffect, useState } from "react";
import { useConnectionStore } from "@/stores/connection";

// A PER-SECOND clock, for the one thing that needs one: the elapsed time on a running job.
//
// Deliberately NOT `useNow` (lib/useNow.ts). That clock ticks every 15 s, which is right for
// "2 hours ago" labels and wrong here — an elapsed counter that jumps 2m30s → 2m45s reads as a
// stuck widget, which is the failure this whole change exists to remove. A separate hook rather
// than a faster shared clock, so nothing else pays for the extra renders.
//
// `active` gates the interval: a terminal job's duration is fixed, so nothing should tick for it.
// When it goes false the last value is kept rather than reset — the caller is showing a frozen
// duration at that point, not a live one.
export function useTicker(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    // Set immediately as well as on the interval: a card that mounts mid-job would otherwise show
    // the mount-time value for a full second before the first tick.
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [active]);
  return now;
}

// elapsedSeconds is the live age of a job, or null when it cannot be computed. Null rather than 0
// on a bad timestamp: 0 is a measurement, and "just started" is not what an unparseable date means.
export function elapsedSeconds(startedAt: string, now: number): number | null {
  const t = Date.parse(startedAt);
  if (Number.isNaN(t)) return null;
  return Math.max(0, Math.floor((now - t) / 1000));
}

// The server's clock, as seen through this browser.
//
// EVERY duration on screen is a server timestamp subtracted from a browser clock, so a viewer with
// a wrong clock sees wrong durations — and the failure is silent and total. Measured on a real
// phone, 2026-08-17: "Set Automatically" was off, the device had drifted 26 s ahead, and a backup
// that had just started rendered "26s" the moment it appeared, consistently, for every attempt.
//
// Correcting at the point of USE rather than at the point of parse, because `started_at` is a fact
// about the server and must stay one; only the comparison needs the offset.
export function useServerNow(active: boolean): number {
  const now = useTicker(active);
  const offset = useConnectionStore((s) => s.serverOffsetMs);
  return now + offset;
}
