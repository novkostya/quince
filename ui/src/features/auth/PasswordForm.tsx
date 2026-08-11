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
    //
    // `dvh` IS RIGHT HERE, AND THE UNIT IS NOT THE THING TO CHANGE (quince#659). An issue was filed
    // saying the opposite — that `dvh` with the toolbars hidden is "larger than the visible area" —
    // and that is the definition of `lvh`. Per CSS Values 4 the dynamic viewport equals the SMALL
    // viewport when the toolbars are expanded and the LARGE one when they retract, so it tracks what
    // is visible in BOTH states:
    //
    //     svh   toolbars retracted: SHORTER than visible  |  expanded: equals visible
    //     lvh   toolbars retracted: equals visible        |  expanded: TALLER than visible
    //     dvh   toolbars retracted: equals visible        |  expanded: equals visible
    //
    // `min-h-svh` here would make the box shorter than the viewport whenever the toolbars retract —
    // importing a background band onto the login screen to fix a problem that does not exist.
    //
    // WHAT `dvh` CAN DO is lag transiently during the toolbar animation, and whether that lag is a
    // defect depends on the SCROLL STRUCTURE around it rather than on the unit. `/setup`, `/login`
    // and `/onboarding/https` are siblings of the `AppLayout` route rather than children
    // (`router.tsx`), so the DOCUMENT scrolls here and the worst a lag costs is a brief scroll that
    // resolves itself. In the authed shell — `overflow-hidden`, no document scroll — the same lag
    // puts content where no scroller reaches it. That is quince#649, and it is a property of that
    // structure rather than of this unit.
    <div className="flex min-h-dvh items-start justify-center bg-bg pb-6 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(4rem,env(safe-area-inset-top))] text-fg sm:items-center sm:py-6">
      <form onSubmit={submit} className="w-full max-w-sm rounded-card border border-line bg-card p-6">
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-base font-semibold">{title}</h1>
        <p className="mt-1 text-sm text-muted">{subtitle}</p>
        {notice}
        {/* THE ANCHOR, NOT A CHOICE THE USER HAS — quince#819. A password manager keys a credential
            on (origin, username). quince asks for two DIFFERENT passwords on one origin — this one
            and the per-device backup password in `EncryptionDialog` — and until this field existed
            neither declared a username, so both collapsed to origin-only entries that iCloud
            Keychain filed together. `quince-admin` is a constant because quince is single-admin;
            the differentiation it buys is against the OTHER surface, not between accounts here.

            `readOnly` AND VISIBLE, rather than suppressed. Chromium's own form guidance accepts a
            hidden input for this ("include a hidden input field containing this information even if
            it is not directly necessary for your form"), but the browser this was reported against
            is Safari and no authoritative source was found either way for WebKit — and a
            `display:none` field is the variant most likely to be ignored. Visible is the option that
            cannot be skipped for being invisible, so it is the safe default until G1 says otherwise.

            NOT `disabled`: a disabled control is excluded from form submission and is skipped by
            autofill, which would defeat the whole point.

            `tabIndex={-1}` BECAUSE THERE IS NOTHING TO DO HERE — quince#824. A read-only anchor
            defaults to `tabindex=0`, so it sits in the tab order and can hold the focus ring ahead
            of the field the user actually types in. It stays FOCUSABLE (`.focus()` still works,
            unlike `disabled`) and stays visible to autofill: developers trying to SUPPRESS Chrome
            and Safari autofill with exactly `readonly` + tab tricks report the managers ignore
            them, which is the same property working in our favour here.

            AND DO NOT ADD MORE AUTOFOCUS MACHINERY TO CHASE THE iOS KEYBOARD. `autoFocus` below
            already focuses the password correctly — measured on both engines against a real build,
            `focused=input#password` in Chromium AND WebKit. iOS deliberately declines to raise the
            keyboard for a programmatic focus that did not come from a user gesture, so the opening
            tap on a phone is a platform decision and CANNOT be removed from this page. A
            `setTimeout` + `.focus()` is the shape that looks like it should fix it; it does not,
            and it is how this comment came to be written (quince#824). */}
        <div className="mt-4 flex flex-col gap-1">
          <Label htmlFor="username">Username</Label>
          <Input
            id="username"
            name="username"
            type="text"
            autoComplete="username"
            readOnly
            tabIndex={-1}
            value="quince-admin"
          />
        </div>
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
