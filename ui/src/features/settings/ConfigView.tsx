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
          {/* A warning carries a config path and a server message, both arbitrary-length — the
              same class of content as the dump below, so the same treatment (quince#631). */}
          <ul className="mt-1 list-disc pl-5 font-mono text-xs break-words">
            {data.warnings.map((w, i) => (
              <li key={i}>
                {w.path ? `${w.path}: ` : ""}
                {w.message}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {/* WRAP, don't scroll sideways — and the `overflow-auto` here is the net, not the mechanism.
          A config value long enough to exceed a phone's width used to widen this pane, the grid
          column holding it, and the whole content area with it, sliding the editor's own fields off
          the left edge (quince#631). `overflow-auto` was already on this element and was DEAD: a
          grid item defaults to `min-width: auto`, so the column could not shrink below this
          element's intrinsic width and there was never any overflow for it to catch. The `min-w-0`
          on the column in `SettingsPage` is what brings it to life — neither half works alone.

          Wrapping rather than an inner scrollbar is the Operator's stated preference, and on a
          phone a horizontally-scrollable pane inside a vertically-scrolling page is an awkward
          gesture. `break-words` also breaks a single token too long to fit its line, so a long path
          or blob wraps instead of escaping; `overflow-auto` stays for anything that still cannot.

          Same idiom and same reason as `JobLogPane`, which got this right during the qn.6a mobile
          pass and says so in its own comment. */}
      <pre className="overflow-auto rounded-card border border-line bg-bg p-4 font-mono text-xs whitespace-pre-wrap break-words text-muted">
        {JSON.stringify(data.config, null, 2)}
      </pre>
      {/* The path is arbitrary-length too, and it is the other thing on this page that can be
          longer than a phone is wide. */}
      <div className="mt-2 font-mono text-xs break-words text-subtle">
        {data.source.path}
        {data.source.mtime ? ` · edited ${formatDateTime(data.source.mtime)}` : " · not written yet"}
      </div>
    </div>
  );
}
