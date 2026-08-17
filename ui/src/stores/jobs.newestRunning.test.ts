import { describe, expect, it } from "vitest";
import { newestRunningJob } from "./jobs";
import type { Job } from "@/lib/types";

function job(id: string, state: Job["state"], started_at: string): Job {
  return {
    id, udid: "u1", kind: "backup", transport: "wifi", state,
    progress: { phase: "receiving", percent: null, bytes_done: 0, bytes_total: 0,
                files_received: 0, liveness: "active" },
    started_at, finished_at: null, error: null, retry_of: null,
    intent_id: id, attempt: 1, version_id: null, storage_id: null,
  };
}

// Operator, 2026-08-17: cancel a backup, tap Back up now, and the pane shows a timer already at
// 26 s. `Object.values(byId)` is insertion order and `upsert` appends, so `find` returned the job
// that was cancelled rather than the one just started — and every derived thing on that pane, the
// elapsed clock and the time-boxed passcode hint included, described the wrong job.
describe("newestRunningJob", () => {
  it("picks the newest running job, not the first one in the store", () => {
    // Insertion order deliberately oldest-first: this is the case `find` got wrong.
    const jobs = [job("A", "backing_up", "2026-08-17T05:33:34Z"), job("B", "backing_up", "2026-08-17T05:35:03Z")];
    expect(newestRunningJob(jobs)?.id).toBe("B");
  });

  it("still picks the newest when the store happens to hold them newest-first", () => {
    const jobs = [job("B", "backing_up", "2026-08-17T05:35:03Z"), job("A", "backing_up", "2026-08-17T05:33:34Z")];
    expect(newestRunningJob(jobs)?.id).toBe("B");
  });

  it("ignores finished jobs however recent they are", () => {
    const jobs = [job("A", "backing_up", "2026-08-17T05:33:34Z"), job("B", "succeeded", "2026-08-17T05:35:03Z")];
    expect(newestRunningJob(jobs)?.id).toBe("A");
  });

  it("returns nothing when the device has no running job", () => {
    expect(newestRunningJob([job("A", "cancelled", "2026-08-17T05:33:34Z")])).toBeUndefined();
    expect(newestRunningJob([])).toBeUndefined();
  });

  it("breaks a same-second tie by id, because ULIDs are monotonic", () => {
    const jobs = [job("01M0A", "backing_up", "2026-08-17T05:35:03Z"), job("01M0B", "backing_up", "2026-08-17T05:35:03Z")];
    expect(newestRunningJob(jobs)?.id).toBe("01M0B");
  });
});
