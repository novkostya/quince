import * as React from "react";
import { Button } from "@/components/ui/button";
import type { Storages } from "./useStorages";

// StorageSelect is the "where does this backup go" control on Back up now (qn.6c story 9).
//
// It renders ONLY when there is a real choice — more than one declared storage. With one storage
// the question does not exist, and a select with a single option is a control that teaches the user
// there is a decision when there is not.
//
// A FAILED LOAD IS NOT THE SAME AS NO CHOICE, and rendering them identically is the defect the
// review caught (quince#452): the user with two disks would find the control simply gone, press the
// button, and have the backup go to the default with nothing saying so. Failure gets its own line —
// shown, not thrown, the same shape as `Storage.unreachable_reason`.
//
// An UNREACHABLE storage is listed and DISABLED, with its reason shown. Hiding it would be the
// wrong kind of tidy: the user plugged that disk in once, and a list it silently vanishes from is a
// list they cannot trust. Disabled-with-a-reason is the honest shape — and it is why the ruling
// made quince serve rather than refuse when a disk is out.
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
  const { state, recheck, rechecking } = sub;
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

  if (state.status === "failed") {
    return (
      <span className="text-xs text-warn" data-testid="storages-failed">
        couldn&rsquo;t load storages — this backup will go to the default
      </span>
    );
  }
  if (state.status === "loading") return null;
  if (storages.length < 2) return null;

  return (
    <div className="flex flex-col gap-1">
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

      {/* THE REASON FOR EVERY UNREACHABLE STORAGE, not just a chosen one — because a disabled
          option CANNOT BE CHOSEN. Showing it on selection was unreachable code: the user saw
          "not connected" and could never learn which path or why. Found by driving G8 against
          the real API rather than against props (qn.6c story 9).

          The daemon's own sentence, because it names the path and the marker.

          RE-CHECK SITS HERE, ON THE UNREACHABLE ROW ONLY (quince#459). The Operator's ruling is
          "plug the disk in and press the button", and this is where the sentence describing the
          problem already is — the button lands next to its own reason rather than in a corner of
          the page. A reachable storage gets none: the press would be a no-op the user cannot
          interpret, and a control offered where there is nothing to fix teaches that pressing it
          is how you make things happen. That is a UI-taste call, rung-local, not a contract one —
          quince#459 flagged it as exactly that. */}
      {storages
        .filter((s) => !s.reachable && s.unreachable_reason)
        .map((s) => (
          <span
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
              disabled={disabled || rechecking[s.id] === "pending"}
              onClick={() => recheck(s.id)}
              data-testid="storage-recheck"
              aria-label={`Re-check ${s.name}`}
            >
              {rechecking[s.id] === "pending" ? "Checking…" : "Re-check"}
            </Button>
            {/* A FAILED PRESS IS SHOWN, NEVER SWALLOWED. Without this the button would look
                identical whether the re-check ran and the disk is still out, or the request never
                landed — and the user would keep pressing a control that is not reaching the
                daemon. */}
            {rechecking[s.id] === "failed" ? (
              <span data-testid="storage-recheck-failed">couldn&rsquo;t re-check</span>
            ) : null}
          </span>
        ))}

      {/* THE COST, STATED BEFORE IT IS PAID (story 8). Attached to the option that carries it, not
          to the page: it is a fact about this device and this storage. */}
      {chosen?.reachable && chosen.will_be_full ? (
        <span className="text-xs text-warn" data-testid="storage-will-be-full">
          First backup to {chosen.name} — this transfers everything, not just what changed.
        </span>
      ) : null}
    </div>
  );
}
