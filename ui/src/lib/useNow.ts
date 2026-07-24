import { useSyncExternalStore } from "react";

// A single shared clock so every relative-time label advances live WITHOUT each one owning a timer.
// One interval runs while at least one consumer is mounted; it stops when the last unmounts.
const TICK_MS = 15_000; // fine enough to catch a minute rollover promptly; cheap for older labels

let now = Date.now();
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setInterval> | null = null;

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  if (!timer) {
    timer = setInterval(() => {
      now = Date.now();
      for (const l of listeners) l();
    }, TICK_MS);
  }
  return () => {
    listeners.delete(cb);
    if (listeners.size === 0 && timer) {
      clearInterval(timer);
      timer = null;
    }
  };
}

// useNow returns a timestamp that advances every TICK_MS while mounted, so components re-render and
// their relative-time strings stay current. Same value across all consumers (one clock).
export function useNow(): number {
  return useSyncExternalStore(subscribe, () => now, () => now);
}
