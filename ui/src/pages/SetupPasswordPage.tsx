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
        nav("/", { replace: true });
      }}
    />
  );
}
