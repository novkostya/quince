import { createBrowserRouter, Navigate } from "react-router-dom";
import { AppLayout } from "./AppLayout";
import { LoginGate, RequireAdmin, RequireAuth, RequireStorage, ScopedHome, SetupGate } from "./guards";
import { SetupPasswordPage } from "@/pages/SetupPasswordPage";
import { OnboardingHTTPSPage } from "@/pages/OnboardingHTTPSPage";
import { OnboardingCertificatePage } from "@/pages/OnboardingCertificatePage";
import { OnboardingCertificateConfirmPage } from "@/pages/OnboardingCertificateConfirmPage";
import { OnboardingStoragePage } from "@/pages/OnboardingStoragePage";
import { LoginPage } from "@/pages/LoginPage";
import { DashboardPage } from "@/pages/DashboardPage";
import { DeviceDetailsPage } from "@/pages/DeviceDetailsPage";
import { SettingsPage } from "@/pages/SettingsPage";
import { SettingsAuthPage } from "@/pages/SettingsAuthPage";
import { NotificationsInstallPage } from "@/pages/NotificationsInstallPage";
import { StorageDetailsPage } from "@/pages/StorageDetailsPage";
import { AddStoragePage } from "@/pages/AddStoragePage";
import { VaultBrowsePage } from "@/pages/VaultBrowsePage";
import { MessagesPage } from "@/pages/MessagesPage";
import { VersionOverviewPage } from "@/pages/VersionOverviewPage";
import { EnrolPage } from "@/pages/EnrolPage";

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
  // THE ENROLMENT LANDING PAGE (qn.13 D4, slice 9d), outside every guard for the login page's
  // reason: whoever scanned the QR has no session, and obtaining one is what this leads to. Its
  // bound is the secret in its own URL — single-use, minutes-long, and cancellable by the admin
  // while the page is open. NOT behind `SetupGate` either: an install reaching this is configured
  // by definition, since only an admin on a device page can issue the code.
  { path: "/enrol", element: <EnrolPage /> },
  // THE CERTIFICATE STEP'S OWN ROUTE (quince#908 §5, slice 4), outside every guard for the identical
  // reason its parent is: it is reached by somebody who cannot log in yet.
  //
  // A ROUTE RATHER THAN AN ACCORDION ON THE CARD, ruled in §4 and not a matter of taste: the flow is
  // multi-step and stateful, and an accordion is application state with no URL — fill three fields,
  // navigate away, come back to an empty collapsed box. A route is state the browser understands.
  { path: "/onboarding/https/certificate", element: <OnboardingCertificatePage /> },
  // THE CONFIRMATION (quince#908 §5, slice 5), opened at a DIFFERENT ORIGIN from every other route
  // in this file: `https://<the certificate name>:<same port>`. Same app, same daemon, same
  // listener — the TLS half rather than the plain one — so it needs nothing beyond being outside
  // every guard, for its parent's reason.
  {
    path: "/onboarding/https/certificate/confirm",
    element: <OnboardingCertificateConfirmPage />,
  },
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
  // `/onboarding/passkey` IS GONE — qn.6m slice 4. It was `qn.6k` story 9's separate step; the
  // passkey offer now lives ON the setup screen, which is quince#841 item 2 and the ruled fix for
  // quince#840 (the offer never rendered — the screen stops existing rather than being debugged).
  //
  // NO REDIRECT LEFT BEHIND, and that is deliberate rather than an omission. The path was only ever
  // reachable by `SetupPasswordPage` navigating to it in the seconds after first-run setup — never
  // linked and never worth bookmarking, and meaningless on an install that now has a password. The
  // catch-all at the foot of this list sends it to `/`, which is the right destination for the only
  // person who could still have it open.
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
      // HOME IS WHOEVER IS ASKING (qn.13 slice 8d, D8). A scoped holder's Home IS their device
      // page; the admin's is the dashboard. `ScopedHome` is the one component that knows which.
      { index: true, element: <ScopedHome><DashboardPage /></ScopedHome> },
      { path: "devices", element: <Navigate to="/" replace /> },
      { path: "devices/:udid", element: <DeviceDetailsPage /> },
      // ADDING A STORAGE IS A DESTINATION, not an interruption (quince#846) — the same conclusion
      // `/onboarding/storage` reached, now applied to the in-app route. It sits INSIDE the shell,
      // unlike the first-run step: there is a Home to come back to and a sidebar that renders.
      //
      // IT SHADOWS A STORAGE NAMED `new`, and that is a real cost rather than a theoretical one.
      // React Router ranks a static segment above `storage/:name`, so the shadow is deterministic
      // — but a config that says `name: new` would make that storage's details page unreachable.
      // Accepted because `name` defaults to the PATH (quince#504), which is absolute, so reaching
      // it takes someone deliberately naming a storage after this route.
      { path: "storage/new", element: <AddStoragePage /> },
      // Routed on NAME, not id: the API addresses a storage by its config name (quince#570), and a
      // name exists for every declared storage where an id does not — one that was never created
      // has none, and that is exactly the storage a user goes looking for.
      { path: "storage/:name", element: <StorageDetailsPage /> },
      // ONE COMMITTED BACKUP, OPENED (qn.8 slice 7). A PAGE rather than a dialog, and this is the
      // fourth time the same call has gone the same way: quince#838 gave scrolling back to the
      // browser, quince#908 §4 replaced an accordion with a route because the flow was multi-step
      // and stateful, and quince#931 made a dialog a place you went. `useDialogRoute`'s own comment
      // names the exit — "can become one the day a dialog is worth its own screen, at which point it
      // is a page" — and a filterable list of a hundred thousand rows behind a password is that day.
      //
      // ROUTED ON THE VERSION, NOT THE SESSION, and the difference is what makes the URL worth
      // having. A session id is minted at unlock and dies at the TTL, so a link carrying one is
      // stale within minutes and cannot be sent to anybody. A version id is the durable thing; the
      // session is re-established on arrival, which is the same act as typing the password again
      // and is what contracts §1 means by "unlock is per-session, always".
      //
      // NOT NESTED UNDER `devices/:udid`, though a version belongs to a device. The version list is
      // rendered on the storage page too (mixed across devices), and a link whose shape depends on
      // which page you clicked from is two links to one thing.

      // qn.9 slice 10 — the version's PRIMARY page: what this backup IS, before it is opened.
      // Sits at the version's own path with the browser one click behind it (D9), which is what
      // the rung means by overview becoming primary. It needs no session, so unlike its sibling
      // below it renders on arrival with no password.
      { path: "versions/:id", element: <VersionOverviewPage /> },
      { path: "versions/:id/browse", element: <VaultBrowsePage /> },
      // Messages sits beside the browser, one click behind the version page (qn.10 D9, slice
      // 7c-2a). Same shape as `browse`: routed on the durable version id, not on a session.
      { path: "versions/:id/messages", element: <MessagesPage /> },
      // SETTINGS IS ADMIN-ONLY AND ITS ROUTES DO NOT RESOLVE for a scoped holder — D8's
      // *hidden, not merely empty*. Hiding the nav item alone leaves the URL working, and a
      // bookmark is how a household member reaches a screen that then errors at them.
      { path: "settings", element: <RequireAdmin><SettingsPage /></RequireAdmin> },
      // The auth surface is its OWN PAGE, linked from Settings — quince#841 ruling A. A child of
      // the authed shell, unlike its onboarding sibling, which is a top-level route: one has a
      // session and one does not, which is the whole of qn.6m D2.
      { path: "settings/auth", element: <RequireAdmin><SettingsAuthPage /></RequireAdmin> },
      // The notifications install step (qn.12, spec D1). INSIDE the authed shell, unlike the
      // `/onboarding/*` routes at the top of this file: nothing here is a prerequisite of logging
      // in, so it needs none of their exemptions and must not acquire one. What it reports is a
      // property of the visitor's own browser, and an unauthenticated caller has no business
      // learning what this install can do.
      { path: "settings/notifications", element: <RequireAdmin><NotificationsInstallPage /></RequireAdmin> },
    ],
  },
  { path: "*", element: <Navigate to="/" replace /> },
]);
