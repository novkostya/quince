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

  // Column on phones (nav as a top bar), row on desktop (nav as the left sidebar). min-w-0 on the
  // main column lets wide children (logs, tables) scroll inside themselves instead of forcing the
  // whole page to scroll horizontally (qn.6a mobile pass).
  return (
    <div className="flex h-full min-h-screen flex-col bg-bg text-fg sm:flex-row">
      <Sidebar />
      <main className="min-w-0 flex-1 overflow-auto p-4 sm:p-8">
        <Outlet />
      </main>
    </div>
  );
}
