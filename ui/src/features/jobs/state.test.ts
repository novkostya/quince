import { describe, expect, it } from "vitest";
import { isTerminalJob, livenessNote } from "./state";
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
