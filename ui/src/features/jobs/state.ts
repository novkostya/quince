import type { Job } from "@/lib/types";

const STATE_LABELS: Record<Job["state"], string> = {
  queued: "Queued",
  waiting_for_device: "Waiting for device",
  preflight: "Preflight",
  seeding: "Preparing",
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

// TERMINAL_STATES is the client's copy of the server's `store.JobIsTerminal`. Duplicated rather
// than derived, because the wire carries the state string and no flag — there is nowhere to derive
// it from — and the four names are frozen in contracts §2.
const TERMINAL_STATES = new Set<Job["state"]>([
  "succeeded",
  "failed",
  "cancelled",
  "connection_lost",
]);

export function isTerminalJob(job: Job): boolean {
  return TERMINAL_STATES.has(job.state);
}

// livenessNote returns honest narration for the slow/silent/passcode phases (ui.design.md
// principle 2 — the lab proved Apple's protocol goes silent for minutes; never fake motion).
export function livenessNote(job: Job): string | null {
  // A FINISHED JOB NARRATES NOTHING LIVE (quince#313). This is the surface most users actually see,
  // and it had the same defect as the CLI: the passcode branch read `progress.phase` without ever
  // asking whether the job was still running, so a Wi-Fi backup that failed while parked at
  // `waiting_for_passcode` — the ordinary shape of one nobody unlocked — went on telling the user
  // to "enter the passcode on the device to continue" after it was over.
  //
  // Asked FIRST, above every other branch, because the liveness branch below has the same shape and
  // would otherwise narrate "device is preparing…" on a failed job the moment the phase was cleared.
  // Fixing only the branch that was reported would have moved the bug rather than removed it.
  if (isTerminalJob(job)) {
    return null;
  }
  if (job.state === "seeding") {
    // The clone runs BEFORE idevicebackup2 starts, so the on-device passcode prompt can't appear yet
    // — narrate the wait instead of dead air (qn.6a (cu)/(cv)).
    return "cloning from your last backup…";
  }
  if (job.progress.phase === "waiting_for_passcode") {
    return "enter the passcode on the device to continue";
  }
  if (job.progress.liveness === "silent_but_connected" || job.progress.liveness === "suspected_stall") {
    return "device is preparing… this can take several minutes";
  }
  return null;
}
