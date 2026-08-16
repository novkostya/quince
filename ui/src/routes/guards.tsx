import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useAuthStatus } from "@/lib/auth";
import { useConfig } from "@/lib/config";
import { useInsecureOrigin } from "@/lib/health";

function Loading() {
  return (
    <div className="flex min-h-screen items-center justify-center text-sm text-muted">Loading…</div>
  );
}

// RequireAuth gates the shell: unauthenticated → login (preserving the intended path),
// first-run → setup. An errored status is treated as needs_login.
export function RequireAuth({ children }: { children: ReactNode }) {
  const { data, isLoading, isError } = useAuthStatus();
  const location = useLocation();
  if (isLoading) return <Loading />;
  const state = isError ? "needs_login" : data?.state;
  if (state === "needs_setup") return <Navigate to="/setup" replace />;
  if (state !== "authenticated") {
    return <Navigate to={`/login?next=${encodeURIComponent(location.pathname)}`} replace />;
  }
  return <>{children}</>;
}

// SetupGate holds `/setup`, and since quince#908 it also decides that a first run over a connection
// which cannot carry a session cookie belongs somewhere else entirely.
//
// THE FORM BEHIND THIS GATE CANNOT SUCCEED WHEN `insecure_origin` IS TRUE. `refuseInsecureOrigin`
// runs BEFORE the credential is examined — before `SetPassword` — so a fresh install reached over
// plain http at a LAN address answers 426 to every password the user types. What stood here was a
// working form guaranteed to fail, whose only exits were editing YAML on the box or knowing about
// `localhost`.
//
// FIRST RUN ONLY, AND THE BOUND IS THE SAFETY ARGUMENT rather than a scoping convenience
// (quince#908 §3). In the pre-credential window `POST /api/auth/setup` is already authExempt and
// one-shot, so anyone who reaches the port can claim the install outright — routing them to a page
// about transport grants strictly less than what is already on offer. Once a credential exists that
// reasoning stops holding, which is why this gate stops at `needs_setup`: the returning visitor is
// `LoginGate`'s to route, on the narrower argument recorded there (quince#1069).
//
// SetupGate IS THE SINGLE FUNNEL, which is why this lives here rather than in three places:
// `RequireAuth` and `LoginGate` both send `needs_setup` to `/setup`, so every route into first run
// passes through this component.
//
// `replace`, NEVER A PUSH. A pushed entry makes Back return to `/setup`, which redirects forward
// again — a two-entry trap with no exit. `/onboarding/https` is top-level and behind no gate, so
// the redirect itself cannot bounce.
export function SetupGate({ children }: { children: ReactNode }) {
  const { data, isLoading } = useAuthStatus();
  const insecureOrigin = useInsecureOrigin();
  if (isLoading) return <Loading />;
  if (data?.state !== "needs_setup") return <Navigate to="/" replace />;
  // AFTER the state check, deliberately. This is a statement about FIRST RUN; reading it earlier
  // would let the transport decide a route while the auth state was still unknown.
  if (insecureOrigin) return <Navigate to="/onboarding/https" replace />;
  return <>{children}</>;
}

// LoginGate holds `/login`, and since quince#1069 it also declines to render a form that cannot
// succeed — the returning-user half of what `SetupGate` already does for first run.
//
// THE ARGUMENT IS NOT "IT WOULD FAIL", IT IS *WHERE THE PASSWORD GOES* — Operator, 2026-08-16.
// `refuseInsecureOrigin` answers 426 BEFORE the credential is examined, but the browser has already
// put it on the wire in clear by then. So a reader on plain http types their admin password, hands
// it to the network, and only then learns they cannot sign in at this address. Redirecting means
// that keystroke is never sent.
//
// IT EXPOSES NO CONTROL, so quince#908 §3 is untouched. The plain-HTTP confirm on that page is
// `firstRun`-only; a `needs_login` visitor gets the instructional page, which is the version §2 says
// they should get — and whose copy is already addressed to them (*"a returning user cannot sign in
// from elsewhere and can still reach quince on localhost"*).
//
// AND LOCALHOST STILL GETS THE FORM, which is the recovery path and has to stay open:
// `insecure_origin` is false on loopback, so an admin at the machine is never sent away from it.
//
// AFTER the state checks, for `SetupGate`'s reason — a transport fact must not decide a route while
// the auth state is unknown. `authenticated` is checked first on purpose: a signed-in reader on
// plain http keeps working (quince#1080), and bouncing them out of the app would be a second wrong
// answer to that question rather than a fix for it.
export function LoginGate({ children }: { children: ReactNode }) {
  const { data, isLoading } = useAuthStatus();
  const insecureOrigin = useInsecureOrigin();
  if (isLoading) return <Loading />;
  if (data?.state === "needs_setup") return <Navigate to="/setup" replace />;
  if (data?.state === "authenticated") return <Navigate to="/" replace />;
  if (insecureOrigin) return <Navigate to="/onboarding/https" replace />;
  return <>{children}</>;
}

// RequireStorage sends an authenticated user with NO declared storage to the first-run storage step
// (qn.6e). The daemon is refusing every API outside setup in that state, so the app shell would
// render a Home whose every query 503s.
//
// DERIVED FROM THE CONFIG, NOT FROM A FLAG — the same shape the daemon's own guard uses. `storage`
// being empty IS the state, so there is nothing to persist, nothing to reset, and no second source
// of truth to go stale. The moment a storage is added the config refetches and this stops matching.
//
// A FAILED OR LOADING CONFIG FALLS THROUGH rather than redirecting. Sending a user to first-run
// because a request failed would be the worst possible false positive: it tells someone whose
// quince is fully configured that it is not. The daemon is the authority — it refuses what it
// refuses — and this gate only decides where to point someone, so it fails toward the ordinary app.
//
// `null` AND `[]` BOTH MEAN NO STORAGE, and this gate shipped treating only `[]` as the state —
// so a genuine first run walked straight past it onto a Home with no Storage section at all
// (Operator-reported from a fresh stand).
//
// `Config.storage` is `*[]StorageEntry` server-side, a POINTER on purpose so that an ABSENT key is
// distinguishable from an empty list (`schema.go`). A fresh install has no `storage:` key at all,
// so it serialises as `null`; a list someone emptied by hand serialises as `[]`. That distinction
// is real and worth keeping — but it is NOT the distinction this gate cares about. Both are
// "quince has nowhere to put backups", which is why the daemon's own predicate is
// `scfg == nil || len(*scfg) == 0`.
//
// TWO IMPLEMENTATIONS OF ONE PREDICATE, AND THEY DISAGREED. That is the hazard `storage.WantZFS`
// is commented against, arriving here instead: the server decides whether to REFUSE, this decides
// where to POINT someone, and a first run fell in the gap between them.
export function RequireStorage({ children }: { children: ReactNode }) {
  const { data, isLoading, isError } = useConfig();
  if (isLoading || isError) return <>{children}</>;
  if (data === undefined) return <>{children}</>;
  const storages = data.config.storage;
  if (storages === null || storages.length === 0) {
    return <Navigate to="/onboarding/storage" replace />;
  }
  return <>{children}</>;
}
