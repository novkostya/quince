import { Link } from "react-router-dom";
import { ShieldAlert, ShieldCheck, Usb, Wifi, WifiOff } from "lucide-react";
import type { Device, Job } from "@/lib/types";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { modelLine } from "./modelName";
import { RelativeTime } from "@/components/RelativeTime";
import { isRunning, useJobsStore } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";
import { JobProgressInline } from "@/features/jobs/JobProgress";
import { useBackup } from "@/features/jobs/useBackup";

function EncryptionBadge({ state }: { state: Device["backup_encryption"] }) {
  if (state === "on") {
    return (
      <Badge tone="ok">
        <ShieldCheck size={12} /> Encrypted
      </Badge>
    );
  }
  if (state === "off") {
    return (
      <Badge tone="warn">
        <ShieldAlert size={12} /> Not encrypted
      </Badge>
    );
  }
  return null; // "unknown" (muxd-minimal, before qn.3 lockdown) — no badge
}

// BackupStatus is the one line under the transports: real history if any (with a live, hover-exact
// timestamp), else a state-appropriate placeholder.
function BackupStatus({ device }: { device: Device }) {
  if (device.last_backup) {
    return (
      <>
        Last backup <RelativeTime iso={device.last_backup.at} /> · {device.last_backup.status}
      </>
    );
  }
  return <>{device.paired === "yes" ? "No backups yet" : "Not set up yet"}</>;
}

// isFailed marks a terminal attempt the user should act on (assisted model — a failed newest attempt
// must be visible or the soak is worthless, gate-11 finding #6, (cj)).
function isFailed(state: Job["state"]): boolean {
  return state === "failed" || state === "connection_lost";
}

export function DeviceCard({ device }: { device: Device }) {
  const jobsForDevice = (s: { byId: Record<string, Job> }) =>
    Object.values(s.byId).filter((j) => j.udid === device.udid);
  const activeJob = useJobsStore((s) => jobsForDevice(s).find((j) => isRunning(j.state)));
  // The newest attempt for the device (by start time) — its failure drives the "needs attention" line.
  const newestJob = useJobsStore((s) =>
    jobsForDevice(s).reduce<Job | undefined>(
      (newest, j) => (!newest || j.started_at > newest.started_at ? j : newest),
      undefined,
    ),
  );
  // Non-missing versions this device actually holds (the card's "N backups" count, qn.6a).
  const versionCount = useVersionsStore(
    (s) => s.order.filter((id) => s.byId[id]?.udid === device.udid && !s.byId[id]?.missing).length,
  );

  const { start, busy, error } = useBackup(device.udid);
  const present = Boolean(device.transports.usb || device.transports.wifi);
  const subtitle = modelLine(device.model, device.ios_version);
  // Surface a failed newest attempt only when nothing is currently running (a running job already
  // narrates itself) and last_backup (last SUCCESS) doesn't cover it.
  const attention = !activeJob && newestJob && isFailed(newestJob.state) ? newestJob : undefined;

  return (
    <Card data-testid="device-card">
      <CardContent className="p-4 sm:p-5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <Link
              to={`/devices/${device.udid}`}
              className="text-sm font-semibold tracking-tight hover:text-accent"
            >
              {device.name || device.udid}
            </Link>
            {subtitle ? <div className="truncate text-xs text-muted">{subtitle}</div> : null}
          </div>
          <EncryptionBadge state={device.backup_encryption} />
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          {device.transports.usb ? (
            <Badge tone="ok">
              <Usb size={12} /> USB
            </Badge>
          ) : null}
          {device.transports.wifi ? (
            <Badge tone="ok">
              <Wifi size={12} /> Wi-Fi
            </Badge>
          ) : null}
          {!present ? (
            <Badge tone="neutral">
              <WifiOff size={12} /> Offline
            </Badge>
          ) : null}
        </div>

        <div className="mt-3 text-xs text-muted">
          <BackupStatus device={device} />
        </div>
        <div className="mt-1 flex flex-wrap gap-x-3 text-xs text-subtle">
          <span>
            {versionCount} {versionCount === 1 ? "backup" : "backups"}
          </span>
          {!present && device.last_seen ? (
            <span>
              last seen <RelativeTime iso={device.last_seen} />
            </span>
          ) : null}
        </div>

        {/* Exactly ONE primary action per card (qn.6a soak fix — a "needs attention" line PLUS a
            separate "Back up now" was two buttons doing the same thing). When the newest attempt
            failed, Retry IS that action and replaces Back up now, with the failure as context. */}
        <div className="mt-4">
          {activeJob ? (
            <JobProgressInline job={activeJob} />
          ) : !present ? (
            // Offline: a disabled "Back up now" WITH a reason (never a dead button), same shape as an
            // online card so the layout stays aligned (Operator ruling, (ch)/(bq)). The reason is
            // shown inline too — a hover title alone is invisible on a phone.
            <div className="flex flex-col gap-1">
              <Button
                size="sm"
                disabled
                title="Connect the device to back it up"
                data-testid="card-backup-now"
              >
                Back up now
              </Button>
              <span className="text-xs text-muted">Connect it over USB or Wi-Fi to back it up.</span>
            </div>
          ) : device.paired !== "yes" ? (
            // Pairing is USB-only and narrated (Trust + passcode), so it lives on the device's
            // details page (qn.3); the card routes there carrying a pair INTENT (router state) so the
            // details page auto-opens the dialog — the click delivers on its label (qn.4b fix, (bq)).
            <Button asChild size="sm" variant="outline">
              <Link to={`/devices/${device.udid}`} state={{ pair: true }}>
                Pair
              </Link>
            </Button>
          ) : attention ? (
            <div className="flex flex-col gap-1.5">
              <span className="text-xs text-danger">Last attempt needs attention</span>
              <Button
                size="sm"
                onClick={() => void start("auto", attention.id)}
                disabled={busy}
                data-testid="card-retry"
              >
                {busy ? "Starting…" : "Retry backup"}
              </Button>
              {error ? (
                <span className="text-xs text-danger" role="alert">
                  {error}
                </span>
              ) : null}
            </div>
          ) : (
            <div className="flex flex-col gap-1">
              <Button
                size="sm"
                onClick={() => void start("auto")}
                disabled={busy}
                data-testid="card-backup-now"
              >
                {busy ? "Starting…" : "Back up now"}
              </Button>
              {error ? (
                <span className="text-xs text-danger" role="alert">
                  {error}
                </span>
              ) : null}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
