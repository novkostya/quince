import { useEffect, useState } from "react";
import { fetchHealth, type Health } from "./health";

export type ConnState = "connecting" | "online" | "offline";

export interface HealthState {
  conn: ConnState;
  health: Health | null;
}

/** Poll /api/health so the shell can show version + connection status. A real WS bridge
 * replaces polling in qn.1+; qn.0 only needs a live version and an honest online dot. */
export function useHealth(): HealthState {
  const [state, setState] = useState<HealthState>({ conn: "connecting", health: null });

  useEffect(() => {
    let alive = true;
    const controller = new AbortController();

    async function tick(): Promise<void> {
      try {
        const health = await fetchHealth(controller.signal);
        if (alive) setState({ conn: "online", health });
      } catch {
        if (alive) setState((s) => ({ conn: "offline", health: s.health }));
      }
    }

    void tick();
    const id = setInterval(() => void tick(), 5000);
    return () => {
      alive = false;
      controller.abort();
      clearInterval(id);
    };
  }, []);

  return state;
}
