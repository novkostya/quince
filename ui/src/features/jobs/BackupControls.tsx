import * as React from "react";
import type { Device, Job } from "@/lib/types";
import { Button } from "@/components/ui/button";
import type { RequestTransport } from "./useBackup";

interface BackupControlsProps {
  device: Device;
  activeJob?: Job;
  start: (transport: RequestTransport, retryOf?: string) => Promise<boolean>;
  cancel: (jobId: string) => Promise<boolean>;
  busy: boolean;
  error: string | null;
}

// BackupControls is the assisted "Back up now" action on a device's details page. It starts a backup
// over the chosen transport (default "auto" — the engine resolves it, design §4/(bp)), offers a
// transport override only when the device is present on both, cancels a running job, and surfaces
// refusals honestly (device offline, already running, no engine — the shared error). The
// started/cancelled job renders from the WS job.updated stream; this never fabricates progress
// (ui.design.md). start/cancel/busy/error are lifted to the page so Retry shares the same state.
export function BackupControls({ device, activeJob, start, cancel, busy, error }: BackupControlsProps) {
  const [transport, setTransport] = React.useState<RequestTransport>("auto");

  const onUSB = Boolean(device.transports.usb);
  const onWifi = Boolean(device.transports.wifi);
  const present = onUSB || onWifi;

  const errorLine = error ? (
    <p className="text-xs text-danger" role="alert">
      {error}
    </p>
  ) : null;

  if (activeJob) {
    return (
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" onClick={() => void cancel(activeJob.id)} data-testid="cancel-backup">
            Cancel backup
          </Button>
          <span className="text-xs text-muted">backing up over {activeJob.transport}</span>
        </div>
        {errorLine}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          onClick={() => void start(transport)}
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
      </div>
      {!present ? <p className="text-xs text-muted">Connect the device to back it up.</p> : null}
      {errorLine}
    </div>
  );
}
