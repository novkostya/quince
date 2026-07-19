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
  replaceAll: (jobs) => set(() => ({ byId: Object.fromEntries(jobs.map((j) => [j.id, j])) })),
}));

export function isRunning(state: Job["state"]): boolean {
  return RUNNING.has(state);
}
