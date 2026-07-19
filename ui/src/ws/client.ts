import type { WSEnvelope } from "@/lib/types";
import { refreshAll } from "@/lib/refresh";
import { useConnectionStore } from "@/stores/connection";
import { dispatch } from "./dispatch";

const BASE_DELAY = 500;
const MAX_DELAY = 30_000;

// backoffDelay is the pure reconnect schedule: exponential with ±50% jitter, capped.
// Exported for unit testing.
export function backoffDelay(attempt: number, rand: () => number = Math.random): number {
  const base = Math.min(BASE_DELAY * 2 ** attempt, MAX_DELAY);
  return Math.round(base * (0.5 + rand()));
}

function wsURL(): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/api/ws`;
}

let socket: WebSocket | null = null;
let attempt = 0;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let stopped = true;

function open(): void {
  if (socket) return;
  const ws = new WebSocket(wsURL());
  socket = ws;

  ws.onmessage = (ev) => {
    let env: WSEnvelope;
    try {
      env = JSON.parse(ev.data as string) as WSEnvelope;
    } catch {
      return; // ignore malformed frame
    }
    if (env.type === "hello") {
      attempt = 0;
      useConnectionStore.getState().setStatus("online");
      dispatch(env);
      void refreshAll();
      return;
    }
    dispatch(env);
  };

  ws.onclose = () => {
    socket = null;
    scheduleReconnect();
  };
  ws.onerror = () => ws.close();
}

function scheduleReconnect(): void {
  if (stopped) {
    useConnectionStore.getState().setStatus("offline");
    return;
  }
  useConnectionStore.getState().setStatus("reconnecting");
  const delay = backoffDelay(attempt);
  attempt += 1;
  reconnectTimer = setTimeout(open, delay);
}

// connect opens the socket (called from the authed shell only).
export function connect(): void {
  stopped = false;
  attempt = 0;
  useConnectionStore.getState().setStatus("connecting");
  open();
}

// close tears the socket down and stops reconnecting (called on logout / shell unmount).
export function close(): void {
  stopped = true;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (socket) {
    socket.onclose = null;
    socket.close();
    socket = null;
  }
  useConnectionStore.getState().setStatus("offline");
}

// A dev/demo-only deterministic disconnect hook for the Playwright reconnect story.
declare global {
  interface Window {
    __quince?: { dropWs: () => void };
  }
}
if (typeof window !== "undefined") {
  window.__quince = { dropWs: () => socket?.close() };
}
