import type { Device, Storage } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { useBackup } from "@/features/jobs/useBackup";
import { isRunning, useJobsStore } from "@/stores/jobs";
import { JobProgressInline } from "@/features/jobs/JobProgress";

// StorageDeviceBackup is `Back up now` on the STORAGE details page — story 6.
//
// It is `qn.6c` story 9's selector answered by CONTEXT instead of a dropdown: the page you are on
// IS the destination, so the job is bound to this storage and there is nothing to choose. That is
// also why this is a component per device rather than one control for the page — `useBackup` is
// keyed by udid and hooks cannot be called in a loop.
//
// IT REFUSES BEFORE THE SERVER HAS TO. A storage whose resolution did not succeed never accepts a
// job (`Slot.Usable()`, design §5), so offering the action on an unreachable storage would earn a
// 409 and teach the user that the button is unreliable rather than that the disk is unplugged.
// Same for a storage with no id: one that was never created cannot be a job's destination.
//
// THAT PRINCIPLE WAS IMPLEMENTED FOR THREE CASES AND MISSING THE COMMONEST ONE (quince#639).
//
// `busy` is not "a backup is running" — it is "the HTTP request is in flight". `useBackup` sets it
// around one POST, so the button disabled for a few hundred milliseconds and re-enabled with the
// same label, and the job ran invisibly. Pressing again earned `a backup is already in progress`,
// which was the FIRST and ONLY visible consequence of having pressed the button at all — the
// component teaching exactly the lesson its own comment forbids.
//
// So this now subscribes to the jobs store, which is where the truth is, and keeps `busy` for what
// it honestly tracks: the optimistic window between the POST returning and the job reaching the
// store. `useBackup` is NOT changed — it is accurate about a request; the defect was a caller
// reading a request flag as a job state, and fixing it in the hook would move the confusion rather
// than remove it.
//
// A RUNNING JOB AIMED SOMEWHERE ELSE GETS THE BUTTON DISABLED, NOT A PROGRESS BAR (ruled). A
// progress bar here asserts "a backup is arriving HERE", and on the one page whose entire premise
// is that the destination is the page, that is false for a job going to another disk. It is a
// fourth rung on the `blocked` ladder rather than new machinery.
export function StorageDeviceBackup({ device, storage }: { device: Device; storage: Storage }) {
  const { start, busy, error } = useBackup(device.udid);

  // The authoritative signal, exactly as `DeviceCard` reads it: the jobs store, fed by the
  // `job.updated` WS stream. Single-flight is per DEVICE, so at most one of these is running.
  const activeJob = useJobsStore((s) =>
    Object.values(s.byId).find((j) => j.udid === device.udid && isRunning(j.state)),
  );
  const here = activeJob?.storage_id === storage.id && storage.id !== "";

  const present = Boolean(device.transports.usb || device.transports.wifi);
  const blocked = !storage.reachable
    ? "not connected — plug this storage in to back up to it"
    : storage.id === ""
      ? "quince has never reached this storage, so it cannot be a destination yet"
      : !present
        ? "connect this device over USB or Wi-Fi to back it up"
        : // The fourth rung. An unresolvable destination says "already backing up" rather than
          // naming a storage — "to <the default>" would be a claim the job never made, which is
          // the same restraint `DeviceCard` shows when it cannot resolve `storage_id`.
          activeJob && !here
          ? "this device is already backing up"
          : null;

  // THE JOB, NOT THE REQUEST. Rendered only when the running job is aimed at THIS storage.
  if (activeJob && here) {
    return (
      <div className="mt-2" data-testid="storage-device-progress">
        <JobProgressInline job={activeJob} />
      </div>
    );
  }

  return (
    <div className="mt-2">
      <Button
        size="sm"
        variant="outline"
        data-testid="storage-device-backup"
        data-udid={device.udid}
        disabled={blocked !== null || busy}
        onClick={() => {
          // The storage is the PAGE's, never a selection — which is the whole of story 6. `auto`
          // lets the engine resolve the transport, exactly as the device page does.
          void start("auto", { storageID: storage.id });
        }}
      >
        {/* THE OPTIMISTIC LABEL, carried over from `DeviceCard` (quince#639). It covers the window
            between the POST returning and the job reaching the store — without it the fix has a
            visible gap in exactly the moment it exists to fill. This is the honest use of `busy`:
            a request is in flight, which is all it ever knew. */}
        {busy ? "Starting…" : "Back up now"}
      </Button>
      {/* The reason sits BELOW the control, never inside the row (quince#325: the action row holds
          controls, prose goes beneath). A disabled button with no reason is the state this project
          has already had to fix once. */}
      {blocked !== null ? (
        <div data-testid="storage-device-backup-blocked" className="mt-1 text-xs text-muted">
          {blocked}
        </div>
      ) : null}
      {error !== null ? (
        <div role="alert" className="mt-1 text-xs text-danger">
          {error}
        </div>
      ) : null}
    </div>
  );
}
