import { describe, expect, it } from "vitest";
import {

  STALL_AFTER_SECONDS,
  isStalledTransfer,
  preparingNote,
  stalledNote,
  displayPercent,
  isFinishingUp,
  isPreparing,
  isTerminalJob,
  jobStatusLabel,
  livenessNote,
} from "./state";
import type { Job } from "@/lib/types";

function mkJob(over: Partial<Job>): Job {
  return {
    id: "x",
    udid: "u1",
    kind: "backup",
    transport: "wifi",
    state: "backing_up",
    progress: {
      phase: "receiving",
      percent: 50,
      bytes_done: 0,
      bytes_total: 0,
      files_received: 0,
      liveness: "active",
    },
    started_at: "2026-07-18T00:00:00Z",
    finished_at: null,
    error: null,
    retry_of: null,
    intent_id: "i1",
    attempt: 1,
    version_id: null,
    storage_id: null,
    ...over,
  };
}

// quince#313. `livenessNote` is the narration the user reads on the surface most of them actually
// see, and it read `progress.phase` without ever asking whether the job was still running. The
// engine now clears the phase on termination, so these assert the CONSUMER independently of that —
// a consumer that quotes a running field must first ask whether anything is running, whatever a
// producer does. Every case below feeds a terminal job a live-looking progress block on purpose.
describe("livenessNote on a finished job", () => {
  it("says nothing when a failed job is still parked at waiting_for_passcode", () => {
    // The reported defect, and the ordinary shape of a Wi-Fi backup nobody unlocked: the product
    // telling a user to act on a job that is over.
    const job = mkJob({
      state: "failed",
      progress: { ...mkJob({}).progress, phase: "waiting_for_passcode" },
    });
    expect(livenessNote(job)).toBeNull();
  });

  it("says nothing when a failed job still carries a stalled liveness", () => {
    // The branch BELOW the reported one, and the reason the check goes at the top of the function
    // rather than beside the phase test: clearing the phase alone would have moved this defect to
    // "device is preparing… this can take several minutes" on a job that is not preparing anything.
    const job = mkJob({
      state: "connection_lost",
      progress: { ...mkJob({}).progress, phase: "", liveness: "suspected_stall" },
    });
    expect(livenessNote(job)).toBeNull();
  });

  it("says nothing for every terminal state, not only the reported one", () => {
    for (const state of ["succeeded", "failed", "cancelled", "connection_lost"] as const) {
      const job = mkJob({
        state,
        progress: { ...mkJob({}).progress, phase: "waiting_for_passcode" },
      });
      expect(livenessNote(job), `state=${state}`).toBeNull();
    }
  });
});

// The control, and it is the half that matters most: the fix is one careless step from silencing
// the narration this function exists to produce. `ui.design.md` principle 2 — the lab proved
// Apple's protocol goes silent for minutes — makes a missing note worse than a wrong one, because
// dead air is what the whole feature was built to remove.
describe("livenessNote on a live job", () => {
  it("still asks for the passcode while the job is running", () => {
    const job = mkJob({
      state: "backing_up",
      progress: { ...mkJob({}).progress, phase: "waiting_for_passcode" },
    });
    expect(livenessNote(job)).toBe("enter the passcode on the device to continue");
  });

  it("still narrates the seeding clone", () => {
    expect(livenessNote(mkJob({ state: "seeding" }))).toBe("cloning from your last backup…");
  });

  it("still narrates a silent-but-connected device", () => {
    const job = mkJob({
      state: "backing_up",
      progress: { ...mkJob({}).progress, phase: "receiving", liveness: "silent_but_connected" },
    });
    expect(livenessNote(job)).toBe("device is preparing… this can take several minutes");
  });
});

describe("isTerminalJob", () => {
  it("matches the server's four terminal states and nothing else", () => {
    // The client copy of `store.JobIsTerminal`. If the server's set ever grows, this is what fails
    // rather than the narration quietly going wrong on the new state.
    for (const state of ["succeeded", "failed", "cancelled", "connection_lost"] as const) {
      expect(isTerminalJob(mkJob({ state })), `terminal: ${state}`).toBe(true);
    }
    for (const state of [
      "queued",
      "waiting_for_device",
      "preflight",
      "seeding",
      "backing_up",
      "verifying",
      "committing",
    ] as const) {
      expect(isTerminalJob(mkJob({ state })), `running: ${state}`).toBe(false);
    }
  });
});

// quince#376 / quince#808. Both windows below were measured on the lab rig, 2026-08-16, on a
// 12m42s incremental: 184 s where the card showed "Backing up / — / a still bar", and 50 s where
// it showed a full bar while the tool was still working. Neither is a stall and neither was
// narrated, which is the whole of what these assert.
describe("the two windows with no percentage to show", () => {
  it("calls the pre-progress window Preparing, not Backing up", () => {
    const j = mkJob({ progress: { ...mkJob({}).progress, phase: "starting", percent: null } });
    expect(isPreparing(j)).toBe(true);
    expect(jobStatusLabel(j)).toBe("Preparing");
    // Indeterminate rather than 0: quince has no measurement here, and a zero-width bar IS a
    // measurement claim — the one the issue was filed about.
    expect(displayPercent(j)).toBeNull();
  });

  it("leaves the pre-progress window to useJobNote, which knows the device", () => {
    // `liveness` is `active` throughout that window — idevicebackup2 keeps printing, and the
    // sampler counts any output as activity — so this function could never narrate it. The note
    // now needs the device family too, which is not a property of a Job, so it lives in the hook.
    const j = mkJob({ progress: { ...mkJob({}).progress, phase: "starting", percent: null, liveness: "active" } });
    expect(livenessNote(j)).toBeNull();
  });

  it("calls a running job at 100% Finishing up, and withholds the full bar", () => {
    const j = mkJob({ state: "backing_up", progress: { ...mkJob({}).progress, percent: 100 } });
    expect(isFinishingUp(j)).toBe(true);
    expect(jobStatusLabel(j)).toBe("Finishing up");
    expect(displayPercent(j)).toBeNull();
  });

  it("leaves verifying and committing alone — they say more than 'finishing up' does", () => {
    for (const state of ["verifying", "committing"] as const) {
      const j = mkJob({ state, progress: { ...mkJob({}).progress, percent: 100 } });
      expect(isFinishingUp(j)).toBe(false);
      expect(displayPercent(j)).toBe(100);
    }
  });

  it("does not call a FINISHED job preparing, whatever phase it carries", () => {
    // The quince#313 shape: a terminal job holding a live-looking progress block.
    const j = mkJob({ state: "failed", progress: { ...mkJob({}).progress, phase: "starting", percent: null } });
    expect(isPreparing(j)).toBe(false);
    expect(jobStatusLabel(j)).toBe("Failed");
  });

  it("shows a real percentage untouched", () => {
    const j = mkJob({ progress: { ...mkJob({}).progress, percent: 63 } });
    expect(jobStatusLabel(j)).toBe("Backing up");
    expect(displayPercent(j)).toBe(63);
  });
});


// The 91 s freeze the Operator watched: zero bytes, zero CPU, zero packets, and `liveness` stayed
// `active` for the whole run because the server's note needs LivenessTimeout/6 = 3 minutes.
describe("isStalledTransfer", () => {
  const receiving = (over: Partial<Job["progress"]> = {}, state: Job["state"] = "backing_up") =>
    mkJob({ state, progress: { ...mkJob({}).progress, phase: "receiving", liveness: "active", ...over } });

  it("says so once a transfer has been quiet past the threshold", () => {
    expect(isStalledTransfer(receiving(), STALL_AFTER_SECONDS, true)).toBe(true);
  });

  it("stays silent through the routine gaps", () => {
    // Measured distribution: 131 gaps of 1-2 s, 12 of 5-10 s, one of 11-20 s. None of those is news.
    expect(isStalledTransfer(receiving(), STALL_AFTER_SECONDS - 1, true)).toBe(false);
    expect(isStalledTransfer(receiving(), 8, true)).toBe(false);
  });

  it("does not fire while merely preparing — that window has its own note", () => {
    expect(isStalledTransfer(receiving({ phase: "starting" }), 300, true)).toBe(false);
  });

  it("yields to the server once IT escalates, so the two never argue", () => {
    expect(isStalledTransfer(receiving({ liveness: "silent_but_connected" }), 300, true)).toBe(false);
    expect(isStalledTransfer(receiving({ liveness: "suspected_stall" }), 300, true)).toBe(false);
  });

  it("blames nothing on the device when OUR socket is the thing that dropped", () => {
    expect(isStalledTransfer(receiving(), 300, false)).toBe(false);
  });

  it("says nothing about a finished job", () => {
    expect(isStalledTransfer(receiving({ phase: "done" }, "succeeded"), 300, true)).toBe(false);
  });

  it("carries no second counter — the answer does not depend on the number", () => {
    expect(stalledNote("iPhone", "card")).toBe("Waiting for your iPhone…");
    expect(stalledNote("iPhone", "full")).not.toMatch(/\d/);
  });
});

// Operator, 2026-08-17, in two rounds. First: a fixed "your iPhone will ask for your passcode"
// stayed up for the whole 191 s window, long after the passcode had been entered, which reads as a
// bug. Then: a 20 s time box was too short to be seen at all. A CONDITION is true before, during
// and after, so it needs no expiry — and it is also true on a device with no passcode, which quince
// cannot detect (`PasswordProtected` reads false on a device that prompts).
describe("the preparing note", () => {
  it("states a condition rather than an instruction, so it cannot go stale", () => {
    for (const v of ["card", "full"] as const) {
      expect(preparingNote("iPhone", v)).toMatch(/if it asks/i);
    }
  });

  it("names the device the user actually has", () => {
    // design §3: iPhone AND iPad are first-class. Hardcoding "iPhone" here called an iPad an iPhone.
    expect(preparingNote("iPad", "card")).toBe("Waiting for your iPad — unlock it if it asks");
    expect(preparingNote("iPhone", "card")).toBe("Waiting for your iPhone — unlock it if it asks");
    expect(preparingNote("device", "card")).toBe("Waiting for your device — unlock it if it asks");
  });

  it("keeps the card's version to one line and says the stakes only on details", () => {
    expect(preparingNote("iPhone", "card").length).toBeLessThan(
      preparingNote("iPhone", "full").length,
    );
    expect(preparingNote("iPhone", "full")).toMatch(/can't start until you do/);
  });

  it("names the device in the stall note too", () => {
    expect(stalledNote("iPad", "card")).toBe("Waiting for your iPad…");
  });
});
