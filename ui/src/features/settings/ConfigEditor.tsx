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
// surfaces per-field inline. Restart-required this rung (D12 staging).
export function ConfigEditor({ config }: { config: Config }) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<Config>(config);
  const [errors, setErrors] = useState<ConfigFieldError[]>([]);
  const [saved, setSaved] = useState(false);

  const mutation = useMutation({
    mutationFn: (c: Config) => updateConfig(c),
    onSuccess: (resp) => {
      setErrors([]);
      setSaved(true);
      setTheme(resp.config.ui.theme as Theme);
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
      <Field label="Storages" error={errFor("storage")}>
        {(id) => (
          <p id={id} className="text-xs text-muted">
            {draft.storage === null
              ? "none declared — quince refuses to start without one (config.yml `storage:`)"
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

      <div className="flex items-center gap-3">
        <Button type="submit" disabled={mutation.isPending}>
          Save
        </Button>
        {saved ? <span className="text-xs text-ok">Saved · restart quince to apply</span> : null}
        {errFor("") ? <span className="text-xs text-danger">{errFor("")}</span> : null}
      </div>
    </form>
  );
}
