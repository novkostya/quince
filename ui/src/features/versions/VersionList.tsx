import type { Version } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatBytes, formatRelativeTime } from "@/lib/format";

function verifyLabel(v: Version): string {
  if (v.content_verified_at) return "decryption verified";
  if (v.structure_verified_at) return "structure verified";
  return "unverified";
}

function VersionRow({ version }: { version: Version }) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-card border border-line bg-card p-4">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium">{formatRelativeTime(version.created_at)}</span>
          {version.is_latest ? <Badge tone="accent">latest</Badge> : null}
          {version.job_id === null ? <Badge tone="neutral">adopted</Badge> : null}
          {!version.encrypted ? <Badge tone="warn">unencrypted</Badge> : null}
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-xs tabular-nums text-subtle">
          <span>{version.backend}</span>
          <span>{version.kind}</span>
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

export function VersionList({ versions }: { versions: Version[] }) {
  if (versions.length === 0) {
    return <div className="text-sm text-muted">No versions yet.</div>;
  }
  return (
    <div className="flex flex-col gap-2">
      {versions.map((v) => (
        <VersionRow key={v.id} version={v} />
      ))}
    </div>
  );
}
