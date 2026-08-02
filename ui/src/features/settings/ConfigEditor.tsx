import { useState } from "react";
import type { ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Config, ConfigFieldError } from "@/lib/types";
import { configKey, updateConfig } from "@/lib/config";
import { APIError } from "@/lib/api";
import { setTheme, type Theme } from "@/lib/theme";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";

function Field({ label, error, children }: { label: string; error?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <Label>{label}</Label>
      {children}
      {error ? <span className="text-xs text-danger">{error}</span> : null}
    </div>
  );
}

function Select({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (v: string) => void;
  options: string[];
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="h-9 w-full rounded-lg border border-line bg-bg px-3 text-sm text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      {options.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
    </select>
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
      <Field label="Backup transport" error={errFor("backup.transport")}>
        <Select
          value={draft.backup.transport}
          onChange={(v) => setDraft({ ...draft, backup: { ...draft.backup, transport: v } })}
          options={["auto", "usb", "wifi"]}
        />
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
      <Field label="Storages" error={errFor("storage")}>
        <p className="text-xs text-muted">
          {draft.storage === null
            ? "none declared — quince refuses to start without one (config.yml `storage:`)"
            : `${draft.storage.length} declared in config.yml — ${draft.storage
                .map((s) => `${s.name || s.path} (${s.backend})`)
                .join(", ")}`}
        </p>
      </Field>

      <Field label="Session TTL (minutes)" error={errFor("sessions.ttl_minutes")}>
        <Input
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
      </Field>

      <Field label="Theme" error={errFor("ui.theme")}>
        <Select
          value={draft.ui.theme}
          // Same shape as above. `ui:` has one key today so nothing is lost yet — which is
          // precisely why it is worth fixing now rather than when it is a bug.
          onChange={(v) => setDraft({ ...draft, ui: { ...draft.ui, theme: v } })}
          options={["system", "light", "dark"]}
        />
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
