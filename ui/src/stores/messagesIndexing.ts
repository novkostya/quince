import { create } from "zustand";

// The live count from `messages.indexing` (qn.10 D3), keyed by session.
//
// KEYED BY SESSION BECAUSE THE EVENT IS. One quince can hold several unlocked sessions at once, and
// a count from another one rendered against this thread would be a number about somebody else's
// backup. The key is what makes that impossible rather than unlikely.
//
// A BARE NUMBER, NOT A PROGRESS OBJECT, and deliberately: there is no total to carry, so a shape
// with `{done, total}` would invite a percentage that cannot be computed honestly.
//
// IT IS READ ONLY WHILE A REQUEST IS IN FLIGHT, so a stale entry is harmless — but it is dropped on
// lock anyway (below), because *how many messages this person has* is a fact about the backup and
// story 6 says nothing read from one survives a lock.
interface MessagesIndexingState {
  bySession: Record<string, number>;
  note: (sessionID: string, messages: number) => void;
  drop: (sessionID: string) => void;
}

export const useMessagesIndexingStore = create<MessagesIndexingState>((set) => ({
  bySession: {},
  note: (sessionID, messages) =>
    set((s) => ({ bySession: { ...s.bySession, [sessionID]: messages } })),
  drop: (sessionID) =>
    set((s) => {
      if (!(sessionID in s.bySession)) return s;
      const next = { ...s.bySession };
      delete next[sessionID];
      return { bySession: next };
    }),
}));
