import { create } from "zustand";
import type { Version } from "@/lib/types";

interface VersionsState {
  byId: Record<string, Version>;
  order: string[]; // newest first
  upsert: (v: Version) => void;
  remove: (id: string) => void;
  replaceAll: (versions: Version[]) => void;
}

export const useVersionsStore = create<VersionsState>((set) => ({
  byId: {},
  order: [],
  // upsert mirrors the server's single-is_latest-per-device invariant (gate-11 finding #7, (cj)):
  // when a newly committed version arrives with is_latest=true, the previously-latest version of the
  // SAME device is demoted in the store immediately — otherwise two "latest" badges show until the
  // next full reload. Pure client-side; the server already enforces this (PromoteLatest).
  upsert: (v) =>
    set((s) => {
      const byId = { ...s.byId };
      if (v.is_latest) {
        for (const id of s.order) {
          const prev = byId[id];
          if (prev && prev.udid === v.udid && prev.is_latest && prev.id !== v.id) {
            byId[id] = { ...prev, is_latest: false };
          }
        }
      }
      byId[v.id] = v;
      return {
        byId,
        order: s.order.includes(v.id) ? s.order : [v.id, ...s.order],
      };
    }),
  remove: (id) =>
    set((s) => {
      const byId = { ...s.byId };
      delete byId[id];
      return { byId, order: s.order.filter((x) => x !== id) };
    }),
  replaceAll: (versions) =>
    set(() => ({
      byId: Object.fromEntries(versions.map((v) => [v.id, v])),
      order: versions.map((v) => v.id),
    })),
}));
