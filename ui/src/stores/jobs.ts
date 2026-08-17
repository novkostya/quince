import { create } from "zustand";
import type { Job } from "@/lib/types";

const LOG_CAP = 500; // ring buffer: keep the last N log lines per job

const RUNNING: ReadonlySet<Job["state"]> = new Set([
  "queued",
  "waiting_for_device",
  "preflight",
  "seeding",
  "backing_up",
  "verifying",
  "committing",
]);

interface JobsState {
  byId: Record<string, Job>;
  logByJobId: Record<string, string[]>;
  upsert: (j: Job) => void;
  appendLog: (jobId: string, chunk: string) => void;
  setLog: (jobId: string, lines: string[]) => void;
  replaceAll: (jobs: Job[]) => void;
}

export const useJobsStore = create<JobsState>((set) => ({
  byId: {},
  logByJobId: {},
  upsert: (j) => set((s) => ({ byId: { ...s.byId, [j.id]: j } })),
  appendLog: (jobId, chunk) =>
    set((s) => {
      const existing = s.logByJobId[jobId] ?? [];
      const next = [...existing, chunk];
      if (next.length > LOG_CAP) next.splice(0, next.length - LOG_CAP);
      return { logByJobId: { ...s.logByJobId, [jobId]: next } };
    }),
  // setLog replaces a job's log wholesale — used to recover the full-so-far tail from
  // GET /api/jobs/{id}/log on WS reconnect (the live job.log stream is not replayable).
  setLog: (jobId, lines) =>
    set((s) => {
      const capped = lines.length > LOG_CAP ? lines.slice(lines.length - LOG_CAP) : lines;
      return { logByJobId: { ...s.logByJobId, [jobId]: capped } };
    }),
  replaceAll: (jobs) => set(() => ({ byId: Object.fromEntries(jobs.map((j) => [j.id, j])) })),
}));

export function isRunning(state: Job["state"]): boolean {
  return RUNNING.has(state);
}

// THE NEWEST running job for a device, which is not what `find` gives you.
//
// `Object.values(byId)` is INSERTION order, so `jobs.find(j => isRunning(j.state))` returns the
// FIRST running job it meets — the oldest. `upsert` appends a newly created job at the end, so
// during the window between starting a backup and the previous one's terminal update arriving,
// `find` keeps returning the PREVIOUS job.
//
// Measured symptom (Operator, 2026-08-17): cancel a backup, tap Back up now, and the progress pane
// shows a timer already reading 26 s — because it is still rendering the job you just cancelled.
// The passcode hint was invisible for the same reason: the job on screen was long past it.
//
// `started_at` is the key rather than the id, matching `newestJob` in DeviceCard, which had this
// right all along. Ids are ULIDs and would sort the same way; the tie-break uses the id because two
// jobs can share a start second.
export function newestRunningJob(jobs: Job[]): Job | undefined {
  return jobs.reduce<Job | undefined>((newest, j) => {
    if (!isRunning(j.state)) return newest;
    if (!newest) return j;
    if (j.started_at !== newest.started_at) return j.started_at > newest.started_at ? j : newest;
    return j.id > newest.id ? j : newest;
  }, undefined);
}
