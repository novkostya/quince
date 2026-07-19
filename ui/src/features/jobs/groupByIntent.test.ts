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
});
