import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useAuthStatus } from "@/lib/auth";

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
