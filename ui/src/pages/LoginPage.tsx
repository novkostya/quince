import { useNavigate, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { PasswordForm } from "@/features/auth/PasswordForm";
import { authStatusKey, login } from "@/lib/auth";

export function LoginPage() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const next = params.get("next") ?? "/";
  return (
    <PasswordForm
      title="Sign in"
      subtitle="Enter your admin password."
      cta="Sign in"
      onSubmit={async (pw) => {
        await login(pw);
        await qc.invalidateQueries({ queryKey: authStatusKey });
        nav(next, { replace: true });
      }}
    />
  );
}
