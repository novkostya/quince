import { useCallback } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { PasswordForm } from "@/features/auth/PasswordForm";
import { usePasskeyLogin } from "@/features/auth/usePasskeyLogin";
import { authStatusKey, login } from "@/lib/auth";
import { useDemoResetMinutes, useIsPublicDemo } from "@/lib/health";
import { formatResetInterval } from "@/lib/format";

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
  const interval = formatResetInterval(useDemoResetMinutes());

  // A passkey sign-in lands in exactly the place a password one does — same query invalidation,
  // same `next`. The two paths converge here rather than in the hook, so there is one definition of
  // "signed in" and the hook stays a credential mechanism with no opinion about routing.
  // AWAITED, exactly as the password path below awaits it — and the difference was a real bug.
  // Navigating before the auth-status query has refetched leaves `RequireAuth` still seeing
  // `needs_login`, so it bounces straight back here, this page remounts, and the passkey ceremony
  // arms a SECOND time. Operator-reported: the sheet appeared again on the Home screen.
  const onPasskey = useCallback(async () => {
    await qc.invalidateQueries({ queryKey: authStatusKey });
    nav(next, { replace: true });
  }, [qc, nav, next]);
  usePasskeyLogin(onPasskey);

  return (
    <PasswordForm
      title="Sign in"
      subtitle={isPublicDemo ? "This is a public demo of quince." : "Enter your admin password."}
      cta="Sign in"
      // ARMED ON LOGIN ONLY. The setup page shares this component and must NOT arm it: there is
      // nothing to sign in to before a password exists (qn.6k).
      passkeys
      notice={
        isPublicDemo ? (
          <div className="mt-3 space-y-2 rounded-card border border-line bg-bg px-3 py-2 text-sm text-muted">
            <p>
              Password: <span className="font-mono font-semibold text-fg">{DEMO_PASSWORD}</span>
            </p>
            {/*
              Story 6, and the sentence is UNCONDITIONAL while the schedule is not. The reset wipes
              whatever a visitor has done, which is a destructive degraded mode — `no silent caps or
              fallbacks` says it is surfaced, and an instance whose deployment never declared an
              interval is exactly the one where a visitor is most likely to be surprised. So a
              missing interval costs the schedule, never the warning.
            */}
            <p>
              This demo resets {interval ? `every ${interval}` : "periodically"} — anything you
              change here will be wiped.
            </p>
          </div>
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
