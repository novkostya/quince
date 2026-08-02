import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { useStorages } from "./useStorages";
import type { Storage } from "@/lib/types";

const get = vi.fn();
const post = vi.fn();
vi.mock("@/lib/api", () => ({
  api: { get: (p: string) => get(p), post: (p: string) => post(p) },
}));

function storage(over: Partial<Storage>): Storage {
  return {
    id: "01JB",
    name: "shuttle",
    path: "/mnt/shuttle",
    backend: "unknown",
    default: false,
    reachable: false,
    unreachable_code: "missing_medium",
    unreachable_reason: "the path is readable but carries no quince storage marker",
    will_be_full: null,
    ...over,
  };
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
});

describe("useStorages recheck", () => {
  // THE CLAIM THAT MATTERS, and the reason the hook does not simply splice the 200 {storage}
  // response into its list. `POST /api/storages/{id}/recheck` is device-INDEPENDENT by ruling —
  // RecheckStorage(id) takes no udid — so its will_be_full is ALWAYS null. Splicing it would drop
  // "First backup to shuttle — this transfers everything" at exactly the moment the disk came back
  // and that warning became true. Story 8's claim would vanish on success.
  it("reloads the device-scoped list after a re-check, so will_be_full survives", async () => {
    get
      .mockResolvedValueOnce({ storages: [storage({})] })
      .mockResolvedValueOnce({
        storages: [storage({ reachable: true, backend: "hardlink", will_be_full: true })],
      });
    // The endpoint's own response, faithfully device-independent: will_be_full is null.
    post.mockResolvedValueOnce({ storage: storage({ reachable: true, will_be_full: null }) });

    const { result } = renderHook(() => useStorages("DEV-1"));
    await waitFor(() => expect(result.current.state.status).toBe("loaded"));

    act(() => result.current.recheck("01JB"));

    await waitFor(() => {
      const s = result.current.state;
      expect(s.status).toBe("loaded");
      if (s.status !== "loaded") return;
      expect(s.storages[0].reachable).toBe(true);
      expect(s.storages[0].will_be_full).toBe(true);
    });

    expect(post).toHaveBeenCalledWith("/api/storages/01JB/recheck");
    // Two GETs: the initial load and the reload. If the hook had spliced, there would be one.
    expect(get).toHaveBeenCalledTimes(2);
    expect(get).toHaveBeenLastCalledWith("/api/storages?udid=DEV-1");
  });

  // A failed press is a state the row can render, never a swallowed error and never a blanked
  // list — the last thing the daemon said about the disk is still the best answer available.
  it("records the failure and keeps the list it already had", async () => {
    get.mockResolvedValueOnce({ storages: [storage({})] });
    post.mockRejectedValueOnce(new Error("no_such_storage"));

    const { result } = renderHook(() => useStorages("DEV-1"));
    await waitFor(() => expect(result.current.state.status).toBe("loaded"));

    act(() => result.current.recheck("01JB"));

    await waitFor(() => expect(result.current.rechecking["01JB"]).toBe("failed"));
    const s = result.current.state;
    expect(s.status).toBe("loaded");
    if (s.status !== "loaded") return;
    expect(s.storages).toHaveLength(1);
    // No reload was attempted: the press did not land, so there is nothing new to fetch.
    expect(get).toHaveBeenCalledTimes(1);
  });

  // Pending is keyed per storage. The user plugged in one disk and pressed one button.
  it("marks only the pressed storage as pending", async () => {
    get.mockResolvedValue({ storages: [storage({}), storage({ id: "01JC", name: "nas" })] });
    let release: (v: unknown) => void = () => {};
    post.mockReturnValueOnce(new Promise((r) => (release = r)));

    const { result } = renderHook(() => useStorages("DEV-1"));
    await waitFor(() => expect(result.current.state.status).toBe("loaded"));

    act(() => result.current.recheck("01JB"));
    await waitFor(() => expect(result.current.rechecking["01JB"]).toBe("pending"));
    expect(result.current.rechecking["01JC"]).toBeUndefined();

    await act(async () => {
      release({});
    });
  });

  // Changing device clears the per-storage state with the list. A pending marker carried across a
  // udid change would describe a press made about a different phone.
  it("clears re-check state when the device changes", async () => {
    get.mockResolvedValue({ storages: [storage({})] });
    post.mockRejectedValueOnce(new Error("nope"));

    const { result, rerender } = renderHook(({ udid }) => useStorages(udid), {
      initialProps: { udid: "DEV-1" },
    });
    await waitFor(() => expect(result.current.state.status).toBe("loaded"));
    act(() => result.current.recheck("01JB"));
    await waitFor(() => expect(result.current.rechecking["01JB"]).toBe("failed"));

    rerender({ udid: "DEV-2" });
    await waitFor(() => expect(result.current.rechecking["01JB"]).toBeUndefined());
  });
});
