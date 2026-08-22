import { Link } from "react-router-dom";
import { HardDrive, AlertTriangle } from "lucide-react";
import type { Storage } from "@/lib/types";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import { formatBytes } from "@/lib/format";

// StorageCard is one declared storage on Home (qn.6d stories 2-4).
//
// It mirrors DeviceCard's anatomy deliberately — same Card/CardContent shell, same `h-full` +
// `min-w-0` + `mt-auto` so the two kinds align in one grid, same title/status/meta rhythm. Storage
// is a PEER of Device, and a card that looked like a different species would say otherwise.
//
// THAT MIRROR IS A CLAIM A READER TRUSTS INSTEAD OF CHECKING, and it was false for a while: quince#1033
// put `min-w-0` on DeviceCard and StorageDetailsPage and not here, so this sentence described an
// anatomy two of three cards had (quince#1042). Restated with the class named, because "same shell"
// is not something the next reader can check against a diff.
//
// THE ACTION SLOT IS EMPTY IN THIS RUNG. Devices put `Back up now` there; storage has no card-level
// action until a later rung. The slot is still reserved so the two card kinds keep the same shape.

// space narrows the two nullable capacity fields ONCE and returns them with the percentage.
//
// `pct` is the fraction USED, so the bar FILLS as the disk fills. This is the convention every
// reference the Operator checked uses — Proxmox Backup Server's "Usage %" column and Windows
// Explorer's drive bar both fill by usage — and it is what a person already knows how to read.
//
// I SHIPPED THE INVERSE AND IT WAS WRONG ON REAL DATA. The card first rendered the FREE fraction,
// on the argument that it is derivable from the "X free of Y" line directly above it. On staging an
// EMPTY storage then rendered a COMPLETELY FULL bar at 100% — the most alarming thing a capacity
// gauge can show, for the safest possible state. The argument was about arithmetic; the failure was
// about what a filled bar MEANS, which no amount of internal consistency fixes.
//
// The spec's own mockup showed the free fraction and has been corrected with it, because it was my
// invention rather than a ruling.
//
// RETURNING THE FIGURES, not just the percentage, is what removes the `?? 0` the render path used to
// need. That fallback was unreachable in fact — guarded by a non-null percentage, which implied both
// figures were non-null — and one loosened condition away from silently rendering "0 B free", which
// is exactly the full-disk misreading the ruling forbids (capacity is null, never 0). Narrowing once
// here makes the guarantee structural rather than a property two functions must keep agreeing about.
//
// Null when either figure is missing: an unreachable storage has no capacity at all, and a bar at 0%
// would read as "no space left" rather than "no measurement". The caller hides the bar instead.
function space(s: Storage): { pct: number; free: number; total: number } | null {
  const { filesystem_free_bytes: free, filesystem_total_bytes: total } = s;
  // `== null` RATHER THAN `=== null`: the fields are ABSENT for a device-scoped principal since
  // qn.13 slice 8f, so undefined and null are the same "no measurement" here (spec D3).
  if (free == null || total == null || total <= 0 || free < 0) return null;
  const used = Math.max(0, total - free);
  return { pct: Math.min(100, (used / total) * 100), free, total };
}

export function StorageCard({ storage, showDefault }: { storage: Storage; showDefault: boolean }) {
  const sp = space(storage);
  const unreachable = !storage.reachable;

  return (
    // min-w-0 closes the same chain quince#631 closed on Settings and quince#1033 closed on the two
    // device cards, one level up from the `min-w-0` on the name column below: a grid item defaults
    // to `min-width: auto`, so this card would not shrink below the intrinsic width of its widest
    // line. Below `sm:` the grid has no explicit template, so the implicit column sizes to content
    // and ONE over-wide card widens the column and every card in it. The `truncate` on the name and
    // the path is dead code until the card itself can shrink.
    //
    // REACHABLE HERE THROUGH TWO ARBITRARY-LENGTH USER-CONTROLLED STRINGS, which is what made this
    // worth fixing rather than mirroring for symmetry: `storage.name` and `storage.path` both come
    // off the wire with nothing bounding them, and a filesystem path passes the width that broke
    // quince#1033 without trying.
    <Card
      data-testid="storage-card"
      data-storage-name={storage.name}
      className={`flex h-full min-w-0 flex-col${unreachable ? " border-dashed opacity-80" : ""}`}
    >
      <CardContent className="flex flex-1 flex-col p-4 sm:p-5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex min-w-0 items-center gap-1.5">
              <HardDrive size={14} className="shrink-0 text-muted" />
              {/* Linked on NAME, matching the route and the ruled API identity — an unreachable
                  storage has no id, and it is the one a user most wants to open. */}
              <Link
                to={`/storage/${encodeURIComponent(storage.name)}`}
                className="truncate text-sm font-semibold tracking-tight hover:text-accent"
              >
                {storage.name}
              </Link>
            </div>
            {/* The name DEFAULTS to the path (quince#504), so repeating it below would say the same
                thing twice on the single-storage install that default exists for. */}
            {storage.name !== storage.path ? (
              <div className="truncate text-xs text-muted">{storage.path}</div>
            ) : null}
          </div>
          {/* `Default` labels nothing when there is only one storage — it is a distinction, and
              there is nothing to distinguish it from. */}
          {showDefault && storage.default ? <Badge tone="neutral">Default</Badge> : null}
        </div>

        {/* A degraded backend is a degraded mode and CLAUDE.md requires those be surfaced. Backend
            is otherwise NOT a glance fact and lives on the details page; `copy` is the exception. */}
        {storage.backend === "copy" ? (
          <div className="mt-3">
            <Badge tone="warn">
              <AlertTriangle size={12} /> copy backend — every backup duplicates the whole tree
            </Badge>
          </div>
        ) : null}

        <div className="mt-3">
          {unreachable ? (
            // NO SIZE CLAIM for an unreachable storage — the same discipline VersionList follows for
            // a missing version. Capacity is null rather than 0 on the wire precisely so the card
            // cannot render "0 bytes free", which would read as a full disk.
            <div data-testid="storage-unreachable-reason" className="text-xs text-warn">
              {storage.unreachable_reason ?? "not connected"}
            </div>
          ) : sp === null ? (
            <div className="text-xs text-muted">free space unavailable</div>
          ) : (
            <>
              {/* ALWAYS plain "1.2 TB free" — never "on this filesystem". Gap A, ruled 2026-08-03:
                  equal byte counts cannot prove two storages share a disk, and the two fields that
                  would have carried filesystem identity were both DECLINED. Two storages on one
                  disk each show this figure with nothing saying it is the same space. That is a
                  RULED ACCEPTANCE — do not "fix" it by reintroducing the distinction. */}
              <div data-testid="storage-space" className="text-sm">
                <span className="font-medium tabular-nums">
                  {formatBytes(sp.free)}
                </span>{" "}
                <span className="text-muted">
                  free of{" "}
                  <span className="tabular-nums">
                    {formatBytes(sp.total)}
                  </span>
                </span>
              </div>
              <div className="mt-2 flex items-center gap-2">
                <Progress percent={sp.pct} className="flex-1" />
                <span className="w-9 shrink-0 text-right text-xs tabular-nums text-subtle">
                  {Math.round(sp.pct)}%
                </span>
              </div>
            </>
          )}
        </div>

        <div
          data-testid="storage-counts"
          className="mt-3 flex flex-wrap gap-x-3 text-xs text-subtle"
        >
          {/* "ever made", not bare "backups" (quince#661). `backup_count` INCLUDES versions whose
              artifact has vanished — qn.6d rung-ruled decision 3: *a version whose artifact has
              vanished is still history the user should see*. The storage page's per-device figure
              counts what is RESTORABLE and is labelled so; the difference between the two is the
              number of missing versions rather than a discrepancy.

              Two numbers measuring different things were rendered as though one should sum to the
              other, and an e2e asserted exactly that — green only because the demo implemented the
              opposite rule from the daemon. */}
          <span>
            {storage.backup_count} {storage.backup_count === 1 ? "backup" : "backups"} ever made
          </span>
          <span>
            {storage.device_count} {storage.device_count === 1 ? "device" : "devices"}
          </span>
          {/* NO TIMESTAMP accompanies the counts, and the field is gone from the wire entirely —
              quince#588, ruled 2026-08-03.

              The spec justified it as *"counts came from the DB and were true at LAST CONTACT"*,
              and that premise is wrong: the counts ARE DB rows, and the DB is reachable whether or
              not the disk is. Unplugging a storage does not make its version count stale — quince
              knows exactly how many rows point at it. The daemon stamps the field at REQUEST time,
              so it reads "just now" on every card, for every storage, always.

              What CAN go stale is whether those versions still exist on the disk, and that is
              `Version.missing` — a different fact, with its own field, set by reconciliation. */}
        </div>

        {/* The action slot devices use for `Back up now`. Empty this rung, and reserved so the two
            card kinds keep one shape. */}
        <div className="mt-auto pt-4" />
      </CardContent>
    </Card>
  );
}
