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

  return (
    <div className="flex h-full min-h-screen bg-bg text-fg">
      <Sidebar />
      <main className="flex-1 overflow-auto p-8">
        <Outlet />
      </main>
    </div>
  );
}
