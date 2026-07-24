import { useState } from "react";
import type { Version } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { formatBytes } from "@/lib/format";
import { RelativeTime } from "@/components/RelativeTime";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";

function verifyLabel(v: Version): string {
  if (v.content_verified_at) return "decryption verified";
  if (v.structure_verified_at) return "structure verified";
  return "unverified";
}

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
  if (version.missing) {
    return (
      <div className="flex items-center justify-between gap-3 rounded-card border border-dashed border-line bg-card p-4 opacity-80">
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
            fragile-chain mental model. Sizes are the honest, actionable facts. */}
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-xs tabular-nums text-subtle">
          <span>
            {formatBytes(version.logical_bytes)} logical · {formatBytes(version.physical_bytes)} on disk
          </span>
          <span>{verifyLabel(version)}</span>
        </div>
      </div>
      {/* Unlock/browse arrive with the vault in qn.8. */}
      <Button size="sm" variant="outline" disabled title="Browsing arrives in a later release">
        Unlock
      </Button>
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
