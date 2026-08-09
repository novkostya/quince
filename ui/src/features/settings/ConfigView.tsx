import type { ConfigResponse } from "@/lib/types";
import { formatDateTime } from "@/lib/format";

// The PVE-style "current configuration" view: THE FILE ITSELF, plus a banner when a hand-edit was
// rejected or an unknown key was seen (ui.design.md principle 8).
//
// This rendered `JSON.stringify(data.config)` until qn.6j (quince#728, Operator ruling 2026-08-09).
// The panel's subtitle says "You can edit the file by hand instead", and what sat beside that
// sentence was not the language of the file — anyone comparing this against their editor was
// translating between two syntaxes.
//
// `file_text` is NOT a rendering of `data.config`, and since qn.6j they are genuinely different
// documents: `config` is the RESOLVED configuration with every key filled, `file_text` is only what
// the user set. Rendering the former here would show a document the file does not contain.
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
      {/* An empty string is a REAL STATE, not a loading one: a fresh install has no config.yml until
          the first save. Saying so beats an empty box, and `source.mtime` says the same thing in the
          line below — this is the sentence a first-run user reads. */}
      <pre className="overflow-auto rounded-card border border-line bg-bg p-4 font-mono text-xs whitespace-pre-wrap break-words text-muted">
        {data.file_text || "No config.yml yet — it is written on your first save."}
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
