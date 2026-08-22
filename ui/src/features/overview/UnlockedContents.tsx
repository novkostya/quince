import * as React from "react";
import { Card } from "@/components/ui/card";
import { formatBytes } from "@/lib/format";
import type { SessionOverview, VersionOverview } from "@/lib/types";
import { partitionByApp, type AppSize } from "./appSizes";

// UnlockedContents is the second tier of overview: what the backup HOLDS, once it is open.
//
// STORY 2 — the app list does not disappear and come back. The names arrive before the
// unlock, from Info.plist; only the SIZES need the session. So this component renders the
// same list the locked screen showed, with a size column that fills in — rather than a
// spinner replacing a list the user was already reading.
//
// STORY 3 — a size that is not known yet renders as PENDING, never as zero. Those are
// different claims: 0 B says quince looked and there is nothing, and it would be wrong about
// every app on the screen for as long as the walk takes.

export function UnlockedContents({
  overview,
  bundleIDs,
  loading,
}: {
  overview: SessionOverview | null;
  bundleIDs: string[];
  loading: boolean;
}) {
  // The partition is computed from whatever rows have arrived. It is deliberately NOT
  // memoised on `loading`: a partial walk yields partial sizes, and showing them as they
  // arrive is the point — what must not happen is presenting a partial total as the total,
  // which is what `settled` below guards.
  const rows = overview?.page.items ?? [];
  const part = React.useMemo(() => partitionByApp(bundleIDs, rows), [bundleIDs, rows]);

  // SETTLED MEANS THE WALK IS DONE AND THE ARITHMETIC AGREES WITH THE SERVER.
  //
  // Two conditions, and both are needed. `loading` false says every page arrived; the totals
  // comparison says what arrived adds up to what the server counted. G3 is exactly this
  // equality, and asserting it at render rather than only in a test is what stops a silently
  // short walk from being displayed as a complete picture.
  const settled =
    !loading &&
    overview !== null &&
    part.totals.bytes === overview.totals.bytes &&
    part.totals.files === overview.totals.files;

  // A DISAGREEMENT IS DISCLOSED, NOT SWALLOWED. If the walk finished and the numbers do not
  // reconcile, something is wrong with the partition or the paging and the screen must say so
  // rather than show a plausible wrong total — "no silent caps" in its arithmetic form.
  const mismatched =
    !loading &&
    overview !== null &&
    (part.totals.bytes !== overview.totals.bytes || part.totals.files !== overview.totals.files);

  return (
    <Card>
      <div className="flex items-baseline justify-between gap-4">
        <h3 className="text-sm font-medium text-fg">What is in this backup</h3>
        <span className="text-sm text-muted">
          {settled ? (
            <>
              {formatBytes(overview.totals.bytes)} · {overview.totals.files.toLocaleString()} files
            </>
          ) : (
            <Pending />
          )}
        </span>
      </div>

      {mismatched ? (
        <p className="mt-2 text-sm text-danger">
          These figures do not add up to the totals quince counted
          {overview ? ` (${formatBytes(overview.totals.bytes)})` : ""}. Something was missed
          while reading this backup — the per-app sizes below are incomplete, and the file
          browser is the reliable view until this is fixed.
        </p>
      ) : null}

      <ul className="mt-3 flex flex-col gap-1">
        {part.apps.map((a) => (
          <AppRow key={a.bundleID} app={a} settled={settled} />
        ))}
      </ul>

      {/* D3's REMAINDER — the row that makes the arithmetic honest. 21 apps whose sizes sum to
          a fraction of the backup, with nothing accounting for the rest, is a silent cap: the
          rule applies to a number that does not add up exactly as it does to a truncated
          list. It is also the biggest row on a real backup, which is worth seeing. */}
      <div className="mt-3 flex items-baseline justify-between gap-4 border-t border-line pt-2">
        <div>
          <div className="text-sm text-fg">Everything else</div>
          <div className="text-xs text-muted">
            System data, shared app groups, and apps that are no longer installed
            {settled
              ? ` — ${part.remainder.domains.toLocaleString()} ${part.remainder.domains === 1 ? "domain" : "domains"}`
              : ""}
          </div>
        </div>
        <div className="shrink-0 text-sm text-fg">
          {settled ? formatBytes(part.remainder.bytes) : <Pending />}
        </div>
      </div>
    </Card>
  );
}

function AppRow({ app, settled }: { app: AppSize; settled: boolean }) {
  return (
    <li className="flex items-baseline justify-between gap-4">
      <span className="font-mono text-xs text-fg">{app.bundleID}</span>
      <span className="shrink-0 text-sm text-muted">
        {!settled ? (
          <Pending />
        ) : app.domains === 0 ? (
          // NOT "0 B". An app that is installed and has no data in this backup is a real
          // state, and it is a different one from an app whose data is very small. The
          // partition carries `domains` precisely so the surface can tell them apart.
          <span className="text-subtle">no data in this backup</span>
        ) : (
          <>
            {formatBytes(app.bytes)}
            <span className="text-subtle"> · {app.files.toLocaleString()}</span>
          </>
        )}
      </span>
    </li>
  );
}

// Pending is the visible statement that a number is being worked out.
//
// IT IS NOT AN EMPTY CELL AND NOT A ZERO. Story 3 names both hazards: a blank reads as "there
// is nothing here" and a zero reads as a measurement. This reads as neither.
function Pending() {
  return (
    <span className="text-subtle" aria-label="still counting">
      counting…
    </span>
  );
}

// versionAppIDs is the bundle list the locked tier already loaded, so the unlocked view needs
// no second source for it — story 2's "without the app list disappearing and coming back".
export function versionAppIDs(v: VersionOverview | undefined): string[] {
  return v?.apps.bundle_ids ?? [];
}
