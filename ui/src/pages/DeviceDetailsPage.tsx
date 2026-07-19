import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useShallow } from "zustand/react/shallow";
import { ArrowLeft } from "lucide-react";
import type { Device } from "@/lib/types";
import { api } from "@/lib/api";
import { isRunning, useJobsStore } from "@/stores/jobs";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { modelName } from "@/features/devices/modelName";
import { JobProgressFull } from "@/features/jobs/JobProgress";
import { JobLogPane } from "@/features/jobs/JobLogPane";
import { JobHistory } from "@/features/jobs/JobHistory";
import { VersionList } from "@/features/versions/VersionList";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { formatRelativeTime } from "@/lib/format";

export function DeviceDetailsPage() {
  const { udid = "" } = useParams();
  const fromStore = useDevicesStore((s) => s.byUdid[udid]);

  // On a cold deep-link the store may be empty; fall back to a direct fetch.
  const q = useQuery({
    queryKey: ["device", udid],
    queryFn: () => api.get<Device>(`/api/devices/${udid}`),
    enabled: !fromStore && udid !== "",
  });
  const device = fromStore ?? q.data;

  const jobs = useJobsStore(useShallow((s) => Object.values(s.byId).filter((j) => j.udid === udid)));
  const versions = useVersionsStore(
    useShallow((s) => s.order.map((id) => s.byId[id]).filter((v) => v.udid === udid)),
  );
  const activeJob = jobs.find((j) => isRunning(j.state));

  return (
    <section>
      <Link to="/devices" className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
        <ArrowLeft size={16} /> All devices
      </Link>

      {!device ? (
        <div className="mt-6 text-sm text-muted">
          {q.isLoading ? "Loading…" : "This device is not currently connected."}
        </div>
      ) : (
        <>
          <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
            <div>
              <h1 className="text-xl font-semibold tracking-tight">{device.name || device.udid}</h1>
              <div className="text-sm text-muted">
                {modelName(device.model)} · iOS {device.ios_version} · seen{" "}
                {formatRelativeTime(device.last_seen)}
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Badge tone={device.paired === "yes" ? "ok" : "warn"}>paired: {device.paired}</Badge>
              <Badge tone={device.backup_encryption === "on" ? "ok" : "warn"}>
                encryption: {device.backup_encryption}
              </Badge>
            </div>
          </div>

          {device.backup_encryption === "off" ? (
            <div className="mt-4 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn">
              This device's backups are <strong>not encrypted</strong> — Health, Keychain, and
              saved passwords are omitted. Enabling encryption arrives with device management.
            </div>
          ) : null}

          <div className="mt-4 flex flex-wrap gap-2">
            <Button disabled title="Backups arrive in a later release">
              Back up now
            </Button>
            <Button variant="outline" disabled title="Device pairing arrives in a later release">
              Pair
            </Button>
            <Button variant="outline" disabled title="Encryption management arrives in a later release">
              Manage encryption
            </Button>
          </div>

          {activeJob ? (
            <div className="mt-6 flex flex-col gap-3">
              <JobProgressFull job={activeJob} />
              <JobLogPane jobId={activeJob.id} />
            </div>
          ) : null}

          <div className="mt-8">
            <h2 className="text-sm font-semibold text-muted">Backup history</h2>
            <div className="mt-3">
              <JobHistory jobs={jobs} />
            </div>
          </div>

          <div className="mt-8">
            <h2 className="text-sm font-semibold text-muted">Versions</h2>
            <div className="mt-3">
              <VersionList versions={versions} />
            </div>
          </div>
        </>
      )}
    </section>
  );
}
