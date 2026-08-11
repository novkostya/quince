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
      onSubmit={async (pw) => {
        await setup(pw);
        await qc.invalidateQueries({ queryKey: authStatusKey });
        // STRAIGHT INTO THE PASSKEY OFFER (qn.6k story 9), not to Home. This is the one moment the
        // user is certainly at a keyboard-and-authenticator they own, having just chosen a
        // password — the ruling puts the offer in onboarding for that reason. The page renders
        // nothing and forwards to `/` where passkeys cannot work, so this never adds a step to a
        // deployment that has no use for one.
        nav("/onboarding/passkey", { replace: true });
      }}
    />
  );
}
