import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { AuthPage } from "@/features/auth/AuthPage";
import { PasswordForm } from "@/features/auth/PasswordForm";
import { APIError } from "@/lib/api";
import { authStatusKey, setup } from "@/lib/auth";
import { registerPasskey } from "@/lib/webauthn";

// FIRST RUN IS ONE SCREEN — quince#841 item 2, qn.6m stories 1-5. Password and, where the device can
// hold one, a passkey: both on one page, no dialog, and NO SECOND ROUTE. `/onboarding/passkey`
// existed to be that second route and is deleted with this change, which is also the ruled fix for
// quince#840 (the offer never rendered) — the screen stops existing rather than being debugged.

// PASSKEY CAPABILITY IS A CLIENT-SIDE GUESS HERE, AND IT HAS TO BE. The authoritative answer is
// `GET /api/auth/passkeys` → `supported`, which needs a SESSION — and on this screen there is none
// yet, because creating one is what the button does. So:
//
//   - the browser half is knowable now  — `PublicKeyCredential` exists, and the origin is secure
//   - the SERVER half is not            — whether this address can be a relying party at all (an
//                                         rpId must be a domain, so a bare IP cannot) is only
//                                         answerable once we can call the API
//
// The offer is therefore shown on the browser test alone, and the server's refusal is handled when
// it arrives (`passkeys_unsupported_here`, contracts §1) by SAYING SO rather than failing. That is
// story 5 honoured as far as this screen can honour it: a tier that cannot work is named at the
// first moment it can be known, which is after the password exists.
function browserCanPasskey(): boolean {
  return typeof window.PublicKeyCredential !== "undefined" && window.isSecureContext;
}

// The name a first-run passkey gets.
//
// NOT A FIELD, deliberately: naming a credential matters when there are several to tell apart, and
// on first run there is exactly one. Settings renames it (`PATCH /api/auth/passkeys/{id}`), which is
// the surface where the question is worth asking.
//
// AND THIS SCREEN IS THE WRONG PLACE TO ASK — Operator, 2026-08-15, on a proposal to put a name
// field here for consistency with the add row's. That row is single-purpose; this screen already
// carries a username anchor, a password, a passkey checkbox, a primary action AND a separate
// passwordless path below it. A name field would have to appear conditionally under one entry point
// and be duplicated for the other, on the screen where friction is most expensive. *"Sounds good on
// paper but in reality it's a tough UI/UX challenge."*
//
// IT WAS "This device" UNTIL 2026-08-15, AND THAT WAS WRONG IN A SPECIFIC WAY WORTH KEEPING.
// The phrase is DEICTIC — its meaning depends on where the reader is standing. It was true at the
// instant of creation and false the moment the list was read from anywhere else: the Operator met it
// on a Mac, describing a credential made on an iPhone. iCloud Keychain makes it worse, since the
// credential is not on one device at all.
//
// SO THE DEFECT IS A LABEL PERSISTED AND READ LATER, NOT THE PHRASE. The checkbox on this same
// screen still says *"Also set up a passkey on this device"* and is left alone: it is read once, at
// the moment it is true, describing what is about to happen rather than labelling a row forever.
//
// `admin` RATHER THAN `First passkey` — Operator: *"I don't want to name passkey passkey. You would
// name other passkeys iPhone, iPad, etc, NOT iPhone passkey."* Right: this list IS passkeys, so a
// category word is `file1.txt` inside a Files folder. `admin` is a noun that sits beside `iPhone` and
// `mac`, matches the `quince-admin` anchor this screen already shows, and can never become false —
// a first-run credential is the admin's however it is later read.
//
// WHAT IT DOES NOT DO IS DISTINGUISH, and that is accepted rather than missed: quince is
// single-admin, so every passkey here is the admin's. Uninformative beats wrong.
const FIRST_PASSKEY_NAME = "admin";

type Outcome = { kind: "dismissed" } | { kind: "unsupported" } | { kind: "failed"; message: string };

export function SetupPasswordPage() {
  const nav = useNavigate();
  const qc = useQueryClient();
  // OFF BY DEFAULT — Operator, 2026-08-15. It was ON, so a first run that just pressed the primary
  // button got an authenticator sheet it had not asked for, in the middle of setting a password.
  //
  // AN OPT-IN IS WHAT A SECOND CREDENTIAL SHOULD BE. The passkey is genuinely optional here — the
  // password alone is a complete setup, and the screen already offers a THIRD path below for people
  // who want a passkey instead. Pre-ticking it makes the common case carry a ceremony, and a sheet
  // nobody asked for is the exact shape `PasswordForm` refuses on the login screen for its own
  // reasons.
  //
  // IT ALSO REMOVES A FAILURE FROM THE HAPPY PATH. A dismissed or unsupported sheet lands the user in
  // the outcome panel with a red line — *"No passkey was added"* — after an action that SUCCEEDED,
  // because the password was set either way. Off by default, that message only appears for somebody
  // who asked for the thing that failed.
  const [wantPasskey, setWantPasskey] = useState(false);
  // Set once the password exists. From that moment the user is SIGNED IN and this screen is no
  // longer a setup form — which is why the outcome panel below REPLACES it rather than sitting under
  // it. Re-submitting would 409, and the only thing left to do is get on with it.
  const [outcome, setOutcome] = useState<Outcome | null>(null);

  const [passwordlessBusy, setPasswordlessBusy] = useState(false);
  const [passwordlessErr, setPasswordlessErr] = useState<string | null>(null);

  const offer = browserCanPasskey();
  const done = () => nav("/", { replace: true });

  // GO PASSWORDLESS AT FIRST RUN — quince#841 item 3, using D5's pre-auth pair.
  //
  // ON THE BUTTON'S OWN CLICK, not behind an effect or a navigation: `navigator.credentials.create()`
  // needs live user activation, and this is the gesture that authorises it.
  //
  // NO PASSWORD IS SET, EVER — that is the whole point. `setup/passkey/finish` issues the session
  // itself, so there is no moment where a password exists and is then removed. The rejected
  // alternative was exactly that (generate one, register, delete it), and it strands the user behind
  // a password they never saw if registration fails halfway.
  async function goPasswordless() {
    setPasswordlessBusy(true);
    setPasswordlessErr(null);
    try {
      const added = await registerPasskey(FIRST_PASSKEY_NAME, { firstRun: true });
      if (!added) {
        // Dismissed, not failed. NOTHING HAS CHANGED — no password, no credential, no session — so
        // the user is exactly where they started and the form below is still the way forward.
        setPasswordlessErr("No passkey was added — the request was cancelled or timed out.");
        return;
      }
      // The session arrives with the credential, so the status the guards read must be seeded
      // before navigating — the same race `onSubmit` documents below.
      qc.setQueryData(authStatusKey, { state: "authenticated", csrf_token: "" });
      done();
    } catch (err) {
      // `already_configured` IS SHOWN, NOT REDIRECTED PAST, AND THAT IS A DELIBERATE REFUSAL —
      // Operator, 2026-08-15. A redirect to sign-in was proposed and withdrawn on better reasoning:
      //
      //   > that case might mean something really bad has just happened
      //
      // This screen only renders while the install is UNCLAIMED. A 409 here means somebody claimed
      // it between the page loading and this button being pressed — which on a network-reachable
      // first run is not a stale tab, it is somebody else taking the install. quince#888 already
      // names an unauthenticated takeover as a live shape in this product.
      //
      // SO THE REMEDY IS NOT A BUTTON, IT IS NOTICING. Sending the user to sign in would answer the
      // one event that most deserves a stop with the most reassuring thing the app can say, and the
      // user would arrive at a login form for an account they never created.
      setPasswordlessErr(
        err instanceof APIError ? err.message : "Could not set up a passkey on this device.",
      );
    } finally {
      setPasswordlessBusy(false);
    }
  }

  // THE PANEL SHOWN WHEN THE PASSWORD IS SET AND THE PASSKEY IS NOT. Every one of these is a state
  // where the install is FINE — the user has an admin password and a session — so none of them is an
  // error page, and all of them offer the same one-tap way onward.
  if (outcome) {
    return (
      <AuthPage
        variant="page"
        title="Password set"
        subtitle={
          outcome.kind === "unsupported"
            ? "quince is protected. A passkey is not possible at this address."
            : "quince is protected. You can add a passkey later, in Settings."
        }
      >
        <div className="mt-4 text-sm text-muted">
          {outcome.kind === "unsupported" ? (
            // The rpId rule, stated as a fact about the ADDRESS rather than about the device — the
            // same distinction the settings surface draws (qn.6k D2), so a user who moves to a
            // domain later is not left thinking their phone was the problem.
            <p>
              A passkey is tied to a domain name, so it cannot be created when quince is reached at a
              bare IP address. Reach quince by a hostname and Settings will offer one.
            </p>
          ) : outcome.kind === "dismissed" ? (
            <p>No passkey was added — the request was cancelled or timed out.</p>
          ) : (
            <p>{outcome.message}</p>
          )}
        </div>
        <Button className="mt-4 w-full" onClick={done}>
          Continue to quince
        </Button>
      </AuthPage>
    );
  }

  return (
    <PasswordForm
      title="Set an admin password"
      subtitle="This protects quince and your backups — you'll use it to sign in."
      cta="Set password and continue"
      // A PAGE, NOT A CARD — ruling A on quince#841, spec D1. `/login` deliberately does not take
      // this: it is a recurring destination on an existing install rather than a first-run step.
      variant="page"
      extra={
        offer ? (
          <label className="mt-4 flex items-start gap-2.5 text-sm">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={wantPasskey}
              onChange={(e) => setWantPasskey(e.target.checked)}
            />
            <span>
              <span className="font-medium">Also set up a passkey on this device</span>
              {/* CHECKED BY DEFAULT, and that is a recommendation rather than a default nobody
                  chose. quince#841's destination is a phone-first, password-optional install, and
                  the whole reason qn.6k exists is that typing an admin password on a phone keyboard
                  is the worst part of using quince. Opting OUT is one tap and the password works
                  either way, so the cost of the default being wrong is negligible — where the cost
                  of the feature going unfound is the entire rung, which is exactly what happened to
                  conditional mediation on hardware. */}
              <span className="mt-0.5 block text-muted">
                Sign in with Face ID or Touch ID instead of typing your password. You will be asked
                right after the password is set.
              </span>
            </span>
          </label>
        ) : null
      }
      footer={
        // THE PASSWORDLESS OPTION — quince#841 item 3, ruling B. D5's pre-auth pair is what makes it
        // reachable AT FIRST RUN at all: ordinary registration is session-required and first run has
        // no session, so this button uses `/api/auth/setup/passkey/*`, which is one-shot and closes
        // the moment the install is claimed.
        //
        // BELOW THE PRIMARY ACTION AND QUIETER, deliberately. A password is the right default for
        // most installs — it needs no second device and no recovery story — so this is an option
        // somebody goes looking for rather than one they fall into.
        offer ? (
          <div className="mt-6 border-t border-line pt-4">
            <p className="text-sm text-muted">
              Or skip the password entirely and use a passkey as your only way in.
            </p>
            {/* THE COST, ON THE SCREEN THAT OFFERS IT — D7, the same rule the settings surface
                follows. Shorter here on purpose: this is first run, there is no install to lose yet,
                and the choice is reversible from Settings the moment they are in. The sentence that
                cannot be dropped is the SHELL one, because it is the fact that makes this unsuitable
                for a box they cannot physically reach — and the one they cannot work out alone. */}
            <p className="mt-2 text-sm text-muted">
              If you lose the device holding it, the only way back is{" "}
              <code className="font-mono text-fg">quince auth reset</code> on the machine running
              quince — so you need console or SSH access to it.
            </p>
            {passwordlessErr ? <p className="mt-2 text-sm text-danger">{passwordlessErr}</p> : null}
            <Button
              type="button"
              variant="outline"
              className="mt-3"
              disabled={passwordlessBusy}
              onClick={goPasswordless}
            >
              {passwordlessBusy ? "…" : "Use a passkey instead"}
            </Button>
          </div>
        ) : null
      }
      onSubmit={async (pw) => {
        // SEEDED, NOT INVALIDATED — and TWO gates are why. This page sits inside `SetupGate`
        // (`state !== "needs_setup"` → Navigate to "/") and the destination sits inside
        // `RequireAuth`. An INVALIDATION is a refetch, so the status is briefly stale whichever
        // order you pick, and each order loses to a different gate:
        //
        //   invalidate, then navigate → the status flips while still under SetupGate, which
        //                               redirects to Home and wins. Measured: setup landed on
        //                               Home and /api/auth/passkeys was never requested.
        //   navigate, then invalidate → RequireAuth reads the STALE status and bounces to
        //                               /login?next=/, then LoginGate sends it on to Home.
        //                               Measured too, after "fixing" the first one.
        //
        // `setup` already returns the authenticated status, so writing it into the cache makes the
        // transition synchronous: there is no window in which either gate can see a stale value.
        const status = await setup(pw);
        qc.setQueryData(authStatusKey, status);

        if (!offer || !wantPasskey) {
          done();
          return;
        }

        // THE REGISTRATION RUNS INSIDE THIS SUBMIT HANDLER, ON THE USER'S OWN CLICK. It has to:
        // `navigator.credentials.create()` requires live user activation, and moving it behind a
        // `useEffect` or a navigation would sever it from the gesture that authorised it.
        //
        // The `setup` call above sits between the tap and `create()`, which looked like a hazard
        // when this was designed and is NOT A NEW ONE: `registerPasskey` already issues
        // `register/begin` in the same gap, and that ships and works on hardware. This adds one more
        // local round trip inside the same activation window, not a different shape.
        try {
          // THE PASSWORD IS PRESENTED, AND WITHOUT IT THIS FLOW IS BROKEN — rule 1, found in review
          // of quince#930. `setup` above has just claimed the install, so by the time
          // `register/begin` runs the server sees `configured`, WITH a password and NO credentials,
          // and demands a present one. The only credential that exists is the password typed into
          // this form; `RequirePresent` would otherwise verify an empty string against the hash and
          // answer `bad_password` about a field this screen never showed.
          const added = await registerPasskey(FIRST_PASSKEY_NAME, { currentPassword: pw });
          // FALSE MEANS DISMISSED, NOT FAILED — the sheet was cancelled, timed out, or the
          // authenticator refused as already-registered. The user ends up where they started, so
          // this is reported as a fact and never as an error (`lib/webauthn.ts`).
          if (!added) {
            setOutcome({ kind: "dismissed" });
            return;
          }
          done();
        } catch (err) {
          // NOTHING IS ALLOWED TO THROW PAST THIS POINT, and that is the whole reason the panel
          // exists. The password is already set and the session already issued, so an exception
          // escaping to `PasswordForm`'s catch would put a red error on a form whose submit now
          // 409s — telling somebody whose install is FINE that their setup failed, with no way on.
          if (err instanceof APIError && err.code === "passkeys_unsupported_here") {
            setOutcome({ kind: "unsupported" });
            return;
          }
          setOutcome({
            kind: "failed",
            message: err instanceof APIError ? err.message : "The passkey could not be added.",
          });
        }
      }}
    />
  );
}
