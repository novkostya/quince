import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { JobProgressFull } from "./JobProgress";
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
    expect(screen.getByText("3.4 GB received")).toBeTruthy();
    // A slash would mean a whole-job total exists. None does.
    expect(screen.queryByText(/\//)).toBeNull();
  });

  it("says nothing before the first bytes arrive, rather than '0 B received'", () => {
    render(<JobProgressFull job={running({ bytes_done: 0 })} />);
    expect(screen.queryByText(/received/)).toBeNull();
  });
});
