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

  // Column on phones (nav as a top bar), row on desktop (nav as the left sidebar). The shell fills
  // the viewport exactly (h-full = the 100dvh chain) and is overflow-hidden, so the ONLY scroll
  // region is <main> — the nav bar stays put and the page never nests a second scroll (qn.6a mobile
  // fix). min-w-0 lets wide children (logs, tables) scroll inside themselves, not the page.
  return (
    <div className="flex h-full flex-col overflow-hidden bg-bg text-fg sm:flex-row">
      <Sidebar />
      <main className="min-w-0 flex-1 overflow-y-auto p-4 sm:p-8">
        <Outlet />
      </main>
    </div>
  );
}
