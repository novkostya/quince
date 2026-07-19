import { create } from "zustand";
import type { Job } from "@/lib/types";

const LOG_CAP = 500; // ring buffer: keep the last N log lines per job

const RUNNING: ReadonlySet<Job["state"]> = new Set([
  "queued",
  "waiting_for_device",
  "preflight",
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
