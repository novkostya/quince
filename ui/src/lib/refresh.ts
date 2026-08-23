import { api } from "./api";
import { authStatusKey, scopeOfSession } from "./auth";
import { queryClient } from "./queryClient";
import type { AuthStatus, Device, Job, Version } from "./types";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore, isRunning } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";

// devicesFor fetches the devices this session is allowed to see, as a list either way.
//
// A SCOPED HOLDER IS REFUSED THE LIST, DELIBERATELY, so asking for it is a request nobody should
// make — `GET /api/devices` is `adminOnly` in `scope_routes.go` because spec D8 rules the devices
// list UNREACHABLE rather than narrowed: a one-row list is the helpful-looking version of the thing
// D8 forbids. Their Home reads `GET /api/devices/{udid}`, which is `scopedOwnDevice` and is the
// route they actually hold. So this asks for the device rather than for the list, and the store ends
// up with the same shape from either branch.
//
// THIS IS A SECOND FIX FOR quince#1523, NOT THE FIRST. The refusal no longer costs the jobs and
// versions beside it — that landed already, and is what makes the fallback below safe rather than
// load-bearing. What this removes is the standing 403 on every connect and every reconnect, and the
// empty devices store behind it.
//
// "" MEANS ADMIN *AND* "WE CANNOT TELL", per `scopeOfSession`, and both land on the list — the same
// direction that function documents. Over-asking costs a refusal quince now absorbs; under-asking
// would hide the admin's own devices from them. The cache is populated by the time this runs:
// `AppLayout` opens the socket, and it renders only inside `RequireAuth`, which is what reads
// `/api/auth/status` in the first place.
async function devicesFor(udid: string): Promise<Device[]> {
  if (udid === "") {
    const { devices } = await api.get<{ devices: Device[] }>("/api/devices");
    return devices;
  }
  return [await api.get<Device>(`/api/devices/${udid}`)];
}

// refreshAll re-fetches the live collections and replaces the stores wholesale. It runs on
// WS connect and reconnect (contracts §3: events are notifications, not a replayable log —
// recover current state with a GET). The full-so-far log of any running job is recovered
// too, so the tailing pane has no hole after a reconnect. The stores of collections that did
// not answer keep their last state, and the connection status reflects reality.
//
// EACH COLLECTION SETTLES ON ITS OWN, AND THAT IS THE WHOLE OF quince#1523. This was one
// `Promise.all` under one `catch`, which is all-or-nothing: the first rejection discarded the
// responses that had already arrived. A device-scoped holder met that on every single connect,
// and their Home rendered `No backups yet for this device.` over a jobs list that had been
// fetched, correctly filtered, and thrown away one statement later.
//
// IT IS NOT A SCOPE FIX AND MUST NOT BE READ AS ONE. Asking for a route you are structurally
// refused is a separate defect — `devicesFor` above — and this one says that a collection quince
// could not read must not erase the two it could. The admin meets the same shape whenever one
// endpoint is transiently unavailable and the other two are fine.
export async function refreshAll(): Promise<void> {
  const scope = scopeOfSession(queryClient.getQueryData<AuthStatus>(authStatusKey));
  const [devices, jobs, versions] = await Promise.allSettled([
    devicesFor(scope),
    api.get<{ jobs: Job[]; next_cursor: string | null }>("/api/jobs"),
    api.get<{ versions: Version[] }>("/api/versions"),
  ]);

  if (devices.status === "fulfilled") useDevicesStore.getState().replaceAll(devices.value);
  else reportFailure("devices", devices.reason);

  if (versions.status === "fulfilled") useVersionsStore.getState().replaceAll(versions.value.versions);
  else reportFailure("versions", versions.reason);

  if (jobs.status !== "fulfilled") {
    reportFailure("jobs", jobs.reason);
    return;
  }
  useJobsStore.getState().replaceAll(jobs.value.jobs);
  // Downstream of the jobs list rather than beside it: there is nothing to recover a log for
  // until quince knows which jobs are running. It settles per job and reports its own
  // failures, so there is nothing here to catch — a backfill that did not arrive has never
  // cost the rows that did.
  await recoverRunningLogs(jobs.value.jobs);
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
//
// THE REVIEW'S FINDING WAS RIGHT ABOUT THE SHAPE AND WRONG ABOUT THE COST, and the difference is
// worth writing down because the wrong version is the intuitive one. quince#1524's verdict read
// *"one failing log fetch still drops the other's backfill"*. It does not: `Promise.all` AGGREGATES,
// it does not CANCEL, so a sibling's callback runs to completion and calls `setLog` whatever the
// outer promise does. Measured — the test asserting the other device's log survives passes against
// the old code, and is kept as a control for exactly that reason.
//
// TWO REAL DEFECTS WERE UNDER IT, both measured against the old code:
//
// (a) `reportFailure("job logs", …)` named the GROUP. With two devices backing up it does not say
//     which pane is short, which is the diagnostic collapse *troubleshooting is actionable* forbids.
//     The job id is what matches it to a device.
//
// (b) THE ORDERING, which is the one with teeth. `Promise.all` rejects at the FIRST failure, so
//     `refreshAll` returned with a slow sibling still in flight — and `ws/client.ts` replays the
//     events it queued during the refresh in `.finally()`. `setLog` replaces a log WHOLESALE, so a
//     backfill landing after that replay DISCARDS the chunks just replayed. Settling per job makes
//     `refreshAll` wait for all of them, so the replay is last, which is the order it assumes.
//
// So the pattern really was the third instance, and removing it fixes something — just not the
// thing it looked like.
async function recoverRunningLogs(jobs: Job[]): Promise<void> {
  const running = jobs.filter((j) => isRunning(j.state));
  const settled = await Promise.allSettled(
    running.map(async (j) => {
      const text = await api.getText(`/api/jobs/${j.id}/log`);
      const lines = text.split("\n").filter((l) => l.length > 0);
      useJobsStore.getState().setLog(j.id, lines);
    }),
  );
  settled.forEach((r, i) => {
    if (r.status === "rejected") reportFailure(`the log for job ${running[i].id}`, r.reason);
  });
}
