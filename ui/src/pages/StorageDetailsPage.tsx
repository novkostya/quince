import { Link, useParams } from "react-router-dom";
import { ArrowLeft, AlertTriangle } from "lucide-react";
import { useShallow } from "zustand/react/shallow";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { useStorages } from "@/features/jobs/useStorages";
import { VersionList } from "@/features/versions/VersionList";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import { RelativeTime } from "@/components/RelativeTime";
import { formatBytes } from "@/lib/format";
import { modelLine } from "@/features/devices/modelName";
import type { Version } from "@/lib/types";

// versionsOn scopes a version list to ONE storage, and is exported so it can be tested without
// mounting the page — the convention `DeviceDetailsWifiSync.test.tsx` sets, where the page's own
// render is covered by e2e and the RULE it implements is pinned here.
//
// AN EMPTY STORAGE ID MATCHES NOTHING, deliberately. `Version.storage_id` is null until attributed,
// and `""` means the storage was never created (quince#582) — so a naive filter would pair every
// unattributed version with every never-created storage and invent a history for both. A storage
// that was never created genuinely has no backups, so the empty result is the right answer.
export function versionsOn(storageID: string, versions: Version[]): Version[] {
  if (storageID === "") return [];
  return versions.filter((v) => v.storage_id === storageID);
}

// StorageDetailsPage is one storage as an OBJECT rather than a path (qn.6d stories 5 and 7).
//
// Routed on `name`, not `id`: the API addresses a storage by its config name (quince#570, ruled
// 2026-08-02), and a name exists for every declared storage whether or not quince has ever reached
// it. An id does not — a storage that was never created has none.
export function StorageDetailsPage() {
  const { name = "" } = useParams();
  const storages = useStorages("");
  const list = storages.state.status === "loaded" ? storages.state.storages : [];
  const storage = list.find((s) => s.name === name);

  const devicesOrder = useDevicesStore(useShallow((s) => s.order));
  const devicesByUdid = useDevicesStore((s) => s.byUdid);
  const allVersions = useVersionsStore(useShallow((s) => s.order.map((id) => s.byId[id])));

  if (storages.state.status === "loading") {
    return <div className="text-sm text-muted">Loading…</div>;
  }
  if (!storage) {
    // A name that is not declared. Says which, because "not found" about a storage the user just
    // typed or bookmarked is the least useful thing a page can say.
    return (
      <section>
        <BackLink />
        <div className="mt-4 rounded-card border border-dashed border-line bg-card p-10 text-center">
          <div className="text-sm font-medium">No storage named {name}</div>
          <div className="mt-1 text-sm text-muted">
            It may have been removed from <code>config.yml</code>, or renamed.
          </div>
        </div>
      </section>
    );
  }

  // Versions are scoped by storage_id, which is why quince#582 had to land first: an unplugged disk
  // used to reach the wire with id "" and could not be matched to its own history — the moment a
  // user most wants to see what is on it. A storage that was NEVER created still has no id, and
  // correctly has no versions either, so the empty match is the right answer rather than a gap.
  const versions = versionsOn(storage.id, allVersions);

  // EVERY declared device is listed, including those with nothing here — story 8's argument, and the
  // whole 3-2-1 case. The action this page exists to make easy is "start backing that device up here
  // too", and it is invisible if those devices are hidden.
  const devices = devicesOrder
    .map((udid) => devicesByUdid[udid])
    .filter((d): d is NonNullable<typeof d> => Boolean(d))
    .map((d) => ({ device: d, here: versions.filter((v) => v.udid === d.udid) }));

  const free = storage.filesystem_free_bytes;
  const total = storage.filesystem_total_bytes;
  // USED, not free — the bar fills as the disk fills (PBS and Windows Explorer both do this).
  // Rendering the free fraction showed an EMPTY storage as a 100%-full bar on staging.
  const pct =
    free !== null && total !== null && total > 0 ? (Math.max(0, total - free) / total) * 100 : null;

  return (
    <section>
      <BackLink />

      <div className="mt-4">
        <h1 className="text-xl font-semibold tracking-tight">{storage.name}</h1>
        {storage.name !== storage.path ? (
          <p className="mt-1 text-sm text-muted">{storage.path}</p>
        ) : null}
      </div>

      {/* THE STATUS HEADER — quince-storage.json rendered, which is what makes a storage an object.
          It branches on `reachable` and falls back to the daemon's own prose rather than mapping
          `unreachable_code` to a remedy of its own: the code's declared values are WRONG today
          (quince#569 — the daemon also emits `unreachable` and `corrupt_marker`, neither in the
          enum), so a client that switched on it would silently default for the commonest failure
          there is, an unmounted disk. The reason always says what happened; the code does not. */}
      <div className="mt-4 flex flex-wrap items-center gap-2">
        {storage.reachable ? (
          <Badge tone="ok">connected</Badge>
        ) : (
          <Badge tone="warn">
            <AlertTriangle size={12} /> not connected
          </Badge>
        )}
        <Badge tone="neutral">{storage.backend}</Badge>
        {storage.default ? <Badge tone="neutral">Default</Badge> : null}
      </div>

      {!storage.reachable && storage.unreachable_reason ? (
        <div
          data-testid="storage-detail-reason"
          className="mt-3 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn"
        >
          {storage.unreachable_reason}
        </div>
      ) : null}

      <dl className="mt-4 grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
        <Row label="Path" value={<code className="text-xs">{storage.path}</code>} />
        <Row label="Backend" value={storage.backend} />
        <Row
          label="Storage ID"
          value={
            storage.id === "" ? (
              // Empty means NEVER CREATED, not "not currently readable" — quince#582 made the id
              // survive a replug, so an absent one now says something definite.
              <span className="text-muted">not yet created — quince has never reached this path</span>
            ) : (
              <code className="text-xs" data-testid="storage-detail-id">
                {storage.id}
              </code>
            )
          }
        />
        <Row
          label="Counts as of"
          value={<RelativeTime iso={storage.counts_as_of} />}
        />
      </dl>

      <h2 className="mt-8 text-sm font-semibold text-muted">Space</h2>
      <div className="mt-3">
        {pct === null || free === null || total === null ? (
          <div className="text-sm text-muted">
            {storage.reachable ? "free space unavailable" : "no measurement while disconnected"}
          </div>
        ) : (
          <>
            {/* Plain, with no filesystem caveat — the gap A ruling, same as the card. */}
            <div data-testid="storage-detail-space" className="text-sm">
              <span className="font-medium tabular-nums">{formatBytes(free)}</span>{" "}
              <span className="text-muted">
                free of <span className="tabular-nums">{formatBytes(total)}</span>
              </span>
            </div>
            <div className="mt-2 flex max-w-md items-center gap-2">
              <Progress percent={pct} className="h-2.5 flex-1" />
              <span className="w-9 shrink-0 text-right text-xs tabular-nums text-subtle">
                {Math.round(pct)}%
              </span>
            </div>
          </>
        )}
      </div>

      <h2 className="mt-8 text-sm font-semibold text-muted">Devices</h2>
      <div className="mt-3 space-y-2">
        {devices.length === 0 ? (
          <div className="text-sm text-muted">No devices known yet.</div>
        ) : (
          devices.map(({ device, here }) => (
            <div
              key={device.udid}
              data-testid="storage-device-row"
              data-udid={device.udid}
              className="rounded-card border border-line bg-card p-3"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <Link
                    to={`/devices/${device.udid}`}
                    className="text-sm font-medium hover:text-accent"
                  >
                    {device.name || device.udid}
                  </Link>
                  <div className="truncate text-xs text-muted">
                    {modelLine(device.model, device.ios_version)}
                  </div>
                </div>
                <span className="shrink-0 text-xs tabular-nums text-subtle">
                  {here.length} {here.length === 1 ? "backup" : "backups"} here
                </span>
              </div>
              {here.length === 0 ? (
                // STORY 8's warning, inline and BEFORE the cost is paid. A device with nothing here
                // is shown rather than filtered out precisely so this sentence has somewhere to be.
                <div data-testid="storage-device-will-be-full" className="mt-2 text-xs text-warn">
                  no backups here yet — the first will be a full transfer
                </div>
              ) : null}
            </div>
          ))
        )}
      </div>

      <h2 className="mt-8 text-sm font-semibold text-muted">Backups here</h2>
      <div className="mt-3">
        {versions.length === 0 ? (
          <div className="text-sm text-muted">No backups on this storage yet.</div>
        ) : (
          // showDevice: this list mixes devices, so each row names its own — the cross-link the
          // spec asks for, in the direction this page needs.
          <VersionList versions={versions} showDevice />
        )}
      </div>
    </section>
  );
}

function BackLink() {
  return (
    <Link to="/devices" className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
      <ArrowLeft size={14} /> Home
    </Link>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex gap-2">
      <dt className="shrink-0 text-muted">{label}</dt>
      <dd className="min-w-0 break-all">{value}</dd>
    </div>
  );
}
