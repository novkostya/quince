import { useEffect, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";

// A BACK LINK THAT IS A REAL HISTORY TRAVERSAL WHEN IT HONESTLY CAN BE, AND AN ORDINARY LINK
// OTHERWISE — quince#838, Operator-reported: "scroll position is not preserved when you tap on back
// button, e.g. `< Home`".
//
// WHY A PLAIN `<Link>` CANNOT PRESERVE IT, and it is not a bug in the scroll work. A link PUSHES a
// new history entry, and a new entry has no saved offset — there is nothing to restore, so the
// correct behaviour for a push is exactly what `useScrollReset` does: start at the top. The swipe
// gesture restores because it is a TRAVERSAL, and only a traversal can be. So the fix is not to
// remember more; it is for this control to be the same act the gesture is.
//
// WHEN IT WOULD LIE, WHICH IS WHY IT IS CONDITIONAL. `history.go(-1)` returns to whatever the
// previous entry is, and this control names a specific destination. Reached as
// Home → device → storage, a `< Home` on the storage page that went back would land on the DEVICE
// page while saying Home. So the traversal is taken only when the previous entry IS this
// destination; otherwise the `<Link>` pushes as before, and the label stays true. Landing at the top
// of a page you have not seen before is not a defect.
//
// THE SECOND GUARD IS THE ENTRY INDEX. `previous` is this module's own memory and is populated by
// navigations WITHIN the app, but a hard reload wipes it while the browser's history survives — and
// a session that began on a deep link has no entry to go back to at all. React Router's history
// writes an `idx` into `history.state`; `idx > 0` is what says a predecessor exists in this session.
// Without it, `navigate(-1)` on a freshly loaded tab leaves the app entirely.
//
// MODULE STATE RATHER THAN A CONTEXT, deliberately: there is exactly one history per document, so a
// provider would add a tree to describe a singleton. `useTrackNavigation` is called ONCE, in
// `AppLayout`, which every route carrying a back link renders inside.
let previousPathname: string | null = null;
let currentPathname: string | null = null;

export function useTrackNavigation(): void {
  const { pathname } = useLocation();
  useEffect(() => {
    // Guarded on a real change: a re-render at the same path must not shift `previous` onto itself
    // and make every back link think it can traverse.
    if (pathname === currentPathname) return;
    previousPathname = currentPathname;
    currentPathname = pathname;
  }, [pathname]);
}

// FOR TESTS. Module state outlives a render, so a suite that navigates in one case would otherwise
// seed the next one's idea of where it came from.
export function clearNavigationHistory(): void {
  previousPathname = null;
  currentPathname = null;
}

function canTraverseTo(to: string): boolean {
  if (previousPathname !== to) return false;
  const idx = (window.history.state as { idx?: number } | null)?.idx;
  return typeof idx === "number" && idx > 0;
}

export function BackLink({
  to,
  className,
  children,
}: {
  to: string;
  className?: string;
  children: ReactNode;
}) {
  const navigate = useNavigate();
  return (
    <Link
      to={to}
      className={className}
      onClick={(event) => {
        // EVERY MODIFIER STILL MEANS WHAT IT MEANS. Cmd/ctrl-click opens a new tab, middle-click
        // too, and shift opens a window — none of those are "go back", and intercepting them would
        // break behaviour the user brought with them from every other link on the web. `<Link>`
        // makes the same checks for the same reason.
        if (event.defaultPrevented || event.button !== 0) return;
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        if (!canTraverseTo(to)) return;
        event.preventDefault();
        navigate(-1);
      }}
    >
      {children}
    </Link>
  );
}
