import { SectionHeading } from "@/components/ui/section-heading";
import { useEffect, useRef, useState } from "react";
import { useLocation, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useShallow } from "zustand/react/shallow";
import { ArrowLeft } from "lucide-react";
import { BackLink } from "@/components/BackLink";
import type { Device } from "@/lib/types";
import { api } from "@/lib/api";
import { newestRunningJob, useJobsStore } from "@/stores/jobs";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { modelLine } from "@/features/devices/modelName";
import {
  encryptionBlocksBackup,
  unencryptedConsequence,
} from "@/features/devices/encryptionPolicy";
import { PairDialog } from "@/features/devices/PairDialog";
import { WifiSyncControl } from "@/features/devices/WifiSyncControl";
import { DeviceNotificationsControl } from "@/features/devices/DeviceNotificationsControl";
import { DeviceEnrolment } from "@/features/devices/DeviceEnrolment";
import { DeviceCredentials } from "@/features/devices/DeviceCredentials";
import { EncryptionDialog, type EncryptionMode } from "@/features/devices/EncryptionDialog";
import { JobProgressFull } from "@/features/jobs/JobProgress";
import { JobLogPane } from "@/features/jobs/JobLogPane";
import { JobHistory } from "@/features/jobs/JobHistory";
import { BackupControls, BackupControlsStatus } from "@/features/jobs/BackupControls";
import { useStorages } from "@/features/jobs/useStorages";
import { useConfig } from "@/lib/config";
import { useDialogRoute } from "@/lib/useDialogRoute";
import { useBackup } from "@/features/jobs/useBackup";
import { VersionList } from "@/features/versions/VersionList";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

export function DeviceDetailsPage() {
  const { udid = "" } = useParams();
  // The dialog is a place, not a boolean — opening it pushes a history entry and closing it
  // pops one, so Back closes it and the browser restores the page offset behind it (quince#931).
  const { open: encOpen, onOpenChange: setEncOpen } = useDialogRoute("encryption");
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
  const activeJob = newestRunningJob(jobs);
  const backup = useBackup(udid);
  // LIFTED HERE so the action row's control and the sentences beneath it share one fetch and one
  // selection (quince#325's rule: the row holds controls, prose goes in the block below — which
  // means the two halves live in different components and must not disagree).
  const storages = useStorages(udid);
  const [storageID, setStorageID] = useState<string>("");

  // A FINISHED JOB INVALIDATES THE STORAGE LIST. `will_be_full` is true only until the first
  // backup to a storage lands, so a page that fetched once keeps advertising a cost that has been
  // paid — measured on the staging stand during G9, where "First backup to shuttle" was still on
  // screen after the transfer it described had committed.
  //
  // Keyed on the job's IDENTITY going away rather than on a terminal state name: `isRunning` is
  // already the one place that decides what "running" means, and re-deriving it here would be a
  // second definition to keep in step.
  const activeJobID = activeJob?.id ?? null;
  const prevJobID = useRef<string | null>(null);
  useEffect(() => {
    if (prevJobID.current && !activeJobID) storages.reload();
    prevJobID.current = activeJobID;
  }, [activeJobID, storages]);

  // ENCRYPTION OFF + `require_encryption` = A BACKUP THAT CANNOT START, and the page has both facts
  // already (quince#889, Operator ruling 2026-08-13: the guard is the UI's, the API is unchanged).
  // The rule itself lives in `encryptionPolicy` because three surfaces render off it.
  const config = useConfig();
  const encryptionBlocks = encryptionBlocksBackup(device, config.data);

  // A pair intent deep-linked from the dashboard card (router state) auto-opens the pair dialog on
  // arrival — qn.4b fix for (bq), keeping qn.3's narrated-flow-on-details decision.
  const location = useLocation();
  const pairIntent = Boolean((location.state as { pair?: boolean } | null)?.pair);

  return (
    <section>
      <BackLink to="/" className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
        <ArrowLeft size={16} /> Home
      </BackLink>

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
              {/* WHAT FOLLOWS DEPENDS ON THE POLICY, and the old single sentence was false under
                  half of it: with `require_encryption: true` nothing is omitted, because nothing is
                  backed up (quince#889). The sentence is composed in `encryptionPolicy` so this
                  page and its test read one string rather than two copies of it. */}
              <span>
                This device's backups are <strong>not encrypted</strong>
                {unencryptedConsequence(encryptionBlocks)}
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
                  storages={storages}
                  storageID={storageID}
                  setStorageID={setStorageID}
                  encryptionBlocks={encryptionBlocks === true}
                />
                <Button variant="outline" onClick={() => openEncryption()}>
                  Manage encryption
                </Button>
              </>
            ) : (
              <PairDialog udid={device.udid} autoOpen={pairIntent} />
            )}
          </div>

          {/* The action row holds BUTTONS and nothing else. Any text that stacks under a button
              belongs here instead, because a flex item is as wide as its widest child: a status
              line left inside the row silently sets its column's width and pushes the next button
              out by the overhang (quince#325). */}
          {device.paired === "yes" ? (
            <div className="mt-2 flex flex-col gap-1">
              <BackupControlsStatus
                device={device}
                activeJob={activeJob}
                error={backup.error}
                storages={storages}
                storageID={storageID}
                encryptionBlocks={encryptionBlocks === true}
              />
            </div>
          ) : null}

          {/* Below the action row, not inside it. A third item in that flex-wrap row lands beside a
              two-line sibling and reads as indented rubble — which is how it looked on hardware.
              Its own block also matches its weight: a once-per-device setting, not an action. */}
          {device.paired === "yes" ? (
            <div className="mt-3">
              <WifiSyncControl device={device} />
            </div>
          ) : null}

          {/* NOT GATED ON `paired`, unlike the two controls above. Those reach the phone and a
              lockdown write needs a trusted session; this writes one row in quince's own
              database. The device this feature exists for — one that is paired, visible, and
              never going to be backed up — must be settable, and so must an OFFLINE device
              (qn.6a), which is a phone in a drawer by another name. */}
          <div className="mt-3">
            <DeviceNotificationsControl device={device} />
          </div>

          <EncryptionDialog
            udid={device.udid}
            // Raw, not `device.name || device.udid` — the dialog owns that fallback (quince#819).
            deviceName={device.name}
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
            <SectionHeading>Backup history</SectionHeading>
            <div className="mt-3">
              <JobHistory jobs={jobs} onRetry={(latest) => void backup.start("auto", { retryOf: latest.id })} />
            </div>
          </div>

          <div className="mt-8">
            {/* THE ENROLMENT SECTION SITS ON THE DEVICE PAGE because that is what it is scoped to
                — D9's "the admin revokes one scoped credential from the device page it was issued
                from". It is admin-only at the API; a scoped holder never reaches this page's own
                admin surface, and the routes refuse them regardless. */}
            <SectionHeading>Share this device</SectionHeading>
            <div className="mt-3">
              <DeviceEnrolment device={device} />
              {/* WHO ALREADY HOLDS ONE, under the control that hands them out — D9. `DeviceEnrolment`
                  lists authority handed out and not yet used; this lists authority in use. Either
                  half alone is a confident, incomplete answer to *what have I issued*. */}
              <DeviceCredentials device={device} />
            </div>
          </div>

          <div className="mt-8">
            <SectionHeading>Versions</SectionHeading>
            <div className="mt-3">
              <VersionList versions={versions} />
            </div>
          </div>
        </>
      )}
    </section>
  );
}
