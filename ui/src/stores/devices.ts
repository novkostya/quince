import { create } from "zustand";
import type { Device } from "@/lib/types";

interface DevicesState {
  byUdid: Record<string, Device>;
  order: string[];
  upsert: (d: Device) => void;
  removeTransport: (udid: string, transport: string) => void;
  replaceAll: (devices: Device[]) => void;
}

export const useDevicesStore = create<DevicesState>((set) => ({
  byUdid: {},
  order: [],
  upsert: (d) =>
    set((s) => ({
      byUdid: { ...s.byUdid, [d.udid]: d },
      order: s.order.includes(d.udid) ? s.order : [...s.order, d.udid],
    })),
  // A per-transport detach: drop that edge; if the device has no transports left, it
  // vanishes from the list (matches the demo's presence toggle).
  removeTransport: (udid, transport) =>
    set((s) => {
      const dev = s.byUdid[udid];
      if (!dev) return s;
      const transports = { ...dev.transports };
      delete transports[transport as keyof typeof transports];
      if (!transports.usb && !transports.wifi) {
        const byUdid = { ...s.byUdid };
        delete byUdid[udid];
        return { byUdid, order: s.order.filter((u) => u !== udid) };
      }
      return { byUdid: { ...s.byUdid, [udid]: { ...dev, transports } } };
    }),
  replaceAll: (devices) =>
    set(() => ({
      byUdid: Object.fromEntries(devices.map((d) => [d.udid, d])),
      order: devices.map((d) => d.udid),
    })),
}));
