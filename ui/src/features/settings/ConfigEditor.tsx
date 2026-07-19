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

      <Field label="Storage backend" error={errFor("storage.backend")}>
        <Select
          value={draft.storage.backend}
          onChange={(v) => setDraft({ ...draft, storage: { ...draft.storage, backend: v } })}
          options={["auto", "zfs", "reflink", "hardlink", "copy"]}
        />
      </Field>

      <Field label="Session TTL (minutes)" error={errFor("sessions.ttl_minutes")}>
        <Input
          type="number"
          min={1}
          value={draft.sessions.ttl_minutes}
          onChange={(e) =>
            setDraft({ ...draft, sessions: { ttl_minutes: Number(e.target.value) } })
          }
        />
      </Field>

      <Field label="Theme" error={errFor("ui.theme")}>
        <Select
          value={draft.ui.theme}
          onChange={(v) => setDraft({ ...draft, ui: { theme: v } })}
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
