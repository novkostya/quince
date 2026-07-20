import { create } from "zustand";
import type { Op } from "@/lib/types";

// The ops store holds pair/encryption Ops keyed by id, fed by op.updated WS events (contracts
// §3). A running dialog watches its op by id to narrate running → waiting_for_user →
// succeeded/failed. Ops are transient UI state; there is no REST hydration (GET /api/ops/{id}
// is the poll fallback the dialog can use on a cold refresh).
interface OpsState {
  byId: Record<string, Op>;
  upsert: (op: Op) => void;
}

export const useOpsStore = create<OpsState>((set) => ({
  byId: {},
  upsert: (op) => set((s) => ({ byId: { ...s.byId, [op.id]: op } })),
}));
