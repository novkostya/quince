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
  upsert: (v) =>
    set((s) => ({
      byId: { ...s.byId, [v.id]: v },
      order: s.order.includes(v.id) ? s.order : [v.id, ...s.order],
    })),
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
