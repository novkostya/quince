import type { AuthStatus, WSEnvelope } from "@/lib/types";
import { refreshAll } from "@/lib/refresh";
import { useConnectionStore } from "@/stores/connection";
import { queryClient } from "@/lib/queryClient";
import { authStatusKey } from "@/lib/auth";
import { api, notifyUnauthorized } from "@/lib/api";
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
    if (!stopped) void reportIfSessionLost();
    scheduleReconnect();
  };
  ws.onerror = () => ws.close();
}

// A CLOSED SOCKET IS NOT EVIDENCE OF A NETWORK FAULT, and the badge said it was. The server rejects
// an unauthenticated upgrade with a 401 BEFORE upgrading (core/internal/ws/handler.go), and the
// browser WebSocket API does not expose the handshake status to script: all you get is onerror then
// onclose with code 1006, which is exactly what an unreachable daemon looks like. So an idle tab
// whose session had expired sat on "reconnecting…" — blaming the network for a logged-out session,
// serving stale data that looked current — and nothing redirected, because the API client's 401
// handling can only see a request nobody was making (quince#374).
//
// SO ASK THE ONE ENDPOINT THAT CAN ANSWER instead of inferring from a close code. /api/auth/status
// is auth-exempt and answers 200, which separates the two cases outright:
//
//   answers, not authenticated  -> the session is gone: report it, and the route guard redirects
//   answers, authenticated      -> only the socket is down: keep backing off, unchanged
//   does not answer             -> the SERVER is down: keep backing off, and DO NOT redirect
//
// That third line is why this ASKS rather than simply invalidating the auth query on failure.
// `RequireAuth` treats an errored status as `needs_login` (guards.tsx), so a blind invalidation
// would throw the user to the login screen every time the daemon restarted — trading this defect
// for a worse one. The retry loop itself is not the bug and is left alone: an unreachable server
// SHOULD back off and retry forever.
async function reportIfSessionLost(): Promise<void> {
  let status: AuthStatus;
  try {
    status = await api.get<AuthStatus>("/api/auth/status");
  } catch {
    return; // no answer: an unreachable server proves nothing about the session
  }
  if (status.state !== "authenticated") notifyUnauthorized();
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

function socketOpen(): boolean {
  return socket !== null && socket.readyState === WebSocket.OPEN;
}

// resumeReconnect force-reconnects immediately when the app comes back to the foreground or the
// network returns. iOS suspends a backgrounded PWA and tears down its socket WITHOUT always firing
// onclose, leaving a dead-but-non-null socket that open() would skip (`if (socket) return`) — so the
// UI sits on "reconnecting…" until the PWA is restarted. Here we drop any stale socket, reset the
// backoff (a resumed timer could be up to 30 s out), and reconnect now. We also revalidate auth: a
// long suspension can idle-expire the session, and re-checking /api/auth/status lets the route guard
// send an expired user to login instead of spinning forever (qn.6a soak fix).
function resumeReconnect(): void {
  if (stopped || socketOpen()) return;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  attempt = 0;
  if (socket) {
    socket.onclose = null;
    socket.onerror = null;
    socket.onmessage = null;
    try {
      socket.close();
    } catch {
      /* already dead */
    }
    socket = null;
  }
  void queryClient.invalidateQueries({ queryKey: authStatusKey });
  useConnectionStore.getState().setStatus("connecting");
  open();
}

function onVisible(): void {
  if (typeof document !== "undefined" && document.visibilityState === "visible") resumeReconnect();
}

function addResumeListeners(): void {
  if (typeof window === "undefined") return;
  document.addEventListener("visibilitychange", onVisible);
  window.addEventListener("online", resumeReconnect);
  window.addEventListener("pageshow", onVisible);
  window.addEventListener("focus", onVisible);
}

function removeResumeListeners(): void {
  if (typeof window === "undefined") return;
  document.removeEventListener("visibilitychange", onVisible);
  window.removeEventListener("online", resumeReconnect);
  window.removeEventListener("pageshow", onVisible);
  window.removeEventListener("focus", onVisible);
}

// connect opens the socket (called from the authed shell only).
export function connect(): void {
  stopped = false;
  attempt = 0;
  addResumeListeners();
  useConnectionStore.getState().setStatus("connecting");
  open();
}

// close tears the socket down and stops reconnecting (called on logout / shell unmount).
export function close(): void {
  stopped = true;
  removeResumeListeners();
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
