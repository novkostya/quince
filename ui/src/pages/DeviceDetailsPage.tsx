import { useState } from "react";
import { Link, useLocation, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useShallow } from "zustand/react/shallow";
import { ArrowLeft } from "lucide-react";
import type { Device } from "@/lib/types";
import { api } from "@/lib/api";
import { isRunning, useJobsStore } from "@/stores/jobs";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { modelLine } from "@/features/devices/modelName";
import { PairDialog } from "@/features/devices/PairDialog";
import { WifiSyncControl } from "@/features/devices/WifiSyncControl";
import { EncryptionDialog, type EncryptionMode } from "@/features/devices/EncryptionDialog";
import { JobProgressFull } from "@/features/jobs/JobProgress";
import { JobLogPane } from "@/features/jobs/JobLogPane";
import { JobHistory } from "@/features/jobs/JobHistory";
import { BackupControls } from "@/features/jobs/BackupControls";
import { useBackup } from "@/features/jobs/useBackup";
import { VersionList } from "@/features/versions/VersionList";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

export function DeviceDetailsPage() {
  const { udid = "" } = useParams();
  const [encOpen, setEncOpen] = useState(false);
  const [encMode, setEncMode] = useState<EncryptionMode | undefined>(undefined);
  const fromStore = useDevicesStore((s) => s.byUdid[udid]);

  function openEncryption(mode?: EncryptionMode) {
    setEncMode(mode);
    setEncOpen(true);
  }

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
  const backup = useBackup(udid);
  // A pair intent deep-linked from the dashboard card (router state) auto-opens the pair dialog on
  // arrival — qn.4b fix for (bq), keeping qn.3's narrated-flow-on-details decision.
  const location = useLocation();
  const pairIntent = Boolean((location.state as { pair?: boolean } | null)?.pair);

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
              {modelLine(device.model, device.ios_version) ? (
                <div className="text-sm text-muted">{modelLine(device.model, device.ios_version)}</div>
              ) : null}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              {device.paired !== "unknown" ? (
                <Badge tone={device.paired === "yes" ? "ok" : "warn"}>paired: {device.paired}</Badge>
              ) : null}
              {device.backup_encryption !== "unknown" ? (
                <Badge tone={device.backup_encryption === "on" ? "ok" : "warn"}>
                  encryption: {device.backup_encryption}
                </Badge>
              ) : null}
              {/* "unknown" renders nothing, as with the two above — the honest state whenever
                  quince has not read the flag (an unconfirmed pairing, or a failed read).
                  This comment used to say the key was unmeasured and only --demo could show the
                  badge; both went false on 2026-07-31 when the key was measured on hardware and
                  a real device rendered `Wi-Fi sync: on`, then `off` across a Finder toggle. */}
              {device.wifi_sync !== "unknown" ? (
                <Badge tone={device.wifi_sync === "on" ? "ok" : "warn"}>
                  Wi-Fi sync: {device.wifi_sync}
                </Badge>
              ) : null}
            </div>
          </div>

          {device.backup_encryption === "off" ? (
            <div className="mt-4 flex flex-col gap-3 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn sm:flex-row sm:items-center sm:justify-between">
              <span>
                This device's backups are <strong>not encrypted</strong> — Health, Keychain, and
                saved passwords are omitted.
              </span>
              {device.paired === "yes" ? (
                <Button size="sm" onClick={() => openEncryption("enable")}>
                  Enable encryption
                </Button>
              ) : null}
            </div>
          ) : null}

          <div className="mt-4 flex flex-wrap items-start gap-3">
            {device.paired === "yes" ? (
              <>
                <BackupControls
                  device={device}
                  activeJob={activeJob}
                  start={backup.start}
                  cancel={backup.cancel}
                  busy={backup.busy}
                  error={backup.error}
                />
                <Button variant="outline" onClick={() => openEncryption()}>
                  Manage encryption
                </Button>
              </>
            ) : (
              <PairDialog udid={device.udid} autoOpen={pairIntent} />
            )}
          </div>

          {/* Below the action row, not inside it. BackupControls renders its own column with a
              status line under the button, so a third item in that flex-wrap row lands beside a
              two-line sibling and reads as indented rubble — which is how it looked on hardware.
              Its own block also matches its weight: a once-per-device setting, not an action. */}
          {device.paired === "yes" ? (
            <div className="mt-3">
              <WifiSyncControl device={device} />
            </div>
          ) : null}

          <EncryptionDialog
            udid={device.udid}
            encryption={device.backup_encryption}
            open={encOpen}
            onOpenChange={setEncOpen}
            initialMode={encMode}
          />

          {activeJob ? (
            <div className="mt-6 flex flex-col gap-3">
              <JobProgressFull job={activeJob} />
              <JobLogPane jobId={activeJob.id} />
            </div>
          ) : null}

          <div className="mt-8">
            <h2 className="text-sm font-semibold text-muted">Backup history</h2>
            <div className="mt-3">
              <JobHistory jobs={jobs} onRetry={(latest) => void backup.start("auto", latest.id)} />
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
