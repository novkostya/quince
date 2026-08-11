import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { LogOut } from "lucide-react";
import { APIError } from "@/lib/api";
import { authStatusKey, logout } from "@/lib/auth";

// Sign out — qn.6m slice 2, D8. IN THE SHELL RATHER THAN ON THE AUTH PAGE, which is the rung-local
// half quince#841 left open and named the sidebar as a legitimate answer to. Signing out is a
// navigation action, not a setting; burying it two clicks inside Settings → Auth would put the one
// control somebody reaches for in a hurry — a shared screen, a borrowed laptop — the furthest away.
//
// THE WHOLE PATH ALREADY EXISTED AND NOTHING ENTERED IT. `POST /api/auth/logout` is in contracts §1
// and in the storageless-reachable list, and `logout()` has sat in `lib/auth.ts` with NO CALLER
// anywhere in `ui/src/` or `ui/e2e/` — measured. So this is a button and a cache reset, not a new
// capability, which is why it carries no contract change.
export function SignOutButton() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  async function signOut() {
    setBusy(true);
    setFailed(false);
    try {
      await logout();
    } catch (err) {
      // A 401 IS THE GOAL STATE, NOT A FAILURE. The session this button is trying to end is already
      // gone — expired, or ended from another tab — so the user's intent is satisfied and reporting
      // an error would tell somebody who IS signed out that they are not. Every other failure is
      // real: the session is still live server-side, so the button must NOT clear local state and
      // pretend, which is the state-honesty rule applied to the one action whose whole point is
      // that it took effect.
      if (!(err instanceof APIError && err.status === 401)) {
        setFailed(true);
        setBusy(false);
        return;
      }
    }
    // SEED, THEN NAVIGATE, THEN DROP THE REST — and the order is load-bearing rather than tidy.
    //
    // `LoginGate` sends an `authenticated` status straight back to `/`, so navigating while the
    // cached status still says authenticated is a bounce, not a sign-out. Seeding first is the same
    // fix `SetupPasswordPage` carries for the same reason: seed the answer we already know rather
    // than invalidating and racing the refetch.
    //
    // The predicate keeps the seed alive while removing every protected payload — devices, config,
    // storages, versions. A plain `qc.clear()` would drop the seed too, putting `LoginGate` back
    // into `isLoading` and flashing "Loading…" over the form it is about to render.
    qc.setQueryData(authStatusKey, { state: "needs_login", csrf_token: "" });
    nav("/login", { replace: true });
    qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "auth" });
  }

  return (
    <div>
      <button
        type="button"
        onClick={signOut}
        disabled={busy}
        // `aria-label` IS NOT DECORATION HERE: the visible text below is `sm:`-only, so on a phone
        // this button is an icon and nothing else. Without a name it is "button" to a screen reader
        // — and to `getByRole("button", {name})`, which is how the tests and the e2e specs find it.
        aria-label="Sign out"
        className="flex min-h-[40px] w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm text-muted transition-colors hover:bg-elevated hover:text-fg disabled:opacity-60"
      >
        <LogOut size={18} strokeWidth={1.75} />
        {/* The LABEL is hidden on phones, where this sits in a horizontal bar beside Home and
            Settings and a third word would push the nav into wrapping. */}
        <span className="hidden sm:inline">{busy ? "…" : "Sign out"}</span>
      </button>
      {/* VISIBLE AT EVERY BREAKPOINT, and it was `sm:block` in the first draft of this file. The
          label above may hide on a phone because it is redundant with the icon; a failure is not
          redundant with anything. Hiding it would mean a phone user presses sign out, the session
          stays live, and NOTHING says so — a silent failure on the one action whose entire value is
          that it took effect. It costs a wrapped nav bar in a case that should not happen. */}
      {failed ? <div className="mt-1 px-3 text-xs text-danger">Could not sign out.</div> : null}
    </div>
  );
}
