import { api } from "./api";
import type { Device, Job, Version } from "./types";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";

// refreshAll re-fetches the live collections and replaces the stores wholesale. It runs on
// WS connect and reconnect (contracts §3: events are notifications, not a replayable log —
// recover current state with a GET). Failures are swallowed: the stores keep their last
// state and the connection status reflects reality.
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
  } catch {
    // transient / unauthorized — leave stores as-is
  }
}
