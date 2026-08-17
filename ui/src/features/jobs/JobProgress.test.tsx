import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { JobProgressFull, JobProgressInline } from "./JobProgress";
import type { Job } from "@/lib/types";

// quince#808 / Operator 2026-08-17. `bytes_done`/`bytes_total` are never cleared, so the last
// batch's pair outlives the batch. Rendering it beside "Finishing up" claims a transfer that has
// ended — the state the Operator called "super confusing" on a real run.
describe("JobProgressFull in the finishing and terminal states", () => {
  const mk = (over: Partial<Job["progress"]>, state: Job["state"] = "backing_up"): Job => ({
    id: "J", udid: "u", kind: "backup", transport: "wifi", state,
    progress: { phase: "receiving", percent: 100, bytes_done: 2_000_000_000,
                bytes_total: 2_100_000_000, files_received: 5, liveness: "active", ...over },
    started_at: new Date(Date.now() - 60_000).toISOString(), finished_at: null, error: null,
    retry_of: null, intent_id: "i", attempt: 1, version_id: null, storage_id: null,
  });

  it("hides the stale byte pair while finishing up", () => {
    render(<JobProgressFull job={mk({})} />);
    expect(screen.getByText("Finishing up")).toBeTruthy();
    expect(screen.queryByText(/received/)).toBeNull();
    // And no bare dash where the percentage would be.
    expect(screen.queryByText("—")).toBeNull();
  });

  it("hides it once the job is terminal", () => {
    render(<JobProgressFull job={mk({ phase: "done", liveness: "" }, "succeeded")} />);
    expect(screen.queryByText(/received/)).toBeNull();
  });

  it("still shows it while a transfer is genuinely running", () => {
    render(<JobProgressFull job={mk({ percent: 1, bytes_done: 57_300_000, bytes_total: 2_700_000_000 })} />);
    expect(screen.getByText(/received/)).toBeTruthy();
  });
});

// quince#808. `bytes_done` is cumulative and monotonic now, and `bytes_total` is 0 = unknown,
// because every total the protocol exposes is per-message. So the panel states what arrived and
// divides by nothing — there is no honest denominator to divide by.
describe("JobProgressFull states what has arrived", () => {
  const running = (over: Partial<Job["progress"]> = {}): Job => ({
    id: "J", udid: "u", kind: "backup", transport: "wifi", state: "backing_up",
    progress: { phase: "receiving", percent: 1, bytes_done: 3_400_000_000, bytes_total: 0,
                files_received: 0, liveness: "active", ...over },
    started_at: new Date(Date.now() - 60_000).toISOString(), finished_at: null, error: null,
    retry_of: null, intent_id: "i", attempt: 1, version_id: null, storage_id: null,
  });

  it("shows a bare cumulative figure with no denominator", () => {
    render(<JobProgressFull job={running()} />);
    expect(screen.getByText("3.40 GB received")).toBeTruthy();
    // A slash would mean a whole-job total exists. None does.
    expect(screen.queryByText(/\//)).toBeNull();
  });

  it("says nothing before the first bytes arrive, rather than '0 B received'", () => {
    render(<JobProgressFull job={running({ bytes_done: 0 })} />);
    expect(screen.queryByText(/received/)).toBeNull();
  });
});

// Operator, 2026-08-17: put the received figure on the Home card too, beside the timer. It was
// removed from this surface earlier the same day — as a per-batch PAIR it contradicted the
// percentage and cost a whole row. quince#808 fixed both objections rather than overruling them.
describe("JobProgressInline shows what has arrived, beside the clock", () => {
  const card = (over: Partial<Job["progress"]> = {}, state: Job["state"] = "backing_up"): Job => ({
    id: "J", udid: "u", kind: "backup", transport: "wifi", state,
    progress: { phase: "receiving", percent: 63, bytes_done: 3_400_000_000, bytes_total: 0,
                files_received: 0, liveness: "active", ...over },
    started_at: new Date(Date.now() - 60_000).toISOString(), finished_at: null, error: null,
    retry_of: null, intent_id: "i", attempt: 1, version_id: null, storage_id: null,
  });

  it("puts the figure and the percentage on ONE row, so the card keeps its height", () => {
    const { container } = render(<JobProgressInline job={card()} />);
    expect(screen.getByText(/3.40 GB received/)).toBeTruthy();
    expect(screen.getByText("63%")).toBeTruthy();
    // The label, the clock and the figure share one truncating span — the row count is what made
    // this card taller than its neighbours last time.
    expect(container.querySelectorAll(".truncate").length).toBe(1);
  });

  it("cannot contradict the percentage, because it carries no denominator", () => {
    render(<JobProgressInline job={card()} />);
    expect(screen.queryByText(/\//)).toBeNull();
  });

  it("says nothing while finishing up, where it would describe a step that has ended", () => {
    render(<JobProgressInline job={card({ percent: 100 })} />);
    expect(screen.getByText("Finishing up")).toBeTruthy();
    expect(screen.queryByText(/received/)).toBeNull();
  });

  it("says nothing before the first bytes arrive", () => {
    render(<JobProgressInline job={card({ bytes_done: 0 })} />);
    expect(screen.queryByText(/received/)).toBeNull();
  });
});

// THE DEFECT THIS PR EXISTS TO FIX, pinned (quince#1117 review). The figure was hidden for
// `Finishing up` and for terminal states, but `verifying` and `committing` slipped through: both
// describe work that happens AFTER the transfer, and `bytes_done` is never cleared, so it sat
// frozen beside them and read as a transfer still running.
//
// Reverting the guard to the exclusion list it replaced — `isTerminalJob(job) || isFinishingUp(job)`
// — passed the entire suite before these cases existed. The whole argument for the positive
// condition is that it fails SAFE for a state added later, and that is an argument about the
// future, which is exactly what a test defends and a comment does not.
describe("the received figure stops when bytes stop arriving", () => {
  const mid = (state: Job["state"]): Job => ({
    id: "J", udid: "u", kind: "backup", transport: "wifi", state,
    // A live-looking progress block on purpose: the field is frozen at its last value, not zeroed,
    // which is precisely why a state-based guard is needed rather than a value-based one.
    progress: { phase: state, percent: 100, bytes_done: 3_400_000_000, bytes_total: 0,
                files_received: 0, liveness: "active" },
    started_at: new Date(Date.now() - 60_000).toISOString(), finished_at: null, error: null,
    retry_of: null, intent_id: "i", attempt: 1, version_id: null, storage_id: null,
  });

  for (const state of ["verifying", "committing"] as const) {
    it(`says nothing during ${state}, where the transfer is already over`, () => {
      const { unmount } = render(<JobProgressFull job={mid(state)} />);
      expect(screen.queryByText(/received/)).toBeNull();
      unmount();
      render(<JobProgressInline job={mid(state)} />);
      expect(screen.queryByText(/received/)).toBeNull();
    });
  }

  it("still shows it while backing up, so the guard cannot pass by hiding everything", () => {
    // The control. Without it, a guard that returned null unconditionally would satisfy every
    // assertion above — which is how a "fix" removes the feature and still looks tested.
    render(<JobProgressFull job={{ ...mid("backing_up"), progress: { ...mid("backing_up").progress, percent: 42 } }} />);
    expect(screen.getByText(/3.40 GB received/)).toBeTruthy();
  });
});
