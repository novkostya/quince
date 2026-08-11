import { createBrowserRouter, Navigate } from "react-router-dom";
import { AppLayout } from "./AppLayout";
import { LoginGate, RequireAuth, RequireStorage, SetupGate } from "./guards";
import { SetupPasswordPage } from "@/pages/SetupPasswordPage";
import { OnboardingHTTPSPage } from "@/pages/OnboardingHTTPSPage";
import { OnboardingPasskeyPage } from "@/pages/OnboardingPasskeyPage";
import { OnboardingStoragePage } from "@/pages/OnboardingStoragePage";
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
  // THE FIRST-RUN STORAGE STEP (qn.6e). Behind `RequireAuth` and OUTSIDE the `AppLayout` shell —
  // both halves deliberate.
  //
  // Behind auth, unlike `/onboarding/https`: that step is pre-auth because you cannot log in
  // without https, which is a genuine deadlock. Nothing about declaring a storage is a prerequisite
  // of logging in, so this takes the ordinary guard and the exempt set stays at five.
  //
  // Outside the shell, and NOT under `RequireStorage`, because the shell is exactly what cannot
  // render here: the daemon is refusing every API outside setup, so the sidebar's device list and
  // Home's storage list would both 503. Putting it inside its own gate would also be a redirect
  // loop — this is the page that gate points AT.
  {
    path: "/onboarding/storage",
    element: (
      <RequireAuth>
        <OnboardingStoragePage />
      </RequireAuth>
    ),
  },
  // THE PASSKEY OFFER (qn.6k story 9). Behind `RequireAuth` and outside the shell, like the storage
  // step above — registration needs a session by definition, and the offer arrives immediately
  // after setup, where the shell has nothing to show yet.
  //
  // NOT under `RequireStorage`, and not a gate of its own. It is a step the user passes THROUGH:
  // both buttons navigate to `/`, and the page renders nothing at all where passkeys cannot work,
  // so it can never become somewhere a first run gets stuck.
  {
    path: "/onboarding/passkey",
    element: (
      <RequireAuth>
        <OnboardingPasskeyPage />
      </RequireAuth>
    ),
  },
  {
    element: (
      <RequireAuth>
        {/* RequireStorage sits INSIDE RequireAuth, so an unauthenticated visitor goes to login
            rather than to a first-run step they cannot complete — and the config query it reads
            needs a session anyway. */}
        <RequireStorage>
          <AppLayout />
        </RequireStorage>
      </RequireAuth>
    ),
    children: [
      // HOME IS `/`, and `/devices` still resolves (rung-ruled decision 5, quince#443). The pair is
      // inverted from what it was: `/` used to redirect to `/devices`, and now `/devices` redirects
      // to `/`. Breaking a bookmark to make a rename tidy is a cost with no benefit, and the
      // redirect is one line where a broken link is a support question.
      { index: true, element: <DashboardPage /> },
      { path: "devices", element: <Navigate to="/" replace /> },
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
