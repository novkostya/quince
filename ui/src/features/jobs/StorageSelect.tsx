import * as React from "react";
import { Button } from "@/components/ui/button";
import type { Storages } from "./useStorages";
// StorageSelect is the "where does this backup go" CONTROL on Back up now (qn.6c story 9).
//
// IT RENDERS ONLY THE CONTROL. Every sentence it used to emit — the full-transfer warning, the
// unreachable reasons, the failed-load line — now lives in StorageNotices, below the action row.
//
// THAT SPLIT IS quince#325's DEFECT, WHICH THIS COMPONENT REINTRODUCED. A flex item is as wide as
// its widest child, and this control is an item in the page's action row. "First backup to shuttle
// — this transfers everything, not just what changed." is far wider than a `<select>`, so the
// column took the sentence's width and pushed `Manage encryption` out by the overhang — the exact
// gap quince#325 fixed for "Connect the device to back it up." and documented on
// BackupControlsStatus fifteen lines from here. Reported from a screenshot of the staging stand
// during G9, which is the second time the same lesson arrived the same way.
//
// So the rule the comment on BackupControlsStatus states is now STRUCTURAL for this row: the row
// holds controls, prose goes below it. A sentence added back here will re-break the layout.
export function StorageSelect({
  storages: sub,
  value,
  onChange,
  disabled,
}: {
  storages: Storages;
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
}) {
  const { state } = sub;
  const storages = state.status === "loaded" ? state.storages : [];
  const exact = storages.find((s) => s.id === value);
  const chosen = exact ?? storages.find((s) => s.default);

  // TELL THE PARENT WHEN THE FALLBACK FIRES (quince#452 review). If `value` names a storage that is
  // no longer declared — a config edit plus a restart while this page is open — the select would
  // DISPLAY the default while the parent still held the stale id, and the button would submit the
  // stale one. The server refuses that clearly, so it fails safe; but the screen and the request
  // must not disagree in the meantime.
  //
  // Computed BEFORE the early returns because a hook cannot be called after one — the list is empty
  // in every non-loaded state, so the effect is inert there rather than conditional.
  React.useEffect(() => {
    if (!exact && chosen && value !== "") onChange(chosen.id);
  }, [exact, chosen, value, onChange]);

  if (state.status !== "loaded") return null;
  if (storages.length < 2) return null;

  return (
    <label className="text-xs text-muted">
      to{" "}
      <select
        className="rounded-md border border-line bg-card px-1.5 py-1 text-xs text-fg"
        value={chosen?.id ?? ""}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        aria-label="Backup storage"
        data-testid="storage-select"
      >
        {storages.map((s) => (
          <option key={s.id} value={s.id} disabled={!s.reachable}>
            {s.name}
            {s.reachable ? ` (${s.backend})` : " — not connected"}
          </option>
        ))}
      </select>
    </label>
  );
}

// StorageNotices is everything StorageSelect used to say, rendered BELOW the action row where a
// full sentence is free to be a full line (quince#325's rule, applied to this rung's sentences).
//
// It renders for a single storage too, unlike the control: the full-transfer cost and an
// unreachable disk are facts worth stating whether or not there is a choice to make. The CONTROL
// is what a single storage does not need — the question does not exist — and that is a different
// question from whether the user should be told what this backup will cost.
export function StorageNotices({
  storages: sub,
  value,
}: {
  storages: Storages;
  value: string;
}) {
  const { state, recheck, rechecking } = sub;
  const storages = state.status === "loaded" ? state.storages : [];
  const exact = storages.find((s) => s.id === value);
  const chosen = exact ?? storages.find((s) => s.default);

  if (state.status === "failed") {
    return (
      <p className="text-xs text-warn" data-testid="storages-failed">
        couldn&rsquo;t load storages — this backup will go to the default
      </p>
    );
  }
  if (state.status === "loading") return null;

  return (
    <>
      {/* THE REASON FOR EVERY UNREACHABLE STORAGE, not just a chosen one — because a disabled
          option CANNOT BE CHOSEN. Showing it on selection was unreachable code: the user saw
          "not connected" and could never learn which path or why (qn.6c story 9).

          RE-CHECK sits on that row, and only there (quince#459): the Operator's ruling is "plug
          the disk in and press the button", and this is where the sentence describing the problem
          already is. A reachable storage gets none — the press would be a no-op the user cannot
          interpret. */}
      {storages
        .filter((s) => !s.reachable && s.unreachable_reason)
        .map((s) => (
          <p
            key={s.id}
            className="flex flex-wrap items-center gap-2 text-xs text-warn"
            data-testid="storage-unreachable"
          >
            <span>
              {s.name}: {s.unreachable_reason}
            </span>
            <Button
              variant="outline"
              size="sm"
              className="h-6 px-2 text-xs sm:h-6"
              disabled={rechecking[s.name] === "pending"}
              onClick={() => recheck(s.name)}
              data-testid="storage-recheck"
              aria-label={`Re-check ${s.name}`}
            >
              {rechecking[s.name] === "pending" ? "Checking…" : "Re-check"}
            </Button>
            {rechecking[s.name] === "failed" ? (
              <span data-testid="storage-recheck-failed">couldn&rsquo;t re-check</span>
            ) : null}
          </p>
        ))}

      {/* THE COST, STATED BEFORE IT IS PAID (story 8). Proven on hardware during G9 — the staging
          stand's first backup to a second disk showed this line before the transfer started. */}
      {chosen?.reachable && chosen.will_be_full ? (
        <p className="text-xs text-warn" data-testid="storage-will-be-full">
          First backup to {chosen.name} — this transfers everything, not just what changed.
        </p>
      ) : null}
    </>
  );
}
