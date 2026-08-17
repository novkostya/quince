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
  // `Preparing` AND the quiet-transfer note are NOT here — `useJobNote` handles both, because both
  // need something this function cannot see: the device's family, to avoid calling an iPad an
  // iPhone (design §3). `useJobNote` checks them before calling this, so reaching the liveness
  // branch below means neither applied.
  //
  // The window they cover is quince#376's: before the first percentage, `liveness` is `active`
  // throughout — idevicebackup2 is printing, and the sampler counts any output as activity — so the
  // branch below can never catch it, and it needs 3 minutes (LivenessTimeout/6) even when it can.
  if (job.progress.liveness === "silent_but_connected" || job.progress.liveness === "suspected_stall") {
    return "device is preparing… this can take several minutes";
  }
  return null;
}

// PREPARING — the job has started and the device has not yet reported any progress.
//
// `starting` is set the moment idevicebackup2 launches (engine.go) and is replaced by `receiving`
// on the tool's first "Receiving files" line. It has always been on the wire and nothing rendered
// it, so this window showed "Backing up", "—" and a still bar: the card that reads as stalled.
export function isPreparing(job: Job): boolean {
  return !isTerminalJob(job) && job.state === "backing_up" && job.progress.phase === "starting";
}

// FINISHING UP — the transfer percentage has reached 100 and the job is still running.
//
// This is not a rounding artefact, it is how the tool behaves. `percent` comes from the DEVICE's
// own figure, and idevicebackup2 latches it: once `overall_progress >= 100`, `progress_finished`
// is set and it never prints progress again (idevicebackup2.c:2523, tag 1.4.0) — while it goes on
// moving and removing files, and quince still has to verify and commit. Measured 2026-08-16: 50 s
// between percent hitting 100 and the job going terminal, of which verify+commit were 3 s.
//
// So a determinate 100% bar here claims knowledge quince no longer has. The remedy is to stop
// claiming, not to cap the number at 99 — that would make a true figure false. `verifying` and
// `committing` are deliberately NOT included: those states name what is happening, which is
// strictly more than "finishing up" says.
export function isFinishingUp(job: Job): boolean {
  return (
    job.state === "backing_up" && job.progress.percent !== null && job.progress.percent >= 100
  );
}

// The label on the card. Both overrides describe the SAME state (`backing_up`) more precisely
// than its name does, which is why they live here rather than in STATE_LABELS.
export function jobStatusLabel(job: Job): string {
  if (isPreparing(job)) return "Preparing";
  if (isFinishingUp(job)) return "Finishing up";
  return humanJobState(job.state);
}

// What the BAR should show, which is not always what the wire says.
//
// Null means indeterminate. During `Finishing up` the wire says 100 and 100 is true of the
// transfer — but the job is not done, and a full bar is read as "done". Withholding the number is
// the honest move; the label and the elapsed clock carry the reassurance instead.
export function displayPercent(job: Job): number | null {
  if (isFinishingUp(job)) return null;
  return job.progress.percent;
}


// HOW LONG NOTHING MAY ARRIVE BEFORE WE SAY SO. Derived from a real run rather than chosen: in the
// 2026-08-17 backup the gaps between updates were 131 of 1-2 s, 9 of 3-4 s, 12 of 5-10 s, one of
// 11-20 s — and then one 26 s and one 91 s. 20 s separates the routine from the two the Operator
// actually noticed, firing twice in ten minutes.
export const STALL_AFTER_SECONDS = 20;

// The note during `Preparing`. ONE MESSAGE FOR THE WHOLE WINDOW, phrased as a condition rather
// than an instruction, which is what lets the time box go away entirely.
//
// The time-boxed version had to expire because "your iPhone will ask for your passcode" becomes
// false the moment you have entered it, and quince cannot tell when that is — idevicebackup2 does
// not report the prompt until ~190 s later, where it lasts one second. But 20 s proved far too
// short to be seen at all. "unlock it if it asks" is true before, during and after, so it needs no
// expiry — and it is also true on a device with no passcode set, which quince equally cannot
// detect: `PasswordProtected` reads FALSE on a device that prompts, because it means *currently
// locked* rather than *has a passcode* (measured on hardware, 2026-08-17).
//
// `family` is "iPhone" / "iPad" / "device" — never hardcoded, per design §3.
export function preparingNote(family: string, variant: "full" | "card"): string {
  return variant === "card"
    ? `Waiting for your ${family} — unlock it if it asks`
    : `Waiting for your ${family}… If it asks for your passcode, enter it — the backup can't start until you do.`;
}

// Whether a transfer that has gone quiet should say so. Every clause is a way of being wrong:
//   - phase must be `receiving`  — `starting` legitimately shows nothing and has its own note
//   - liveness must be `active`  — once the server's own 3-minute note fires, that one wins
//   - the socket must be online  — otherwise a dropped connection masquerades as a device stall
export function isStalledTransfer(
  job: Job,
  stalledSeconds: number | null,
  socketOnline: boolean,
): boolean {
  if (isTerminalJob(job) || !socketOnline) return false;
  if (job.state !== "backing_up" || job.progress.phase !== "receiving") return false;
  if (job.progress.liveness !== "active") return false;
  return stalledSeconds !== null && stalledSeconds >= STALL_AFTER_SECONDS;
}

// NO SECOND COUNTER, deliberately (Operator, 2026-08-17: "we're building product for humans").
// A ticking "nothing received for 45s" is a diagnostic readout; the only question being asked is
// "should I worry", and the answer does not depend on the number.
export function stalledNote(family: string, variant: "full" | "card"): string {
  return variant === "card"
    ? `Waiting for your ${family}…`
    : `Waiting for your ${family}… This can pause for a minute or two.`;
}
