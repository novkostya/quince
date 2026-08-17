import { create } from "zustand";

export type ConnStatus = "connecting" | "online" | "reconnecting" | "offline";

interface ConnectionState {
  status: ConnStatus;
  serverVersion: string | null;
  // How far the VIEWER'S clock is from the server's, in ms (server − browser).
  //
  // Every duration on screen is a server timestamp subtracted from a browser clock, so a viewer
  // whose clock is wrong sees wrong durations everywhere. Measured on a real phone, 2026-08-17:
  // "Set Automatically" was off, the device had drifted 26 s ahead, and a backup that had just
  // started showed "26s" the instant it appeared. The server is not a safe reference either —
  // this project's own staging box runs with ntpd stopped.
  //
  // Zero until the first frame arrives, which is the honest default: no correction rather than a
  // guessed one.
  serverOffsetMs: number;
  setStatus: (status: ConnStatus) => void;
  setServerVersion: (v: string) => void;
  setServerOffset: (ms: number) => void;
}

export const useConnectionStore = create<ConnectionState>((set) => ({
  status: "connecting",
  serverVersion: null,
  serverOffsetMs: 0,
  setStatus: (status) => set({ status }),
  setServerVersion: (serverVersion) => set({ serverVersion }),
  // Ignore sub-2s differences. Network delay and the 1 s resolution of these timestamps make small
  // values noise, and writing the store on every frame would re-render every live label for it.
  setServerOffset: (ms) =>
    set((s) => (Math.abs(ms - s.serverOffsetMs) < 2000 ? s : { serverOffsetMs: ms })),
}));
