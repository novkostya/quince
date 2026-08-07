import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useAuthStatus } from "@/lib/auth";
import { useConfig } from "@/lib/config";

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

export function SetupGate({ children }: { children: ReactNode }) {
  const { data, isLoading } = useAuthStatus();
  if (isLoading) return <Loading />;
  if (data?.state !== "needs_setup") return <Navigate to="/" replace />;
  return <>{children}</>;
}

export function LoginGate({ children }: { children: ReactNode }) {
  const { data, isLoading } = useAuthStatus();
  if (isLoading) return <Loading />;
  if (data?.state === "needs_setup") return <Navigate to="/setup" replace />;
  if (data?.state === "authenticated") return <Navigate to="/" replace />;
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
