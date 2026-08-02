import * as React from "react";
import type { Device, Job } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { StorageSelect } from "./StorageSelect";
import { useStorages } from "./useStorages";
import type { RequestTransport } from "./useBackup";

interface BackupControlsProps {
  device: Device;
  activeJob?: Job;
  start: (transport: RequestTransport, storageID?: string, retryOf?: string) => Promise<boolean>;
  cancel: (jobId: string) => Promise<boolean>;
  busy: boolean;
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
export function BackupControls({ device, activeJob, start, cancel, busy }: BackupControlsProps) {
  const [transport, setTransport] = React.useState<RequestTransport>("auto");
  const storages = useStorages(device.udid);
  const [storageID, setStorageID] = React.useState<string>("");

  const onUSB = Boolean(device.transports.usb);
  const onWifi = Boolean(device.transports.wifi);
  const present = onUSB || onWifi;

  if (activeJob) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" onClick={() => void cancel(activeJob.id)} data-testid="cancel-backup">
          Cancel backup
        </Button>
        <span className="text-xs text-muted">backing up over {activeJob.transport}</span>
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
        <Button
          onClick={() => void start(transport, storageID || undefined)}
          disabled={!present || busy}
          title={present ? undefined : "Connect the device over USB or Wi-Fi to back it up"}
          data-testid="backup-now"
        >
          {busy ? "Starting…" : "Back up now"}
        </Button>
        {onUSB && onWifi ? (
          <label className="text-xs text-muted">
            over{" "}
            <select
              className="rounded-md border border-line bg-card px-1.5 py-1 text-xs text-fg"
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
  device,
  activeJob,
  error,
}: {
  device: Device;
  activeJob?: Job;
  error: string | null;
}) {
  const present = Boolean(device.transports.usb || device.transports.wifi);
  return (
    <>
      {!activeJob && !present ? (
        <p className="text-xs text-muted">Connect the device to back it up.</p>
      ) : null}
      {error ? (
        <p className="text-xs text-danger" role="alert">
          {error}
        </p>
      ) : null}
    </>
  );
}
