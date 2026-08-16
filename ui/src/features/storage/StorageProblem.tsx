import { Button } from "@/components/ui/button";
import type { Storages } from "@/features/jobs/useStorages";
import type { Storage } from "@/lib/types";

// A STORAGE'S PROBLEM AND ITS REMEDY, ON THE STORAGE'S OWN PAGE (quince#627).
//
// Both used to render on a DEVICE's details page, under the action row: the full diagnosis of a
// storage — marker missing, path unreadable, mount state — and a `Re-check` button that re-probes
// it. A user who came to back up one phone was shown a disk's mount state and a button that
// re-probes it. Operator, 2026-08-04: *"that makes no sense. Should be in storage details
// instead."*
//
// AND IT WAS WORSE THAN MISPLACED. That block rendered one line and one button per unreachable
// storage IN THE WHOLE CONFIGURATION, with no reference to the one being backed up to. The
// screenshot the issue came from showed `shuttle` selected and the sentence diagnosing `ghost` — a
// storage the user was not using, could not reach, and had not asked about. With N unreachable
// storages it was N lines and N buttons on a page about a phone.
//
// So the fix was a DELETION rather than a rescoping: there was no correct chosen-storage-only
// version to keep there, because a storage's health belongs where the storage is.
//
// The device page still says something when its selected storage is unavailable — `StorageNotices`
// carries a short unavailability line naming it and linking here. The FACT, not the diagnosis.
export function StorageProblem({
  storage,
  storages,
}: {
  storage: Storage;
  storages: Storages;
}) {
  const { recheck, rechecking } = storages;
  if (storage.reachable || !storage.unreachable_reason) return null;

  const pending = rechecking[storage.name] === "pending";
  return (
    <div
      data-testid="storage-detail-reason"
      className="mt-3 flex flex-wrap items-center gap-2 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn"
    >
      {/* THE DAEMON'S OWN SENTENCE, never a code mapped to client copy — it names WHICH path and
          WHICH marker, which no client-side copy for a code can.

          THE OLD REASON FOR THIS IS SPENT (quince#569, fixed). It read "`unreachable_code`'s declared
          values are wrong today", which was true while the daemon stringified its internal enum; it
          now translates at the boundary. Rendering the prose is still right, but on the argument
          above rather than because the code could not be trusted. */}
      <span className="min-w-0 break-words">{storage.unreachable_reason}</span>
      {/* RE-CHECK SITS ON THE ROW THAT STATES THE PROBLEM (quince#459): the Operator's ruling is
          "plug the disk in and press the button", and this is where the sentence describing the
          problem already is. A reachable storage gets no button — the press would be a no-op the
          user cannot interpret, which is why this component renders nothing at all in that case. */}
      <Button
        variant="outline"
        size="sm"
        className="h-6 px-2 text-xs sm:h-6"
        disabled={pending}
        onClick={() => recheck(storage.name)}
        data-testid="storage-recheck"
        aria-label={`Re-check ${storage.name}`}
      >
        {pending ? "Checking…" : "Re-check"}
      </Button>
      {rechecking[storage.name] === "failed" ? (
        <span data-testid="storage-recheck-failed">couldn&rsquo;t re-check</span>
      ) : null}
    </div>
  );
}
