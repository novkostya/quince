import * as React from "react";
import type { Device, Job } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { StorageSelect, StorageNotices } from "./StorageSelect";
import type { Storages } from "./useStorages";
import type { RequestTransport } from "./useBackup";

interface BackupControlsProps {
  device: Device;
  activeJob?: Job;
  start: (transport: RequestTransport, opts?: { storageID?: string; retryOf?: string }) => Promise<boolean>;
  cancel: (jobId: string) => Promise<boolean>;
  busy: boolean;
  storages: Storages;
  storageID: string;
  setStorageID: (id: string) => void;
}

// BackupControls is the assisted "Back up now" action on a device's details page. It starts a backup
// over the chosen transport (default "auto" — the engine resolves it, design §4/(bp)), offers a
// transport override only when the device is present on both, and cancels a running job. The
// started/cancelled job renders from the WS job.updated stream; this never fabricates progress
// (ui.design.md). start/cancel/busy are lifted to the page so Retry shares the same state.
//
// It renders BUTTONS ONLY. Everything block-level that used to stack underneath — the reason the
// button is disabled, the shared refusal — lives in BackupControlsStatus below, because this
// component is an item in the page's action row and a flex item is as wide as its widest child.
// Dropping the `error` prop rather than merely not rendering it is deliberate: it makes the rule
// structural, so the row cannot regain a text line without a type error (quince#325).
export function BackupControls({
  device,
  activeJob,
  start,
  cancel,
  busy,
  storages,
  storageID,
  setStorageID,
}: BackupControlsProps) {
  const [transport, setTransport] = React.useState<RequestTransport>("auto");
  // storages is LIFTED to the page (see DeviceDetailsPage) so the control and the notices below
  // the row share one fetch. Same reason start/cancel/busy are lifted: two components, one truth.

  const onUSB = Boolean(device.transports.usb);
  const onWifi = Boolean(device.transports.wifi);
  const present = onUSB || onWifi;
  // The storage a RUNNING job is writing to, by name. Only named when the list is loaded and the
  // job carries an id we recognise — an unresolvable id says nothing rather than guessing, because
  // "to <the default>" would be a claim the job never made.
  const activeStorageName =
    activeJob?.storage_id && storages.state.status === "loaded"
      ? (storages.state.storages.find((s) => s.id === activeJob.storage_id)?.name ?? null)
      : null;


  if (activeJob) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" onClick={() => void cancel(activeJob.id)} data-testid="cancel-backup">
          Cancel backup
        </Button>
        {/* WHERE, not just HOW (Operator, 2026-08-02, from the G9 run). This said "backing up over
            wifi" and stopped there — which was the whole truth while there was one storage and is
            half of it now. `Job.storage_id` has been on the wire since story 6 and nothing
            rendered it, so a user with two disks watching a transfer could not tell which one was
            filling. Resolved through the storage list rather than shown as an id: the id is
            stable identity, the name is what the user wrote. */}
        <span className="text-xs text-muted" data-testid="active-job-line">
          backing up over {activeJob.transport}
          {activeStorageName ? ` to ${activeStorageName}` : ""}
        </span>
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
        <Button
          onClick={() => void start(transport, { storageID: storageID || undefined })}
          disabled={!present || busy}
          title={present ? undefined : "Connect the device over USB or Wi-Fi to back it up"}
          data-testid="backup-now"
        >
          {busy ? "Starting…" : "Back up now"}
        </Button>
        {onUSB && onWifi ? (
          // 16px on phones, 12px from `sm` up, label stepping with the control — the same shape as
          // `StorageSelect`, which carries the full reasoning (quince#616). At 12px this was a
          // 1.33x page zoom on tap, and it sits directly beside "Back up now" on the phone.
          <label className="text-base text-muted sm:text-xs">
            over{" "}
            <select
              className="rounded-md border border-line bg-card px-1.5 py-1 text-base text-fg sm:text-xs"
              value={transport}
              onChange={(e) => setTransport(e.target.value as RequestTransport)}
              aria-label="Backup transport"
            >
              <option value="auto">Auto</option>
              <option value="usb">USB</option>
              <option value="wifi">Wi-Fi</option>
            </select>
          </label>
        ) : null}
        <StorageSelect
          storages={storages}
          value={storageID}
          onChange={setStorageID}
          disabled={busy}
        />
    </div>
  );
}

// BackupControlsStatus is the block-level status under the action row: why the button is disabled,
// and any refusal from the last attempt.
//
// It is a SEPARATE component because of where it must render, not because of what it says. These
// lines used to sit inside BackupControls' own flex column, which is an item in the page's action
// row — and a flex item is as wide as its widest child. "Connect the device to back it up." is
// wider than "Back up now", so the column took the sentence's width and pushed "Manage encryption"
// out by the overhang, leaving a large gap between two buttons that should sit side by side.
//
// That is why the gap only ever appeared on an OFFLINE device: the sentence is the only thing that
// renders it, so a connected device had nothing to widen the column with (quince#325, reported from
// a screenshot). Keeping the text inside the row and constraining its width would trade one layout
// bug for a wrapped sentence under a narrow button; below the row it is free to be a full line, and
// the row is left holding only buttons.
export function BackupControlsStatus({
  storages,
  storageID,
  device,
  activeJob,
  error,
}: {
  device: Device;
  activeJob?: Job;
  error: string | null;
  storages: Storages;
  storageID: string;
}) {
  const present = Boolean(device.transports.usb || device.transports.wifi);
  return (
    <>
      {!activeJob && !present ? (
        <p className="text-xs text-muted">Connect the device to back it up.</p>
      ) : null}
      {/* The storage sentences render HERE, under the row, not beside the select — quince#325's
          rule, which StorageSelect had reintroduced a breach of. While a job runs they are
          suppressed: the row already says which storage is filling, and a full-transfer warning
          about a transfer in progress is a cost being reported after it started. */}
      {!activeJob ? <StorageNotices storages={storages} value={storageID} /> : null}
      {error ? (
        <p className="text-xs text-danger" role="alert">
          {error}
        </p>
      ) : null}
    </>
  );
}
