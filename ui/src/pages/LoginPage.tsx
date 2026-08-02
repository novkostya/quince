import { useNavigate, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { PasswordForm } from "@/features/auth/PasswordForm";
import { authStatusKey, login } from "@/lib/auth";
import { useIsPublicDemo } from "@/lib/health";

// DEMO_PASSWORD mirrors the server constant, ruled on quince#444 and published deliberately. It is
// hardcoded rather than served because a password the server SENDS to an unauthenticated client is
// a different and much worse thing than one both sides happen to know — the mode is the only fact
// that crosses the wire, and /api/health never carries a credential.
const DEMO_PASSWORD = "demo";

export function LoginPage() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const next = params.get("next") ?? "/";
  const isPublicDemo = useIsPublicDemo();

  return (
    <PasswordForm
      title="Sign in"
      subtitle={isPublicDemo ? "This is a public demo of quince." : "Enter your admin password."}
      cta="Sign in"
      notice={
        isPublicDemo ? (
          <p className="mt-3 rounded-card border border-line bg-bg px-3 py-2 text-sm text-muted">
            Password: <span className="font-mono font-semibold text-fg">{DEMO_PASSWORD}</span>
          </p>
        ) : null
      }
      onSubmit={async (pw) => {
        await login(pw);
        await qc.invalidateQueries({ queryKey: authStatusKey });
        nav(next, { replace: true });
      }}
    />
  );
}
