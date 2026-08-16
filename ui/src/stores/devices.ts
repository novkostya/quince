import { create } from "zustand";
import type { Device, Pairing } from "@/lib/types";

interface DevicesState {
  byUdid: Record<string, Device>;
  order: string[];
  upsert: (d: Device) => void;
  removeTransport: (udid: string, transport: string) => void;
  // pairing is the SYSTEM capability from the same envelope (qn.6p D7). It lives here rather
  // than on each Device because there is one lockdown directory behind all of them, and
  // copying it per row would be one fact stored N times.
  //
  // Defaults to writable: a fresh store has not asked yet, and starting from `false` would
  // grey out Pair on every first paint before the first refresh lands.
  pairing: Pairing;
  replaceAll: (devices: Device[], pairing?: Pairing) => void;
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
  pairing: { writable: true },
  replaceAll: (devices, pairing) =>
    set(() => ({
      byUdid: Object.fromEntries(devices.map((d) => [d.udid, d])),
      order: devices.map((d) => d.udid),
      // An absent envelope field leaves the last known answer alone rather than inventing
      // one: a partial refresh must not silently re-enable a control it knows nothing about.
      ...(pairing ? { pairing } : {}),
    })),
}));
