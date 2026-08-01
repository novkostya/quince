import type { Storage } from "@/lib/types";

// StorageSelect is the "where does this backup go" control on Back up now (qn.6c story 9).
//
// It renders ONLY when there is a real choice — more than one declared storage. With one storage
// the question does not exist, and a select with a single option is a control that teaches the user
// there is a decision when there is not.
//
// An UNREACHABLE storage is listed and DISABLED, with its reason shown. Hiding it would be the
// wrong kind of tidy: the user plugged that disk in once, and a list it silently vanishes from is a
// list they cannot trust. Disabled-with-a-reason is the honest shape — and it is why the ruling
// made quince serve rather than refuse when a disk is out.
export function StorageSelect({
  storages,
  value,
  onChange,
  disabled,
}: {
  storages: Storage[];
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
}) {
  if (storages.length < 2) return null;

  const chosen = storages.find((s) => s.id === value) ?? storages.find((s) => s.default);

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

      {/* The reason for an unreachable CHOSEN storage. Shown rather than thrown: the daemon's own
          sentence names which path and which marker, which no client-side copy could. */}
      {chosen && !chosen.reachable && chosen.unreachable_reason ? (
        <span className="text-xs text-warn" data-testid="storage-unreachable">
          {chosen.unreachable_reason}
        </span>
      ) : null}

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
