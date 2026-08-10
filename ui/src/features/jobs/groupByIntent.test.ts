import { describe, expect, it } from "vitest";
import { groupByIntent } from "./groupByIntent";
import type { Job } from "@/lib/types";

function mkJob(over: Partial<Job>): Job {
  return {
    id: "x",
    udid: "u1",
    kind: "backup",
    transport: "wifi",
    state: "succeeded",
    progress: {
      phase: "done",
      percent: 100,
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

describe("groupByIntent", () => {
  it("folds retries into one operation with a summary", () => {
    const jobs = [
      mkJob({ id: "a", intent_id: "i1", attempt: 1, state: "failed" }),
      mkJob({ id: "b", intent_id: "i1", attempt: 2, state: "succeeded", retry_of: "a" }),
    ];
    const groups = groupByIntent(jobs);
    expect(groups).toHaveLength(1);
    expect(groups[0].attempts.map((j) => j.id)).toEqual(["a", "b"]);
    expect(groups[0].latest.id).toBe("b");
    expect(groups[0].summary).toBe("Backup completed after 1 retry");
  });

  it("summarizes a clean single-attempt success", () => {
    const groups = groupByIntent([mkJob({ id: "a", intent_id: "i2", attempt: 1, state: "succeeded" })]);
    expect(groups[0].summary).toBe("Backup completed");
  });

  it("orders intents newest first", () => {
    const jobs = [
      mkJob({ id: "old", intent_id: "old", started_at: "2026-07-10T00:00:00Z" }),
      mkJob({ id: "new", intent_id: "new", started_at: "2026-07-18T00:00:00Z" }),
    ];
    expect(groupByIntent(jobs).map((g) => g.intentId)).toEqual(["new", "old"]);
  });

  // quince#813: the reported defect. The row said "Backup completed 57 minutes ago" about a backup
  // that completed 28 minutes ago, because it was timestamped with the START. The error equals the
  // backup's duration, so the fixture uses a long one.
  it("dates a terminal group from its OUTCOME, not its start", () => {
    const groups = groupByIntent([
      mkJob({
        id: "a",
        intent_id: "i1",
        state: "succeeded",
        started_at: "2026-08-10T05:41:00Z",
        finished_at: "2026-08-10T06:10:40Z",
      }),
    ]);
    expect(groups[0].at).toBe("2026-08-10T06:10:40Z");
    expect(groups[0].atIsStart).toBe(false);
  });

  it.each(["failed", "cancelled", "connection_lost"] as const)(
    "dates a %s group from its outcome too — the summary is past tense for all of them",
    (state) => {
      const groups = groupByIntent([
        mkJob({
          id: "a",
          intent_id: "i1",
          state,
          started_at: "2026-08-10T05:41:00Z",
          finished_at: "2026-08-10T06:10:40Z",
        }),
      ]);
      expect(groups[0].at).toBe("2026-08-10T06:10:40Z");
      expect(groups[0].atIsStart).toBe(false);
    },
  );

  it("dates a RUNNING group from its start — it has no outcome to date from", () => {
    const groups = groupByIntent([
      mkJob({ id: "a", intent_id: "i1", state: "backing_up", started_at: "2026-08-10T06:20:00Z", finished_at: null }),
    ]);
    expect(groups[0].at).toBe("2026-08-10T06:20:00Z");
    expect(groups[0].atIsStart).toBe(true);
  });

  // The retry-fold, and it is invisible on the rig this was found on: r30 measured a real retried
  // intent whose two attempts were 11 SECONDS apart, so latest and attempts[0] agreed. It bites when
  // a long attempt fails and is retried — the operation began when the FIRST attempt did.
  it("dates a running retried group from the FIRST attempt's start, not the latest attempt's", () => {
    const groups = groupByIntent([
      mkJob({ id: "a", intent_id: "i1", attempt: 1, state: "failed", started_at: "2026-08-10T04:00:00Z" }),
      mkJob({
        id: "b",
        intent_id: "i1",
        attempt: 2,
        state: "backing_up",
        started_at: "2026-08-10T06:00:00Z",
        retry_of: "a",
      }),
    ]);
    expect(groups[0].at).toBe("2026-08-10T04:00:00Z");
    expect(groups[0].atIsStart).toBe(true);
  });

  // Terminal with no finished_at cannot happen per contracts §2, but the wire type is nullable.
  // The fallback is the start AND says so, so the row reads oddly rather than falsely.
  it("falls back to the start when a terminal group has no finished_at — and flags it as a start", () => {
    const groups = groupByIntent([
      mkJob({ id: "a", intent_id: "i1", state: "succeeded", started_at: "2026-08-10T05:41:00Z", finished_at: null }),
    ]);
    expect(groups[0].at).toBe("2026-08-10T05:41:00Z");
    expect(groups[0].atIsStart).toBe(true);
  });

  // The ordering hazard quince#813 reasoned about and did not reproduce. Sorting on started_at while
  // labelling from finished_at renders two OVERLAPPING intents in an order their own timestamps deny.
  it("orders OVERLAPPING intents by the instant each row displays", () => {
    const jobs = [
      // A long backup: started first, finished second.
      mkJob({
        id: "long",
        intent_id: "long",
        state: "succeeded",
        started_at: "2026-08-10T05:00:00Z",
        finished_at: "2026-08-10T06:30:00Z",
      }),
      // A quick one started INSIDE that window and finished before it.
      mkJob({
        id: "quick",
        intent_id: "quick",
        state: "succeeded",
        started_at: "2026-08-10T05:30:00Z",
        finished_at: "2026-08-10T05:40:00Z",
      }),
    ];
    // Sorted on started_at this reads ["quick", "long"] while showing 06:30 below 05:40.
    expect(groupByIntent(jobs).map((g) => g.intentId)).toEqual(["long", "quick"]);
  });
});
