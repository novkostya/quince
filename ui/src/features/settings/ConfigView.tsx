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
// DiscardedBanner says what is actually true, and WHICH of the two true things it is.
//
// ONE FLAG SERVES TWO SENTENCES, AND THE STORAGE LIST IS WHAT SEPARATES THEM. That needs saying,
// because a reader meeting this will ask why `discarded` alone is not enough and conclude a second
// boolean was forgotten. It was not — it was declined, deliberately (Operator, 2026-08-17,
// quince#1130), on the grounds that a boolean no client branches on differently costs every client
// and buys nothing. This IS a client that branches differently, and it needs no new field:
//
//   - `discarded` + NO storage → the file was refused AT LOAD, so quince is on `Default()`. Nothing
//     declared is in effect and nothing is being backed up. The strong sentence is correct.
//   - `discarded` + storage present → the file was refused at RELOAD, so quince is on the last
//     document that loaded. Storage is running and backups continue; saying "no backups are being
//     made" here would be false.
//
// `data.config` is the RUNNING configuration rather than the file (see `file_text`'s own note), so
// its storage list is a fact about what quince is doing — which is exactly the question. Verified
// against `RequireStorage`, which derives the same state the same way from the same field.
//
// THE SECOND CASE IS THE COMMON ONE ONCE FILE-WATCH SHIPS, and the pair is near-exhaustive rather
// than merely tidy: a reload refusal implies quince was already running, and quince does not run
// without storage — so `discarded` with a non-empty list is the hand-edit case in practice, and the
// degenerate overlap collapses harmlessly because neither case is backing anything up.
function DiscardedBanner({ data }: { data: ConfigResponse }) {
  const storages = data.config.storage;
  const runningOnDefaults = storages === null || storages === undefined || storages.length === 0;
  return (
    // `border-danger` on `bg-card`, matching `InsecureTransportBanner` — the house idiom for a
    // banner that must not be mistaken for the warning box directly below it. Deliberately NOT the
    // `bg-accent-soft`/`text-warn` treatment the warnings list uses: that similarity is the whole
    // defect this banner exists to fix.
    <div role="alert" className="mb-4 rounded-card border border-danger bg-card px-3 py-2 text-sm text-fg">
      <div className="font-medium text-danger">
        {runningOnDefaults
          ? "quince could not read your configuration"
          : "Your configuration file is not in force"}
      </div>
      <p className="mt-1">
        {runningOnDefaults
          ? "quince is running on its defaults, so nothing your file declares is in effect — including any storage. No backups are being made."
          : "quince could not read the file on disk, so it is still running the last configuration that loaded. Your recent edit has not taken effect. Fix the problem below and it will be picked up on its own — no restart needed."}
      </p>
    </div>
  );
}

export function ConfigView({ data }: { data: ConfigResponse }) {
  return (
    <div>
      {/* THE FATALITY GETS ITS OWN HEADLINE (contracts §1's consumer rule: "branch the HEADLINE on
          this, render `warnings` either way"). Until file-watch, no client here branched on
          `discarded` and that was harmless by accident — the only route to it was a startup refusal,
          which `RequireStorage` caught for an unrelated reason and sent to the first-run screen. A
          hand-edit refused at runtime reaches neither, so without this the operator's file is out of
          force and the only thing on screen is a line in a warnings list, at the weight of an
          ignored typo.

          The trade this closes, worth stating because it is not obviously the right way round:
          BEFORE file-watch a bad hand-edit was invisible until a restart and then very loud; AFTER,
          it is caught in two seconds and would have been quiet. */}
      {data.discarded ? <DiscardedBanner data={data} /> : null}

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
