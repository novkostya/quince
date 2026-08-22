import { describe, it, expect } from "vitest";
import type { ReactElement } from "react";
import type { RouteObject } from "react-router-dom";

import { router } from "./router";
import { RequireAdmin, ScopedHome } from "./guards";

// qn.13 slice 8d / D8 — THE GUARD IS ATTACHED, not merely correct (quince#1467 review).
//
// `scopedShell.test.tsx` proves `RequireAdmin` redirects a scoped holder. It proves nothing about
// whether it is ON the settings routes, because it mounts a stand-in `<Routes>` and no test in this
// repository imports the real `router`. Measured by the architect: dropping the guard from
// `settings/auth` alone leaves the WHOLE suite green — 95 files, 920 tests.
//
// THE TWO MOST FORGETTABLE ENTRIES ARE THE ONES THAT MATTER. `settings/auth` and
// `settings/notifications` are separate route objects and neither is reachable from the nav, so
// URL-only access is their only access — which is exactly the case D8's *hidden, not merely
// unlinked* exists for.
//
// EXHAUSTIVE RATHER THAN A LIST OF THREE, so a fourth settings route added next month is covered by
// nobody remembering. This is the shell's counterpart to `assertExemptRoutesEnforceTheirOwnScope`
// on the server (quince#1441), which this slice's own comments lean on — the server half has an
// enumerated guard and until now the shell half had none.

/** paths walks the route tree, returning every leaf's full path joined by "/". */
function paths(routes: RouteObject[], prefix = ""): { path: string; route: RouteObject }[] {
  const out: { path: string; route: RouteObject }[] = [];
  for (const r of routes) {
    const here = r.path ? [prefix, r.path].filter(Boolean).join("/") : prefix;
    if (r.path) out.push({ path: here, route: r });
    if (r.children) out.push(...paths(r.children, here));
  }
  return out;
}

/** wrappedBy reports whether a route's element is the given component. */
function wrappedBy(route: RouteObject, component: unknown): boolean {
  const el = route.element as ReactElement | undefined;
  return Boolean(el && typeof el === "object" && "type" in el && el.type === component);
}

describe("every settings route is behind RequireAdmin", () => {
  const settings = paths(router.routes).filter(({ path }) => path.split("/")[0] === "settings");

  // THE VACUITY CHECK, copied from `insecure_transport_scope_test.go:30` and for the same reason: a
  // walk that matches zero routes passes against anything, and this rung has been bitten by a
  // vacuous green three times already (quince#1452, quince#1458, quince#1465).
  it("finds settings routes at all — the control", () => {
    expect(settings.length).toBeGreaterThanOrEqual(3);
  });

  it.each(
    // Named at collection time so a failure says WHICH route, and so adding one to the router makes
    // a new case appear here rather than being silently covered by a loop.
    paths(router.routes)
      .filter(({ path }) => path.split("/")[0] === "settings")
      .map(({ path, route }) => [path, route] as const),
  )("%s is wrapped", (_path, route) => {
    expect(wrappedBy(route, RequireAdmin)).toBe(true);
  });
});

// AND `ScopedHome`, WHICH THE WALK ABOVE CANNOT SEE — quince#1467 review, second round.
//
// The index route has no `path`, so `paths()` skips it by construction (`if (r.path)` is what pushes
// an entry) and the `settings` prefix would exclude it regardless. Measured by the architect:
// detaching `ScopedHome` from the index route leaves ALL 924 tests green.
//
// IT IS THE MORE IMPORTANT OF THE TWO ATTACHMENTS. `RequireAdmin` decides what a scoped holder is
// OFFERED, and D8 is explicit that the server refuses those routes anyway. `ScopedHome` is the one
// that fixes the FALSE STATEMENT — the screen that told a household member *"No devices connected"*
// about their own device, which is quince#1443's first defect and this slice's headline. Detach it
// and the bug is back in full, with the suite green.
//
// SCOPE: only the two components THIS slice introduces. `LoginGate`, `RequireAuth`, `RequireStorage`
// and `SetupGate` are equally unasserted and equally invisible here, and covering them is an issue
// against the router rather than a condition on this slice (architect, same review).

/** indexRoutes returns every `index: true` route in the tree, with the path of its parent. */
function indexRoutes(routes: RouteObject[], prefix = ""): { under: string; route: RouteObject }[] {
  const out: { under: string; route: RouteObject }[] = [];
  for (const r of routes) {
    const here = r.path ? [prefix, r.path].filter(Boolean).join("/") : prefix;
    if (r.index) out.push({ under: here || "/", route: r });
    if (r.children) out.push(...indexRoutes(r.children, here));
  }
  return out;
}

describe("the shell's index route is behind ScopedHome", () => {
  const found = indexRoutes(router.routes);

  // THE SAME VACUITY DISCIPLINE. A walk that finds no index route passes for a router that lost it,
  // which is the failure this whole file exists to catch one level down.
  it("finds the index route at all — the control", () => {
    expect(found.length).toBe(1);
  });

  it("wraps it, so `/` routes to whoever is asking", () => {
    expect(found.map(({ route }) => wrappedBy(route, ScopedHome))).toEqual([true]);
  });
});
