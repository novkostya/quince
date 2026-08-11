import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { PasswordForm } from "@/features/auth/PasswordForm";
import { authStatusKey, setup } from "@/lib/auth";

export function SetupPasswordPage() {
  const nav = useNavigate();
  const qc = useQueryClient();
  return (
    <PasswordForm
      title="Set an admin password"
      subtitle="This protects quince and your backups — you'll use it to sign in."
      cta="Set password and continue"
      // A PAGE, NOT A CARD — ruling A on quince#841, spec D1. Measured before the change: the two
      // onboarding steps either side of this one are `max-w-2xl` and `max-w-xl` with no card, while
      // the auth surfaces were `max-w-sm rounded-card`. Two and two, and this was the odd pair.
      //
      // `OnboardingStoragePage`'s own header comment already carries the reasoning: "A first-run
      // step is a DESTINATION, not an interruption." `/login` deliberately does NOT take this — it
      // is a recurring destination on an existing install rather than a step (D1).
      variant="page"
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
        // STRAIGHT INTO THE PASSKEY OFFER (qn.6k story 9), not to Home. This is the one moment the
        // user is certainly at a device they own, having just chosen a password — which is why the
        // ruling puts the offer in onboarding. The page renders nothing and forwards to `/` where
        // passkeys cannot work, so it never adds a step to a deployment with no use for one.
        nav("/onboarding/passkey", { replace: true });
      }}
    />
  );
}
