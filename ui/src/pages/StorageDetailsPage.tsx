import { Link, useParams } from "react-router-dom";
import { ArrowLeft, AlertTriangle } from "lucide-react";
import { useShallow } from "zustand/react/shallow";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { useStorages } from "@/features/jobs/useStorages";
import { VersionList } from "@/features/versions/VersionList";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { formatBytes } from "@/lib/format";
import { modelLine } from "@/features/devices/modelName";
import { StorageDeviceBackup } from "@/features/storage/StorageDeviceBackup";
import { StorageProblem } from "@/features/storage/StorageProblem";
import { ForgetStorage } from "@/features/storage/ForgetStorage";
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
  //
  // quince#582 was NOT sufficient, and this page is where that showed: it covered the
  // missing-medium refusal only, so an unplugged USB — which fails at the marker READ, not the
  // reachability check — still arrived with id "" and hit `versionsOn`'s early return. The page
  // then said "No backups on this storage yet" about a disk full of them. quince#652 makes every
  // refusal carry the id, so this scoping now works whenever the DB knows the storage.
  const versions = versionsOn(storage.id, allVersions);

  // EVERY declared device is listed, including those with nothing here. The action this page exists
  // to make easy is "start backing that device up here too" — the whole 3-2-1 case — and it is
  // invisible if those devices are hidden (quince#443).
  //
  // THE BEHAVIOUR OUTLIVED ITS ORIGINAL JUSTIFICATION, which is why this comment no longer cites
  // story 8. A device with nothing here used to be shown partly "so this sentence has somewhere to
  // be", and that sentence has been deleted (quince#630). The reason above is the one that was
  // always load-bearing, and it stands on its own.
  const devices = devicesOrder
    .map((udid) => devicesByUdid[udid])
    .filter((d): d is NonNullable<typeof d> => Boolean(d))
    // `here` EXCLUDES MISSING VERSIONS, and that is a fix rather than a filter (quince#624).
    //
    // A missing version's registry row survives but its artifact is gone, so counting it tells the
    // user they have a backup they cannot restore. `DeviceCard` already excluded them from its own
    // "N backups", and the storage's `backup_count` excludes them on the wire — this row was the
    // one surface that did not, so a storage holding one dead version reported one more backup here
    // than its own header did. Found by the e2e that sums these rows against that header: 19 vs 18.
    //
    // The version LIST below is deliberately NOT filtered this way. A dead version renders there,
    // explicitly dead, with a Remove action (qn.6a) — history is not silently shrunk. It is the
    // COUNT that must not claim it.
    .map((d) => ({ device: d, here: versions.filter((v) => v.udid === d.udid && !v.missing) }));

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

      {/* THE DIAGNOSIS AND ITS REMEDY, TOGETHER, ON THE STORAGE'S OWN PAGE (quince#627). Both used
          to render under a DEVICE's action row — a disk's mount state, and a button that re-probes
          it, on a screen about a phone. `Re-check` arrives here with the sentence it belongs to. */}
      <StorageProblem storage={storage} storages={storages} />

      {/* ONE ROW, because the other two were saying things the page had already said.
          `Path` repeated the subtitle directly under the title, and `Backend` repeated the badge
          two lines above it — and in a two-column grid the leftover `Backend zfs` sat alone
          against the right edge with nothing beside it, which is what made the duplication look
          like a layout bug rather than a content one. The status header still shows all five
          facts the spec asks for; path is the subtitle, backend and reachability are badges, and
          this is the one fact nothing else carries. */}
      <dl className="mt-4 text-sm">
        <Row
          label="Storage ID"
          value={
            storage.id === "" ? (
              // Empty means NEVER CREATED, not "not currently readable" — so an absent id says
              // something definite.
              //
              // THAT BECAME TRUE IN TWO STEPS, AND THIS COMMENT ASSERTED IT AFTER ONLY THE FIRST.
              // quince#582 carried the id on the missing-medium path; every OTHER way of failing to
              // read the disk still lost it, so an unplugged USB rendered this sentence — a claim
              // about history, made from the absence of a file — about a disk quince had been
              // backing up to for months. quince#652 moved the DB lookup to the front of
              // ResolveStorage so every refusal carries the id, which is what makes branching on
              // the line above safe.
              <span className="text-muted">not yet created — quince has never reached this path</span>
            ) : (
              <code className="text-xs" data-testid="storage-detail-id">
                {storage.id}
              </code>
            )
          }
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
      {/* CARDS IN A GRID, the same shape Home uses for devices and storages. Full-width rows made
          each device look like a list entry rather than a peer object, and on a wide screen the
          backup count drifted a long way from the name it belongs to — visible on the staging
          stand, which is where this came from. `Card` + `h-full` + `mt-auto` is DeviceCard's own
          idiom, kept so the two card kinds line up. */}
      <div className="mt-3 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {devices.length === 0 ? (
          <div className="text-sm text-muted">No devices known yet.</div>
        ) : (
          devices.map(({ device, here }) => (
            <Card
              key={device.udid}
              data-testid="storage-device-row"
              data-udid={device.udid}
              className="flex h-full flex-col"
            >
              <CardContent className="flex flex-1 flex-col p-4">
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
                {/* Its own testid so the count can be read as a VALUE rather than scraped out of
                    the row's text. `textContent` concatenates siblings with no separator, so the
                    model line above ends "…iOS 26.0.1" and runs straight into "15 backups here" —
                    a regex over the row then reads 115. Measured, on the assertion that exists to
                    prove these numbers agree (quince#624). */}
                <span
                  data-testid="storage-device-count"
                  className="shrink-0 text-xs tabular-nums text-subtle"
                >
                  {here.length} {here.length === 1 ? "backup" : "backups"} here
                </span>
              </div>
              {/* STORY 8'S WARNING USED TO RENDER HERE AND IS GONE — one render site, not two
                  (quince#630). The warning itself is untouched and still shows in the device action
                  area, where the server supplies it as `will_be_full` for the chosen storage.

                  What was deleted is a second DERIVATION, not a second consumer: this copy made the
                  same claim from `here.length === 0` — a client-side count of versions — and never
                  read `will_be_full` at all. Two sources for one claim, with a `data-testid`
                  asserting they were the same thing. qn.6d's G2 cannot reach it by construction,
                  because G2 asserts on the API answer AND the marker, "so a UI-only claim cannot
                  pass it".

                  If this page should ever carry the claim again it must CONSUME `will_be_full`,
                  which means a device-scoped fetch: it is a (device, storage) fact the server only
                  supplies when asked about one device. Counting is what made it ungateable. */}
              {/* STORY 6 — the destination is the PAGE, not a dropdown. `mt-auto` pins the action
                  to the bottom so it lines up across a row of unequal-height cards. */}
              <div className="mt-auto pt-1">
                <StorageDeviceBackup device={device} storage={storage} />
              </div>
              </CardContent>
            </Card>
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

      {/* Forget sits at the BOTTOM, after everything a user might want to check before deciding —
          the version list directly above it is the answer to "what am I about to detach". */}
      <h2 className="mt-10 text-sm font-semibold text-muted">Forget</h2>
      <div className="mt-3 rounded-card border border-line bg-card p-4">
        <p className="text-sm text-muted">
          Remove this storage from quince&apos;s configuration. The backups on the disk are not
          deleted.
        </p>
        <div className="mt-3">
          <ForgetStorage storage={storage} />
        </div>
      </div>
    </section>
  );
}

function BackLink() {
  return (
    <Link to="/" className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
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
