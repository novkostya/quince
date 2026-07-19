import { api } from "./api";
import type { Device, Job, Version } from "./types";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore, isRunning } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";

// refreshAll re-fetches the live collections and replaces the stores wholesale. It runs on
// WS connect and reconnect (contracts §3: events are notifications, not a replayable log —
// recover current state with a GET). The full-so-far log of any running job is recovered
// too, so the tailing pane has no hole after a reconnect. Failures are swallowed: the
// stores keep their last state and the connection status reflects reality.
export async function refreshAll(): Promise<void> {
  try {
    const [devices, jobs, versions] = await Promise.all([
      api.get<{ devices: Device[] }>("/api/devices"),
      api.get<{ jobs: Job[]; next_cursor: string | null }>("/api/jobs"),
      api.get<{ versions: Version[] }>("/api/versions"),
    ]);
    useDevicesStore.getState().replaceAll(devices.devices);
    useJobsStore.getState().replaceAll(jobs.jobs);
    useVersionsStore.getState().replaceAll(versions.versions);
    await recoverRunningLogs(jobs.jobs);
  } catch {
    // transient / unauthorized — leave stores as-is
  }
}

// recoverRunningLogs re-fetches the full-so-far log of each running job (GET
// /api/jobs/{id}/log, contracts §1) so the WS job.log stream missed during a disconnect is
// filled back in. Bounded: only running jobs, of which there is at most one per device.
async function recoverRunningLogs(jobs: Job[]): Promise<void> {
  await Promise.all(
    jobs
      .filter((j) => isRunning(j.state))
      .map(async (j) => {
        const text = await api.getText(`/api/jobs/${j.id}/log`);
        const lines = text.split("\n").filter((l) => l.length > 0);
        useJobsStore.getState().setLog(j.id, lines);
      }),
  );
}
