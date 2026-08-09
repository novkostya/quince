import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router-dom";
import { queryClient } from "@/lib/queryClient";
import { router } from "@/routes/router";
import { initTheme } from "@/lib/theme";
import { initViewportDebug } from "@/lib/viewportDebug";
import { setUnauthorizedHandler } from "@/lib/api";
import { authStatusKey } from "@/lib/auth";
import "./index.css";

// System-follow theme at boot (ui.design.md principle 6); the Settings editor can override
// via config.ui.theme once loaded.
initTheme("system");

// TEMPORARY DIAGNOSTIC for quince#762, inert unless the URL carries `?vvdebug`. To be removed with
// the issue, in its own commit. See the module for why an instrument was reached for at all.
initViewportDebug();

// A LOST SESSION MUST REACH THE LOGIN SCREEN. `RequireAuth` already redirects — it simply never got
// the news. `useAuthStatus` refetches on mount or invalidation only, and the guard stays mounted for
// the whole app session, so a mid-session expiry left a cached `authenticated` standing indefinitely
// while every action failed. The user saw "authentication required" on each click, stale data that
// looked current, and a connection badge blaming the network — three signals all pointing away from
// "your session ended" — with a manual reload the only way out (quince#356).
//
// INVALIDATE rather than writing `needs_login` into the cache: `GET /api/auth/status` is exempt from
// the auth guard and answers 200, so the refetch returns the SERVER's state — including
// `needs_setup`, which a guessed value would get wrong. `RequireAuth` preserves the current path via
// `?next=`, so the redirect is recoverable rather than a bounce to the dashboard.
setUnauthorizedHandler(() => {
  void queryClient.invalidateQueries({ queryKey: authStatusKey });
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
