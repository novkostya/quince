import type { ConfigResponse } from "@/lib/types";
import { formatDateTime } from "@/lib/format";

// The PVE-style "current configuration" view: the live config as the source of truth, plus
// a banner when a hand-edit was rejected or an unknown key was seen (ui.design.md principle
// 8). Exact YAML-with-comments text is a qn.6 refinement (D12 staging).
export function ConfigView({ data }: { data: ConfigResponse }) {
  return (
    <div>
      {data.warnings.length > 0 ? (
        <div className="mb-4 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn">
          <div className="font-medium">Configuration warnings</div>
          <ul className="mt-1 list-disc pl-5 font-mono text-xs">
            {data.warnings.map((w, i) => (
              <li key={i}>
                {w.path ? `${w.path}: ` : ""}
                {w.message}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <pre className="overflow-auto rounded-card border border-line bg-bg p-4 font-mono text-xs text-muted">
        {JSON.stringify(data.config, null, 2)}
      </pre>
      <div className="mt-2 font-mono text-xs text-subtle">
        {data.source.path}
        {data.source.mtime ? ` · edited ${formatDateTime(data.source.mtime)}` : " · not written yet"}
      </div>
    </div>
  );
}
