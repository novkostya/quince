import { useState } from "react";
import type { ReactNode } from "react";
import type { FormEvent } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { APIError } from "@/lib/api";
import { signInWithPasskey, webauthnAvailable } from "@/lib/webauthn";
import { AuthPage, type AuthVariant } from "./AuthPage";

// Shared password form for first-run setup and login. The BOX it sits in is `AuthPage`'s (qn.6m
// slice 3); everything below is the fields, the errors and the two buttons.
export function PasswordForm({
  title,
  subtitle,
  cta,
  notice,
  extra,
  footer,
  variant = "card",
  passkeys = false,
  password: wantPassword = true,
  passkeyProof,
  onPasskey,
  onSubmit,
}: {
  title: string;
  subtitle: string;
  cta: string;
  notice?: ReactNode;
  // Rendered between the password field and the submit button — the setup surface's passkey offer.
  extra?: ReactNode;
  // Rendered AFTER the submit button and the passkey sign-in button — a secondary path away from
  // this form entirely (first-run passwordless, qn.6m D5), rather than another input to it. Below
  // the primary action because that is what "secondary" means on a screen with one obvious job.
  footer?: ReactNode;
  // `card` DEFAULT, `page` on setup — ruling A on quince#841, spec D1. Login is not an onboarding
  // step and keeps the card, so the default is the shape that must not change rather than the one
  // being introduced: a surface that forgets to pass this stays exactly as it was.
  variant?: AuthVariant;
  // passkeys arms conditional mediation on this form (qn.6k). FALSE ON THE SETUP PAGE, and
  // deliberately: there is nothing to sign in to before a password exists, and the browser would be
  // asked to offer a credential for an account that has not been created.
  passkeys?: boolean;
  // `password` — render the password field at all. DEFAULT TRUE, which is `variant`'s rule for the
  // same reason: a surface that forgets to pass this stays exactly as it was, so login and setup
  // cannot be changed by this prop existing.
  //
  // FALSE IS FOR THE CHALLENGE ONLY (qn.6o D5/G5), on a PASSWORDLESS install, where `accepts` omits
  // the password because there is no password to type. Rendering it anyway would offer a field that
  // cannot succeed — `qn.6g`'s rule, that a remedy the user cannot follow is the same defect as a
  // silent failure.
  password?: boolean;
  // `passkeyProof` — qn.6o D5. The passkey button PROVES a present credential instead of signing in.
  //
  // A SEPARATE PROP FROM `passkeys`, NOT A MODE ON IT, because the two differ in the one way that
  // matters: `passkeys` arms CONDITIONAL MEDIATION — browser autofill on load — and a challenge must
  // be modal. `lib/reauth.ts` states that rule: *"a non-modal request sitting in an autofill dropdown
  // is for a login form nobody has committed to yet."* Overloading one boolean would make the modal
  // rule depend on remembering which value means which, which is the shape that gets passed the
  // wrong way round.
  //
  // Present → the button renders and runs this instead of `signInWithPasskey`. `passkeys` stays
  // false, and G6 asserts that on the prop, because an autofill prompt on a challenge is invisible
  // in jsdom.
  passkeyProof?: { cta: string; run: () => Promise<void> };
  // onPasskey is called after a successful passkey sign-in, so the page can route exactly as it
  // does after a password one. Absent on setup, where there is nothing to sign in to yet.
  onPasskey?: () => void | Promise<void>;
  onSubmit: (password: string) => Promise<void>;
}) {
  const [password, setPassword] = useState("");
  // The CODE is kept alongside the message, not just the prose. `insecure_origin` is the one
  // failure a user cannot act on from this form — no password will ever work over this
  // connection — so it is the one that has to offer somewhere else to go.
  const [error, setError] = useState<{ message: string; code?: string } | null>(null);
  const [busy, setBusy] = useState(false);

  // THE EXPLICIT PASSKEY BUTTON — Operator-raised, and it is what makes the feature findable.
  //
  // Conditional mediation puts the passkey in the browser's autofill dropdown, which is elegant and
  // INVISIBLE: on hardware the Operator only found it by tapping the key icon on the iOS keyboard,
  // past a suggestion list whose first entry was a password. A feature nobody can find is not a
  // feature, and this is how every bank does it.
  //
  // It does not contradict the ruling against an unconditional modal. That objection was about
  // firing a sheet at users who have no passkey — credential presence is undetectable, so an
  // unprompted modal guesses wrong for everyone without one. A BUTTON cannot make that mistake: the
  // user asked, and "no passkey here" is a fine answer to a question they posed.
  //
  // It also gets its own user activation, which the conditional path cannot share: iOS 16+ grants
  // exactly ONE gesture-free `credentials.get()` per page load, and arming conditional mediation on
  // mount consumes it. The click is a second, fresh one.
  async function passkeySignIn() {
    setBusy(true);
    setError(null);
    try {
      // THE CHALLENGE'S CEREMONY WHEN IT SUPPLIED ONE, and the sign-in ceremony otherwise. Both are
      // modal `credentials.get()` calls behind this same button, so the error handling below is
      // genuinely shared rather than coincidentally similar — `NotAllowedError` means the same
      // thing to both, and neither can tell a cancellation from an absent credential.
      if (passkeyProof) {
        await passkeyProof.run();
        return;
      }
      await signInWithPasskey({ conditional: false });
      await onPasskey?.();
    } catch (err) {
      if (err instanceof APIError) {
        setError({ message: err.message, code: err.code });
      } else if (err instanceof Error && err.name === "NotAllowedError") {
        // Cancelled or not permitted — the browser will not say which, so the message says what is
        // true of both rather than guessing at one. Never silent: the user pressed a button.
        setError({ message: "No passkey was used — the request was cancelled, or this device has none for quince." });
      } else {
        setError({ message: "Could not sign in with a passkey." });
      }
    } finally {
      setBusy(false);
    }
  }

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
    // THE SHELL LIVES IN `AuthPage` NOW (qn.6m slice 3) — the `dvh` reasoning, the safe-area padding
    // and the two box shapes moved there intact, because they are true of every auth surface and
    // this component is no longer the only one.
    <AuthPage variant={variant} title={title} subtitle={subtitle} notice={notice} onSubmit={submit}>
      {/* THE CONTROLS ARE CAPPED, THE PAGE IS NOT — Operator direction, 2026-08-17, and the same rule
          `AddStorageForm` follows with its own `fieldWidth`. A username, a password and one button do
          not get easier to use by being as wide as the prose above them; at the page's full measure
          they read as essay fields.

          `max-w-md` RATHER THAN THE PAGE'S `2xl`, and rather than the storage form's `xl`: this form
          holds two short values where that one holds paths and dataset names. Same rule, different
          content, which is why the number lives with the form and not with the page.

          IT WRAPS THE WHOLE COLUMN, so the submit button and the passkey row cannot drift out of line
          with the fields the next time one of them is edited on its own. */}
      <div className="max-w-md">
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
            autoComplete={passkeys ? "username webauthn" : "username"}
            readOnly
            tabIndex={-1}
            value="quince-admin"
          />
        </div>
        {wantPassword ? (
          <div className="mt-4 flex flex-col gap-1">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              autoFocus
              autoComplete={passkeys ? "current-password webauthn" : "current-password"}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
        ) : null}
        {/* `extra` — qn.6m slice 4. Whatever the surface wants BETWEEN the password and its button:
            on setup that is the passkey offer, which is how quince#841 item 2 gets both options onto
            one screen without a second route or a dialog.

            ABOVE the error and the CTA on purpose. It is part of what the button is about to do, so
            it belongs on the button's side of any message explaining why the last attempt failed. */}
        {extra}
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
        {/* NO PASSWORD FIELD MEANS NO SUBMIT BUTTON. It is disabled on an empty password, so leaving
            it would render a permanently dead primary action next to the one control that works —
            *"no silent caps or fallbacks"* read at the level of a button nobody can press. */}
        {wantPassword ? (
          <Button type="submit" className="mt-4 w-full" disabled={busy || password.length === 0}>
            {busy ? "…" : cta}
          </Button>
        ) : null}

        {/* SHOWN WHENEVER PASSKEYS ARE ARMED, not only when one exists — because whether this device
            has one is UNDETECTABLE. Hiding it until we "know" is not an option the platform offers,
            and a button that says what it does costs a user without a passkey one tap and a plain
            sentence, where its absence costs a user WITH one the entire feature.

            `type="button"`, because components/ui/button.tsx sets none and a Button inside a form is
            a submit by default (quince#824, quince#828). Here that would post an empty password.

            AND NOT WHERE THE BROWSER CANNOT RUN A CEREMONY AT ALL (quince#1076). The paragraph above
            is about PRESENCE, which is undetectable; `webauthnAvailable()` is about AVAILABILITY,
            which is one expression. Over plain http there is no `PublicKeyCredential`, so this button
            threw into the generic catch and answered *"Could not sign in with a passkey."* — which
            reads as *you have none* to someone who has one, and says nothing about the connection
            being the reason. The two are different claims and only one of them justifies hiding the
            button.

            `passkeyProof` IS DELIBERATELY NOT GATED, and the asymmetry is the point. That prop is the
            REAUTH path — `ReauthChallenge` renders this form inside its dialog — and the server has
            already said which factors it accepts. Where it accepts only a passkey, hiding the button
            leaves a dialog asking the reader to confirm with nothing to confirm by, which is a worse
            failure than a button that explains itself and is quince#1077's subject rather than this
            one's. quince#1076 names three surfaces; this is the login one. */}
        {(passkeys && webauthnAvailable()) || passkeyProof ? (
          <Button
            type="button"
            variant="outline"
            className="mt-2 w-full"
            disabled={busy}
            onClick={passkeySignIn}
          >
            {passkeyProof ? passkeyProof.cta : "Sign in with a passkey"}
          </Button>
        ) : null}

        {/* SAID ONCE, IN PLACE OF THE BUTTON — not in addition to it. A reader on a LAN address
            otherwise has no way to tell "this quince has no passkeys" from "this connection cannot
            do them", and those want opposite next actions. It is net LESS on the screen than the
            control it replaces. */}
        {passkeys && !webauthnAvailable() ? (
          <p className="mt-3 text-sm text-muted">
            Passkeys need an https address. Sign in with your password here, or{" "}
            <Link to="/onboarding/https" className="underline underline-offset-2">
              set up https
            </Link>{" "}
            to use one.
          </p>
        ) : null}
        {footer}
      </div>
    </AuthPage>
  );
}
