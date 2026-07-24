import { useEffect } from "react";
import { Outlet } from "react-router-dom";
import { Sidebar } from "@/components/Sidebar";
import { close, connect } from "@/ws/client";

// The authed shell. The WebSocket bridge lives only here — it connects on mount (once
// authenticated) and closes on unmount (logout / auth loss).
export function AppLayout() {
  useEffect(() => {
    connect();
    return () => close();
  }, []);

  // Two scroll models, one shell:
  //  - PHONE (base): the shell fills the viewport exactly (h-full = the 100dvh chain) and is
  //    overflow-hidden, so the ONLY scroll region is <main> — the top nav bar stays put and there is
  //    no nested/toolbar-driven scroll (the qn.6a mobile fix the soak confirmed as "perfect").
  //  - DESKTOP (sm): the DOCUMENT scrolls naturally (native macOS elastic bounce) with the sidebar
  //    made sticky, so there is no forced-height seam and the always-bounce feel is preserved.
  // min-w-0 lets wide children (logs, tables) scroll inside themselves, not the page.
  return (
    <div className="flex h-full flex-col overflow-hidden bg-bg text-fg pt-[env(safe-area-inset-top)] sm:h-auto sm:min-h-screen sm:flex-row sm:overflow-visible sm:pt-0">
      <Sidebar />
      <main className="min-w-0 flex-1 overflow-y-auto overscroll-y-contain p-4 pb-[max(1rem,env(safe-area-inset-bottom))] sm:overflow-visible sm:p-8">
        <Outlet />
      </main>
    </div>
  );
}
