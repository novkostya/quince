import { useEffect, useState } from "react";
import type { Job } from "@/lib/types";
import { useConnectionStore } from "@/stores/connection";
import { useDevicesStore } from "@/stores/devices";
import { deviceFamily } from "@/features/devices/modelName";
import { useServerNow } from "@/lib/useTicker";
import {
  isPreparing,
  isStalledTransfer,
  isTerminalJob,
  livenessNote,
  preparingNote,
  stalledNote,
} from "./state";

// A note carries its own tone: most of these describe something normal, and colouring a routine
// wait amber teaches the user to distrust the colour.
export interface JobNote {
  text: string;
  tone: "muted" | "warn";
}

// How long the job's visible figures have been unchanged.
//
// The signature is exactly what the user can SEE move. A batch boundary resets `bytes_done`, which
// changes the signature, so the end of one batch and the start of the next is correctly read as
// activity rather than as a stall.
function useStalledSeconds(job: Job): number | null {
  const running = !isTerminalJob(job);
  const now = useServerNow(running);
  const sig = [
    job.progress.bytes_done,
    job.progress.bytes_total,
    job.progress.percent,
    job.progress.files_received,
    job.progress.phase,
  ].join("|");
  const [changedAt, setChangedAt] = useState(() => Date.now());
  // Also fires on mount, which is the honest starting point: arriving mid-stall, quince does not
  // know when the silence began, so it counts from when it started looking. That UNDERSTATES the
  // wait, which is the direction to be wrong in.
  useEffect(() => setChangedAt(Date.now()), [sig, job.id]);
  if (!running) return null;
  return Math.max(0, Math.floor((now - changedAt) / 1000));
}

// The one place that decides what a running job says about itself.
//
// It lives in a hook rather than beside the other derivations because its branches depend
// on things a pure function of `Job` cannot see: how long since anything moved, whether the socket is up,
// and which device this is.
export function useJobNote(job: Job, variant: "full" | "card" = "full"): JobNote | null {
  const stalled = useStalledSeconds(job);
  const socketOnline = useConnectionStore((s) => s.status === "online");
  // Named from the DEVICE, never hardcoded: "your iPad" on an iPad (design §3). Falls back to
  // "device" when the model is unknown or the device is not in the store yet, which is always true.
  const family = useDevicesStore((s) => deviceFamily(s.byUdid[job.udid]?.model ?? ""));

  if (isPreparing(job)) {
    return { text: preparingNote(family, variant), tone: "muted" };
  }
  // Checked BEFORE the liveness notes so it cannot fight them: `isStalledTransfer` requires
  // `liveness === "active"`, so the moment the server escalates to its own 3-minute note, this
  // one stops applying and that one takes over.
  if (isStalledTransfer(job, stalled, socketOnline)) {
    return { text: stalledNote(family, variant), tone: "muted" };
  }
  const text = livenessNote(job);
  if (!text) return null;
  // Seeding is informational; everything left here (passcode, silent, suspected stall) is a
  // caution the user may need to act on.
  return { text, tone: job.state === "seeding" ? "muted" : "warn" };
}
