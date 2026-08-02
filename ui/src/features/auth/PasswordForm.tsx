import { useState } from "react";
import type { ReactNode } from "react";
import type { FormEvent } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { APIError } from "@/lib/api";

// Shared full-page password form for first-run setup and login.
export function PasswordForm({
  title,
  subtitle,
  cta,
  notice,
  onSubmit,
}: {
  title: string;
  subtitle: string;
  cta: string;
  notice?: ReactNode;
  onSubmit: (password: string) => Promise<void>;
}) {
  const [password, setPassword] = useState("");
  // The CODE is kept alongside the message, not just the prose. `insecure_origin` is the one
  // failure a user cannot act on from this form — no password will ever work over this
  // connection — so it is the one that has to offer somewhere else to go.
  const [error, setError] = useState<{ message: string; code?: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await onSubmit(password);
    } catch (err) {
      if (err instanceof APIError) {
        setError({ message: err.message, code: err.code });
      } else {
        setError({ message: err instanceof Error ? err.message : "Something went wrong" });
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    // min-h-dvh (not 100vh) so it matches the visible area on a phone — no stray scroll. On a phone
    // the form sits toward the top so the keyboard / Face ID sheet has room below it (dead-centering
    // looks unbalanced once the sheet slides up); on desktop it centers. Safe-area padding keeps it
    // clear of the status bar / side notch (qn.6a soak fixes).
    <div className="flex min-h-dvh items-start justify-center bg-bg pb-6 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(4rem,env(safe-area-inset-top))] text-fg sm:items-center sm:py-6">
      <form onSubmit={submit} className="w-full max-w-sm rounded-card border border-line bg-card p-6">
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-base font-semibold">{title}</h1>
        <p className="mt-1 text-sm text-muted">{subtitle}</p>
        {notice}
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
        {error ? (
          <div className="mt-2 text-sm text-danger">
            {error.message}
            {/* THE ONE ERROR THIS FORM CANNOT HELP WITH. Every other failure here has an action
                the user can take in this box — retype the password, wait out the rate limit. An
                insecure origin means NO password will ever work on this connection, so the only
                useful thing the form can do is point somewhere else.

                Keyed on the CODE, not on `window.location.protocol`, and that is the whole reason
                it is correct: the server sends `insecure_origin` exactly when the cookie would be
                discarded, which is NOT the same as "this is http". On `http://localhost`, in
                `--demo`, and with `sessions.allow_insecure_transport` on, login works fine over
                plain http and this link must not appear. A client-side scheme check would show it
                to all three (quince#559 discussion). */}
            {error.code === "insecure_origin" ? (
              <>
                {" "}
                <Link to="/onboarding/https" className="underline underline-offset-2">
                  How to fix this
                </Link>
              </>
            ) : null}
          </div>
        ) : null}
        <Button type="submit" className="mt-4 w-full" disabled={busy || password.length === 0}>
          {busy ? "…" : cta}
        </Button>
      </form>
    </div>
  );
}
