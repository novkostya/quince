import { useNavigate, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { PasswordForm } from "@/features/auth/PasswordForm";
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

  return (
    <PasswordForm
      title="Sign in"
      subtitle={isPublicDemo ? "This is a public demo of quince." : "Enter your admin password."}
      cta="Sign in"
      notice={
        isPublicDemo ? (
          <div className="mt-3 space-y-2 rounded-card border border-line bg-bg px-3 py-2 text-sm text-muted">
            <p>
              Password:{" "}
              {/*
                data-testid is what lets the e2e read this password OFF THE SCREEN and log in with
                exactly that string (quince#534). The two constants are deliberately NOT
                un-duplicated — serving the password from /api/health would put a credential on an
                authExempt endpoint — so the only honest guard is behavioural: whatever is rendered
                here must actually work. Locating it by styling class instead would break the guard
                the day somebody restyles this notice, which is the wrong thing to be fragile to.
              */}
              <span className="font-mono font-semibold text-fg" data-testid="demo-password">
                {DEMO_PASSWORD}
              </span>
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
