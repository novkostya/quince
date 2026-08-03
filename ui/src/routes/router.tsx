import { createBrowserRouter, Navigate } from "react-router-dom";
import { AppLayout } from "./AppLayout";
import { LoginGate, RequireAuth, SetupGate } from "./guards";
import { SetupPasswordPage } from "@/pages/SetupPasswordPage";
import { OnboardingHTTPSPage } from "@/pages/OnboardingHTTPSPage";
import { LoginPage } from "@/pages/LoginPage";
import { DashboardPage } from "@/pages/DashboardPage";
import { DeviceDetailsPage } from "@/pages/DeviceDetailsPage";
import { SettingsPage } from "@/pages/SettingsPage";
import { StorageDetailsPage } from "@/pages/StorageDetailsPage";

export const router = createBrowserRouter([
  // OUTSIDE EVERY GUARD, and that is the decision rather than an oversight (qn.6f rung-ruled 6).
  //
  // Not `RequireAuth`: over plain http to a LAN address the browser discards the session cookie,
  // so the page explaining exactly that must not sit behind login — the deadlock the endpoint's
  // pre-auth ruling removed (quince#501), and the page is the half a user meets.
  //
  // Not `SetupGate` either, which is the less obvious half. Since quince#530, `POST
  // /api/auth/setup` answers 426 BEFORE storing the password, so a fresh install on a plain-http
  // LAN address cannot complete setup at all — this check is a PREREQUISITE of first-run, not a
  // successor to it, and must be reachable with no password in existence.
  //
  // TOP LEVEL, not a child: the catch-all below `Navigate`s to `/`, which is itself behind
  // `RequireAuth`, so a route nested anywhere would bounce an unauthenticated visitor to /login.
  { path: "/onboarding/https", element: <OnboardingHTTPSPage /> },
  {
    path: "/setup",
    element: (
      <SetupGate>
        <SetupPasswordPage />
      </SetupGate>
    ),
  },
  {
    path: "/login",
    element: (
      <LoginGate>
        <LoginPage />
      </LoginGate>
    ),
  },
  {
    element: (
      <RequireAuth>
        <AppLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/devices" replace /> },
      { path: "devices", element: <DashboardPage /> },
      { path: "devices/:udid", element: <DeviceDetailsPage /> },
      // Routed on NAME, not id: the API addresses a storage by its config name (quince#570), and a
      // name exists for every declared storage where an id does not — one that was never created
      // has none, and that is exactly the storage a user goes looking for.
      { path: "storage/:name", element: <StorageDetailsPage /> },
      { path: "settings", element: <SettingsPage /> },
    ],
  },
  { path: "*", element: <Navigate to="/" replace /> },
]);
