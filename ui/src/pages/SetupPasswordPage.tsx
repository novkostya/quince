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

// The name a first-run passkey gets. NOT A FIELD, deliberately: naming a credential matters when
// there are several to tell apart, and on first run there is exactly one. Settings renames it
// (`PATCH /api/auth/passkeys/{id}`), which is the surface where the question is worth asking.
const FIRST_PASSKEY_NAME = "This device";

type Outcome = { kind: "dismissed" } | { kind: "unsupported" } | { kind: "failed"; message: string };

export function SetupPasswordPage() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [wantPasskey, setWantPasskey] = useState(true);
  // Set once the password exists. From that moment the user is SIGNED IN and this screen is no
  // longer a setup form — which is why the outcome panel below REPLACES it rather than sitting under
  // it. Re-submitting would 409, and the only thing left to do is get on with it.
  const [outcome, setOutcome] = useState<Outcome | null>(null);

  const offer = browserCanPasskey();
  const done = () => nav("/", { replace: true });

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
          const added = await registerPasskey(FIRST_PASSKEY_NAME);
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
