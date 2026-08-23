import { api } from "./api";
import type { Device, Job, Version } from "./types";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore, isRunning } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";

// refreshAll re-fetches the live collections and replaces the stores wholesale. It runs on
// WS connect and reconnect (contracts §3: events are notifications, not a replayable log —
// recover current state with a GET). The full-so-far log of any running job is recovered
// too, so the tailing pane has no hole after a reconnect. The stores of collections that did
// not answer keep their last state, and the connection status reflects reality.
//
// EACH COLLECTION SETTLES ON ITS OWN, AND THAT IS THE WHOLE OF quince#1523. This was one
// `Promise.all` under one `catch`, which is all-or-nothing: the first rejection discarded the
// responses that had already arrived. A device-scoped holder meets that on every single connect,
// because `GET /api/devices` is `adminOnly` by spec D8 — their Home rendered `No backups yet for
// this device.` over a jobs list that had been fetched, correctly filtered, and thrown away one
// statement later.
//
// IT IS NOT A SCOPE FIX AND MUST NOT BE READ AS ONE. Asking for a route you are structurally
// refused is a separate defect, fixed separately; this one says that a collection quince could not
// read must not erase the two it could. The admin meets the same shape whenever one endpoint is
// transiently unavailable and the other two are fine.
export async function refreshAll(): Promise<void> {
  const [devices, jobs, versions] = await Promise.allSettled([
    api.get<{ devices: Device[] }>("/api/devices"),
    api.get<{ jobs: Job[]; next_cursor: string | null }>("/api/jobs"),
    api.get<{ versions: Version[] }>("/api/versions"),
  ]);

  if (devices.status === "fulfilled") useDevicesStore.getState().replaceAll(devices.value.devices);
  else reportFailure("devices", devices.reason);

  if (versions.status === "fulfilled") useVersionsStore.getState().replaceAll(versions.value.versions);
  else reportFailure("versions", versions.reason);

  if (jobs.status !== "fulfilled") {
    reportFailure("jobs", jobs.reason);
    return;
  }
  useJobsStore.getState().replaceAll(jobs.value.jobs);
  // Downstream of the jobs list rather than beside it: there is nothing to recover a log for until
  // quince knows which jobs are running. Its own failures are swallowed for the reason above — a
  // log backfill that did not arrive must not cost the rows that did.
  try {
    await recoverRunningLogs(jobs.value.jobs);
  } catch (err) {
    reportFailure("job logs", err);
  }
}

// reportFailure is the *no silent caps or fallbacks* half of the guard above. A refresh that half
// succeeded leaves stale rows on screen under a connection badge that says `online`, so the console
// is the only place today that says which collection is stale and why — it names the collection,
// because "refresh failed" over three endpoints is the diagnostic that collapses the three causes
// the reader needs to tell apart.
//
// SURFACING IT IN THE UI IS NOT DONE, AND IS quince#1523's open question rather than an oversight.
function reportFailure(what: string, err: unknown): void {
  console.warn(`quince: could not refresh ${what}; showing the last state quince has`, err);
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
