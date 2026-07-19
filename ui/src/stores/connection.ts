import { create } from "zustand";

export type ConnStatus = "connecting" | "online" | "reconnecting" | "offline";

interface ConnectionState {
  status: ConnStatus;
  serverVersion: string | null;
  setStatus: (status: ConnStatus) => void;
  setServerVersion: (v: string) => void;
}

export const useConnectionStore = create<ConnectionState>((set) => ({
  status: "connecting",
  serverVersion: null,
  setStatus: (status) => set({ status }),
  setServerVersion: (serverVersion) => set({ serverVersion }),
}));
