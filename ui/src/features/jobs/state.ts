import type { Job } from "@/lib/types";

const STATE_LABELS: Record<Job["state"], string> = {
  queued: "Queued",
  waiting_for_device: "Waiting for device",
  preflight: "Preflight",
  backing_up: "Backing up",
  verifying: "Verifying",
  committing: "Committing",
  succeeded: "Succeeded",
  failed: "Failed",
  cancelled: "Cancelled",
  connection_lost: "Connection lost",
};

export function humanJobState(s: Job["state"]): string {
  return STATE_LABELS[s] ?? s;
}

// livenessNote returns honest narration for the slow/silent/passcode phases (ui.design.md
// principle 2 — the lab proved Apple's protocol goes silent for minutes; never fake motion).
export function livenessNote(job: Job): string | null {
  if (job.progress.phase === "waiting_for_passcode") {
    return "enter the passcode on the device to continue";
  }
  if (job.progress.liveness === "silent_but_connected" || job.progress.liveness === "suspected_stall") {
    return "device is preparing… this can take several minutes";
  }
  return null;
}
