import { useState } from "react";
import { ChevronRight } from "lucide-react";
import { Link } from "react-router-dom";
import type { Version } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { formatBytes } from "@/lib/format";
import { RelativeTime } from "@/components/RelativeTime";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";

// NO VERIFICATION LABEL ON A VERSION ROW. `verifyLabel` rendered one of three strings here until
// quince#1047, and each was unusable for its own reason:
//
//   "structure verified"  — TAUTOLOGICAL. Structural verify is the gate at commit (verify →
//                           RENAME_EXCHANGE → snapshot), so a tree that fails it never becomes a
//                           version. Every job-created row said this because it could not be in
//                           the list otherwise, which is a row congratulating quince for meeting
//                           its own entry requirement.
//   "unverified"          — UNREACHABLE. It needs a marker with no parseable structure_verified_at,
//                           and every marker quince writes sets one. Blanking it by hand fails the
//                           marker's own sha256, and scanDir then refuses to adopt the version AT
//                           ALL — the row vanishes rather than appearing unverified.
//   "decryption verified" — the only one that would mean something, and NOTHING IN THE ENGINE SETS
//                           content_verified_at (quince#1047). Only demo/fixtures.go did, so the
//                           demo showed a state the product cannot reach.
//
// It comes back with qn.8's unlock, where a resolved canary makes "decryption verified" a claim
// quince has actually earned. Until then the row makes no verification claim at all, which is the
// honest state — not a weaker claim standing in for the one that is unbuilt.

// RemoveButton deletes a version whose artifact is gone (DELETE /api/versions/{id}). On success the
// server emits version.deleted and the store drops the row; if the WS is slow, remove it locally too
// so the dead row disappears immediately. Errors surface inline rather than silently.
function RemoveButton({ id }: { id: string }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const removeLocal = useVersionsStore((s) => s.remove);

  async function remove() {
    setBusy(true);
    setError(null);
    try {
      await api.del(`/api/versions/${id}`);
      removeLocal(id);
    } catch (e) {
      setError(e instanceof Error ? e.message : "could not remove");
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col items-end gap-1">
      <Button size="sm" variant="outline" onClick={() => void remove()} disabled={busy}>
        {busy ? "Removing…" : "Remove"}
      </Button>
      {error ? (
        <span className="text-xs text-danger" role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}

// deviceLabel resolves a friendly device name for the shared dashboard list (which mixes devices);
// falls back to a short UDID tail when the device isn't in the store.
function DeviceLabel({ udid }: { udid: string }) {
  const name = useDevicesStore((s) => s.byUdid[udid]?.name);
  return <span className="text-subtle">{name || `…${udid.slice(-6)}`}</span>;
}

function VersionRow({ version, showDevice }: { version: Version; showDevice?: boolean }) {
  // A missing version's artifact is GONE — the row survives (history isn't silently shrunk, (cr)) but
  // it makes NO size claim, offers no Unlock/browse, and gets an "artifact gone — remove?" action.
  // `data-testid="version-missing"` so a gate can COUNT these rather than match on prose
  // (quince#661). The storage page's header-minus-restorable invariant needs the number of missing
  // versions, and deriving it from the copy would break the moment the copy is reworded.
  if (version.missing) {
    return (
      <div
        data-testid="version-missing"
        className="flex items-center justify-between gap-3 rounded-card border border-dashed border-line bg-card p-4 opacity-80"
      >
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <RelativeTime iso={version.created_at} className="text-sm font-medium text-muted" />
            {showDevice ? <DeviceLabel udid={version.udid} /> : null}
            <Badge tone="danger">missing</Badge>
          </div>
          <div className="mt-1 text-xs text-muted">artifact gone — its backup files are no longer on disk</div>
        </div>
        <RemoveButton id={version.id} />
      </div>
    );
  }

  return (
    <div className="flex items-center justify-between gap-3 rounded-card border border-line bg-card p-4">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <RelativeTime iso={version.created_at} className="text-sm font-medium" />
          {showDevice ? <DeviceLabel udid={version.udid} /> : null}
          {version.is_latest ? <Badge tone="accent">latest</Badge> : null}
          {version.job_id === null ? <Badge tone="neutral">adopted</Badge> : null}
          {!version.encrypted ? <Badge tone="warn">unencrypted</Badge> : null}
        </div>
        {/* kind ("full"/"incremental") is deliberately NOT shown (ck): every quince version is a
            complete, independently-restorable backup — "incremental" would import a false
            fragile-chain mental model.

            ONE size, unqualified. This read "N logical · N on disk" until quince#442: both figures
            came from the same walk, so the row printed one number twice and captioned them as two
            different measurements. The word "logical" went with the second figure — it existed only
            to tell them apart, and a lone qualified size asks the reader what the other one was. */}
        <div className="mt-1 font-mono text-xs tabular-nums text-subtle">
          {formatBytes(version.logical_bytes)}
        </div>
      </div>
      {/* THE ROW OPENS THE BACKUP, and until qn.8 slice 7 this chevron was explicitly inert — the
          comment here read "Non-interactive for now" because there was nowhere for it to go.

          A LINK, NOT A BUTTON, and not the word "Unlock". `VersionList.test.tsx` asserts that word
          appears nowhere on a row and has since quince#442: it made no sense on an unencrypted
          version, which has nothing to unlock and is still browsable (spec D7).

          IT GOES TO THE OVERVIEW, NOT THE BROWSER, since qn.9 slice 10 — the version's own page,
          which says what the backup IS. The file tree is one click on from there (D9), which is
          what "overview becomes primary" means in the one place a user actually chooses.

          NO PASSWORD IS ASKED FOR ANYWHERE ON THAT PATH NOW. This comment used to end "the password
          is asked for on the destination, not here", which was true while the destination was the
          browser; the overview reads three unencrypted plists and needs none. The point it was
          making survives and is now stronger: a row click is a navigation the browser can undo, and
          it no longer costs a credential prompt to look. */}
      <Link
        to={`/versions/${version.id}`}
        aria-label="What is in this backup"
        className="shrink-0 rounded-lg p-1 text-subtle transition-colors hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
      >
        <ChevronRight size={18} aria-hidden />
      </Link>
    </div>
  );
}

export function VersionList({ versions, showDevice }: { versions: Version[]; showDevice?: boolean }) {
  if (versions.length === 0) {
    return <div className="text-sm text-muted">No versions yet.</div>;
  }
  return (
    <div className="flex flex-col gap-2">
      {versions.map((v) => (
        <VersionRow key={v.id} version={v} showDevice={showDevice} />
      ))}
    </div>
  );
}
