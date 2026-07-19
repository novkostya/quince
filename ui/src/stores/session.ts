import { create } from "zustand";

// qn.1 has no vault unlock yet, but the session.locked event must be honored (contracts §3)
// so decrypted views (which arrive in qn.8) drop instantly. For now we record the last lock.
interface SessionState {
  lastLocked: { sessionId: string; reason: string } | null;
  drop: (sessionId: string, reason: string) => void;
}

export const useSessionStore = create<SessionState>((set) => ({
  lastLocked: null,
  drop: (sessionId, reason) => set({ lastLocked: { sessionId, reason } }),
}));
