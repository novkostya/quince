import { Link } from "react-router-dom";
import { ShieldAlert, ShieldCheck, Usb, Wifi } from "lucide-react";
import type { Device } from "@/lib/types";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { modelLine } from "./modelName";
import { formatRelativeTime } from "@/lib/format";
import { isRunning, useJobsStore } from "@/stores/jobs";
import { JobProgressInline } from "@/features/jobs/JobProgress";

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

// backupStatus is the one line under the transports: real history if any, else a
// state-appropriate placeholder ("No backups yet" for a paired device, "Not set up yet" for
// one we can't act on yet).
function backupStatus(device: Device): string {
  if (device.last_backup) {
    return `Last backup ${formatRelativeTime(device.last_backup.at)} · ${device.last_backup.status}`;
  }
  return device.paired === "yes" ? "No backups yet" : "Not set up yet";
}

export function DeviceCard({ device }: { device: Device }) {
  const activeJob = useJobsStore((s) =>
    Object.values(s.byId).find((j) => j.udid === device.udid && isRunning(j.state)),
  );
  const subtitle = modelLine(device.model, device.ios_version);

  return (
    <Card>
      <CardContent className="p-5">
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
        </div>

        <div className="mt-3 text-xs text-muted">{backupStatus(device)}</div>

        <div className="mt-4">
          {activeJob ? (
            <JobProgressInline job={activeJob} />
          ) : device.paired === "yes" ? (
            <Button size="sm" disabled title="Backups arrive in a later release">
              Back up now
            </Button>
          ) : (
            <Button size="sm" disabled title="Device pairing arrives in a later release">
              Pair
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
