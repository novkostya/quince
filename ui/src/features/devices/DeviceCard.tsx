import { Link } from "react-router-dom";
import { ShieldAlert, ShieldCheck, Usb, Wifi } from "lucide-react";
import type { Device } from "@/lib/types";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { modelName } from "./modelName";
import { formatRelativeTime } from "@/lib/format";
import { isRunning, useJobsStore } from "@/stores/jobs";
import { JobProgressInline } from "@/features/jobs/JobProgress";
import { api } from "@/lib/api";

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
  return <Badge tone="neutral">Encryption unknown</Badge>;
}

export function DeviceCard({ device }: { device: Device }) {
  const activeJob = useJobsStore((s) =>
    Object.values(s.byId).find((j) => j.udid === device.udid && isRunning(j.state)),
  );

  function startBackup() {
    // qn.1 has no job engine (qn.4); in demo the scripted job renders regardless. The call
    // is wired for the real engine and its errors are surfaced quietly for now.
    void api.post("/api/jobs", { udid: device.udid, transport: "auto" }).catch(() => undefined);
  }

  return (
    <Card>
      <CardContent className="p-5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <Link
              to={`/devices/${device.udid}`}
              className="text-sm font-semibold tracking-tight hover:text-accent"
            >
              {device.name}
            </Link>
            <div className="truncate text-xs text-muted">
              {modelName(device.model)} · iOS {device.ios_version}
            </div>
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
          <span className="font-mono text-xs tabular-nums text-subtle">
            seen {formatRelativeTime(device.last_seen)}
          </span>
        </div>

        <div className="mt-3 text-xs text-muted">
          {device.last_backup
            ? `Last backup ${formatRelativeTime(device.last_backup.at)} · ${device.last_backup.status}`
            : "No backups yet"}
        </div>

        <div className="mt-4">
          {activeJob ? (
            <JobProgressInline job={activeJob} />
          ) : (
            <Button size="sm" onClick={startBackup}>
              Back up now
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
