import { useId, useState } from "react";
import type { ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Config, ConfigFieldError } from "@/lib/types";
import { configKey, updateConfig } from "@/lib/config";
import { APIError } from "@/lib/api";
import { setTheme, type Theme } from "@/lib/theme";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";

// Field pairs a visible <Label> with its control, and ASSOCIATES THEM (quince#629).
//
// The label had no `htmlFor` and the controls had no `aria-label`, so nothing connected the two: a
// screen reader reaching this form announced a combobox with its options and no indication of what
// it sets. EVERY field here had it, not only the two selects the issue named.
//
// `children` IS A FUNCTION, and that is what makes the association possible without touching any
// primitive. `Field` cannot put an id on an opaque `ReactNode`, so it mints one with `useId()` and
// hands it down; `Input` and `Select` already spread their props onto the native element, so `id`
// forwards unchanged and neither needed a change.
//
// NOT by nesting the control inside the <label>. Radix associates implicitly that way, but it would
// restructure every field in the form to fix an attribute — a redesign where an id was asked for
// (architect ruling 2026-08-04).
//
// THE INLINE SELECTS ELSEWHERE KEEP THEIR `aria-label`, AND THAT IS NOT AN INCONSISTENCY.
// `StorageSelect` and `BackupControls` have no visible <Label> to associate with, so `aria-label` is
// the right mechanism THERE. Each control uses the one that fits it; do not "unify" them.
function Field({
  label,
  error,
  children,
}: {
  label: string;
  error?: string;
  children: (id: string) => ReactNode;
}) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1">
      <Label htmlFor={id}>{label}</Label>
      {children(id)}
      {error ? <span className="text-xs text-danger">{error}</span> : null}
    </div>
  );
}

// A safe-keys editor over config.yml. PUT replaces the full document (contracts §1); a 422
// surfaces per-field inline.
//
// NO RESTART IS PROMISED, AND THE REASON IS PER-FIELD RATHER THAN BLANKET (`qn.6g`, quince#577).
// This header said *"Restart-required this rung (D12 staging)"* and the Save row said
// *"Saved · restart quince to apply"* unconditionally. Neither is true of anything this form
// renders:
//
//	backup.preferred_transport   LIVE   — the backup applier (qn.6g PR 5)
//	backup.require_encryption    LIVE   — read per job, off a synchronized field (PR 5)
//	sessions.ttl_minutes         UNREAD — quince#656; a restart would not make it take effect either
//	ui.theme                     LIVE   — client-side, from the PUT response
//
// Storages are shown read-only here, and are live besides (PR 4).
//
// SO THE NOTICE IS DELETED RATHER THAN MADE CONDITIONAL — the smaller change, and the one this
// form's contents justify: there is no field left to condition it on. **If a restart-required field
// is ever added here — `sessions.allow_insecure_transport` and `devices.*` are the two bins
// contracts §6 names — the notice comes back attached to THAT field, never to the Save row.** An
// unconditional notice over a form of live fields is exactly what this change removes.
export function ConfigEditor({ config }: { config: Config }) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<Config>(config);
  const [errors, setErrors] = useState<ConfigFieldError[]>([]);
  const [saved, setSaved] = useState(false);

  // THE DRAFT FOLLOWS THE SERVER (quince#764). `useState(config)` captures its argument on FIRST
  // MOUNT and ignores every later value, so without this the editor can hold a document the server
  // stopped serving minutes ago — and `PUT /api/config` is a FULL-DOCUMENT REPLACE, so saving one
  // field ships the whole stale draft and reverts everything that changed underneath it.
  //
  // Observed on the staging stand: two storages hand-edited to `backend: hardlink`, quince restarted,
  // one unrelated save, and both were back to `auto` on disk — while the preview panel showed the
  // correct file. Two documents on one screen, disagreeing.
  //
  // ADJUSTED DURING RENDER, NOT IN AN EFFECT. This is React's own pattern for state derived from
  // props; an effect renders once with the stale value first, and on this form that frame is a save
  // the user can click.
  //
  // ONLY WHEN THE FORM IS CLEAN, and that half is load-bearing. React Query refetches on window
  // focus, so an unconditional re-sync would wipe whatever was being typed the moment you switched
  // tabs — trading this bug for a second silent loss in the opposite direction. A dirty form keeps
  // its draft; TELLING the user it no longer matches the server is quince#764's PR 2, and until that
  // lands a dirty form can still save a stale section.
  //
  // `config !== synced` is a REFERENCE test, deliberately: React Query's structural sharing preserves
  // identity across a refetch whose content is unchanged, so this fires when the document actually
  // moved rather than on every poll.
  // THE STRINGIFY COMPARISON IS KEY-ORDER SENSITIVE, and a false `dirty` silently restores
  // quince#764 (architect note on quince#765). It is safe because BOTH SIDES COME FROM THE SAME
  // SERVER DOCUMENT and are only ever updated by SPREAD, which preserves key insertion order — so a
  // clean draft stringifies identically to `synced`. A future handler that rebuilds a nested section
  // from an object literal in a different key order breaks that: the form reads dirty when nobody
  // touched it, the re-sync stops, and the bug is back for that section **with no test failing**,
  // because the tests reach this through spreads. Written down because the assumption is invisible
  // and its failure is silent.
  const [synced, setSynced] = useState<Config>(config);
  const [staleElsewhere, setStaleElsewhere] = useState(false);
  if (config !== synced) {
    const dirty = JSON.stringify(draft) !== JSON.stringify(synced);
    setSynced(config);
    if (dirty) {
      // THE FORM IS NOT OVERWRITTEN AND THE USER IS TOLD. Keeping the draft protects the edit in
      // progress; saying so is what stops the save silently shipping a stale section — the half PR 1
      // deliberately left open.
      setStaleElsewhere(true);
    } else {
      setDraft(config);
      setStaleElsewhere(false);
    }
  }

  // Taking the server's document DISCARDS the edit in progress, so it is an explicit action rather
  // than something that happens to you — which is the whole distinction this pair of changes draws.
  const takeServerVersion = () => {
    setDraft(config);
    setSynced(config);
    setStaleElsewhere(false);
    setErrors([]);
  };

  const mutation = useMutation({
    mutationFn: (c: Config) => updateConfig(c),
    onSuccess: (resp) => {
      setErrors([]);
      setSaved(true);
      setTheme(resp.config.ui.theme as Theme);
      // THE SAVE ENDS THE DIVERGENCE, whichever way it went. The response IS the new server document
      // — `PUT` returns the same body `GET` does — so adopting it here leaves the form, `synced` and
      // the server agreeing, and clears a notice whose cause the save has just resolved. Without
      // this, a form that was stale-then-saved would keep warning about a document it has replaced.
      setDraft(resp.config);
      setSynced(resp.config);
      setStaleElsewhere(false);
      void qc.invalidateQueries({ queryKey: configKey });
    },
    onError: (err: unknown) => {
      setSaved(false);
      if (err instanceof APIError && err.status === 422) {
        const details = err.details as { errors?: ConfigFieldError[] } | undefined;
        setErrors(details?.errors ?? []);
      } else {
        setErrors([{ path: "", message: err instanceof Error ? err.message : "save failed" }]);
      }
    },
  });

  const errFor = (path: string) => errors.find((e) => e.path === path)?.message;

  return (
    <form
      className="flex max-w-md flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        setSaved(false);
        mutation.mutate(draft);
      }}
    >
      {/* PREFERRED, not "backup transport" — the label is the fix as much as the key is (quince#654).
          "Backup transport: usb" reads as "use USB", and the only true meaning is "prefer USB when
          both are available". That misreading is the report that opened the issue, so the label says
          what it does and the hint below says what it does NOT do. No `auto`: as a preference it
          would mean "prefer whatever is already preferred". */}
      <Field label="Preferred transport" error={errFor("backup.preferred_transport")}>
        {(id) => (
          <>
            <Select
              id={id}
              value={draft.backup.preferred_transport}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  backup: { ...draft.backup, preferred_transport: e.target.value },
                })
              }
            >
              {["usb", "wifi"].map((o) => (
                <option key={o} value={o}>
                  {o}
                </option>
              ))}
            </Select>
            <p className="mt-1 text-xs text-muted">
              Used when a device is reachable over both. A device on only one is always backed up
              over that one.
            </p>
          </>
        )}
      </Field>

      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={draft.backup.require_encryption}
          onChange={(e) =>
            setDraft({ ...draft, backup: { ...draft.backup, require_encryption: e.target.checked } })
          }
        />
        Require encryption
      </label>

      {/* THE GLOBAL "Storage backend" SELECT IS GONE, and it is not moved — it no longer exists.
          quince#473 flattened `storage:` to a list of fully-specified entries, so `backend` is a
          per-entry key and there is no global to edit. This control read `draft.storage.backend`
          and crashed the whole Settings route on a null `storage` (the demo's state), which is how
          it was found — reported from the demo, not caught by a gate.

          Editing a storage is quince#443's surface (`qn.6d` — storage becomes visible), and a
          read-only list here would be a second place to keep in step with it. What Settings shows
          instead is nothing, which is honest: this page never edited storages, only the global. */}
      {/* The id goes on the <p> here rather than a control, because there is no control — this
          field is read-only prose. `htmlFor` pointing at a non-labelable element is inert rather
          than wrong, and the alternative (a Field variant with no association) would be a second
          shape to keep in step for one call site. */}
      {/* THE ZERO-STORAGE COPY WAS FALSE FROM THE MOMENT qn.6e LANDED. It read "quince refuses
          to start without one", which was true until the Operator ruled that any zero-storage
          start IS the onboarding state (2026-08-07, quince#502): quince now SERVES and refuses
          every API outside setup instead of exiting.

          Reachable, and by the one reader most likely to be misled: this branch renders only
          when `storage` is null, which is exactly the first-run install now able to reach
          Settings at all. Before the ruling it was nearly dead code. */}
      <Field label="Storages" error={errFor("storage")}>
        {(id) => (
          <p id={id} className="text-xs text-muted">
            {draft.storage === null
              ? "none declared — quince is serving SETUP ONLY until you add one (config.yml `storage:`)"
              : `${draft.storage.length} declared in config.yml — ${draft.storage
                  .map((s) => `${s.name || s.path} (${s.backend})`)
                  .join(", ")}`}
          </p>
        )}
      </Field>

      <Field label="Session TTL (minutes)" error={errFor("sessions.ttl_minutes")}>
        {(id) => (
        <Input
          id={id}
          type="number"
          min={1}
          value={draft.sessions.ttl_minutes}
          // SPREAD THE SECTION, not only the document. `{ ...draft, sessions: {…} }` keeps every
          // other section and REPLACES this one, so any key of `sessions:` this form does not
          // render is dropped on save — and PUT is a full-document replace, so dropped means reset
          // to the Go zero value. Editing the TTL would have switched `allow_insecure_transport`
          // back off with nothing said. `tsc` caught it the moment that field became required,
          // which is the quince#493 hazard catching itself exactly once.
          onChange={(e) =>
            setDraft({
              ...draft,
              sessions: { ...draft.sessions, ttl_minutes: Number(e.target.value) },
            })
          }
        />
        )}
      </Field>

      <Field label="Theme" error={errFor("ui.theme")}>
        {(id) => (
          <Select
            id={id}
            value={draft.ui.theme}
            // Same shape as above. `ui:` has one key today so nothing is lost yet — which is
            // precisely why it is worth fixing now rather than when it is a bug.
            onChange={(e) => setDraft({ ...draft, ui: { ...draft.ui, theme: e.target.value } })}
          >
            {["system", "light", "dark"].map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </Select>
        )}
      </Field>

      {/* THE CONFIGURATION MOVED WHILE YOU WERE EDITING (quince#764). Above the Save row on purpose:
          it is a fact about what Save will do, and a reader who meets it after pressing the button
          has already shipped the stale section.

          It says what will happen rather than only what happened, because `PUT /api/config` is a
          full-document replace — saving now overwrites the change somebody else made. The action is
          the other direction and is labelled with its cost: taking the new version discards this
          edit. Neither side is dropped without being chosen, which is the rule this pair of changes
          exists to establish. */}
      {staleElsewhere ? (
        <div
          role="status"
          className="rounded-card border border-line bg-accent-soft p-3 text-sm text-warn"
        >
          <div className="font-medium">The configuration changed elsewhere</div>
          <p className="mt-1 text-xs">
            Something else — a hand-edit, the CLI, another tab — changed <code>config.yml</code> since
            this form loaded. Saving now replaces it with what you see here. Your unsaved edits are
            kept until you choose.
          </p>
          <Button type="button" className="mt-2" onClick={takeServerVersion}>
            Discard my edits and load the new version
          </Button>
        </div>
      ) : null}

      <div className="flex items-center gap-3">
        <Button type="submit" disabled={mutation.isPending}>
          Save
        </Button>
        {/* "Saved", and NOT "Saved · applied". The stronger word was the first draft and it is a
            lie about one of the four fields above: `sessions.ttl_minutes` is read by nothing
            (quince#656), so a save neither applies nor fails to apply — it lands in a file with no
            consumer. Trading a false restart promise for a false apply promise is not a fix.
            Surfacing the unread field is quince#656's, and this spec puts it out of scope. */}
        {saved ? <span className="text-xs text-ok">Saved</span> : null}
        {errFor("") ? <span className="text-xs text-danger">{errFor("")}</span> : null}
      </div>
    </form>
  );
}
