import { useState } from "react";
import type { FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// Shared full-page password form for first-run setup and login.
export function PasswordForm({
  title,
  subtitle,
  cta,
  onSubmit,
}: {
  title: string;
  subtitle: string;
  cta: string;
  onSubmit: (password: string) => Promise<void>;
}) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await onSubmit(password);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg p-6 text-fg">
      <form onSubmit={submit} className="w-full max-w-sm rounded-card border border-line bg-card p-6">
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-base font-semibold">{title}</h1>
        <p className="mt-1 text-sm text-muted">{subtitle}</p>
        <div className="mt-4 flex flex-col gap-1">
          <Label htmlFor="password">Password</Label>
          <Input
            id="password"
            type="password"
            autoFocus
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {error ? <div className="mt-2 text-sm text-danger">{error}</div> : null}
        <Button type="submit" className="mt-4 w-full" disabled={busy || password.length === 0}>
          {busy ? "…" : cta}
        </Button>
      </form>
    </div>
  );
}
