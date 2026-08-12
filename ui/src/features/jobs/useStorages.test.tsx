import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { clearStorageCache, useStorages } from "./useStorages";
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
    // This default is an UNREACHABLE storage, so capacity is null and counts are populated — the
    // asymmetry gap A's ruling defines. A test wanting a reachable one overrides all three.
    filesystem_free_bytes: null,
    filesystem_total_bytes: null,
    backup_count: 3,
    device_count: 1,
    ...over,
  };
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  // The last-known-good map is MODULE state and outlives a `renderHook`. Without this every case
  // after the first starts `loaded` from its predecessor's data, and a fetch-count assertion fails
  // in a way that reads like a race rather than like leakage.
  clearStorageCache();
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

    act(() => result.current.recheck("shuttle"));

    await waitFor(() => {
      const s = result.current.state;
      expect(s.status).toBe("loaded");
      if (s.status !== "loaded") return;
      expect(s.storages[0].reachable).toBe(true);
      expect(s.storages[0].will_be_full).toBe(true);
    });

    expect(post).toHaveBeenCalledWith("/api/storages/shuttle/recheck");
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

    act(() => result.current.recheck("shuttle"));

    await waitFor(() => expect(result.current.rechecking["shuttle"]).toBe("failed"));
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

    act(() => result.current.recheck("shuttle"));
    await waitFor(() => expect(result.current.rechecking["shuttle"]).toBe("pending"));
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
    act(() => result.current.recheck("shuttle"));
    await waitFor(() => expect(result.current.rechecking["shuttle"]).toBe("failed"));

    rerender({ udid: "DEV-2" });
    await waitFor(() => expect(result.current.rechecking["shuttle"]).toBeUndefined());
  });
});

// THE HEIGHT OF THE PAGE IS PART OF THE SCROLL POSITION — quince#838 step 4.
//
// Home hides its Storage section while this hook reads `loading`, so a remount that starts there
// renders a SHORTER page. On a Back traversal the browser has already restored an offset and clamps
// it to what is scrollable, so a shorter page lands high and the section arriving afterwards can no
// longer move it. These two cases state the fix and its bound.
describe("useStorages remount", () => {
  it("a remount for a device already seen starts loaded, never loading", async () => {
    get.mockResolvedValue({ storages: [storage({ name: "shuttle" })] });

    const first = renderHook(() => useStorages("DEV-1"));
    await waitFor(() => expect(first.result.current.state.status).toBe("loaded"));
    first.unmount();

    // THE CLAIM IS ABOUT THE FIRST RENDER, so it is read synchronously rather than through
    // `waitFor` — which would pass just as happily one tick later on the build without the cache.
    const second = renderHook(() => useStorages("DEV-1"));
    const initial = second.result.current.state;
    expect(initial.status).toBe("loaded");
    if (initial.status !== "loaded") return;
    expect(initial.storages[0].name).toBe("shuttle");

    // It still revalidates: the cache removes the gap, it does not replace the request.
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
  });

  it("a revalidation that fails still reports failed rather than leaving stale rows", async () => {
    get.mockResolvedValueOnce({ storages: [storage({})] });

    const first = renderHook(() => useStorages("DEV-1"));
    await waitFor(() => expect(first.result.current.state.status).toBe("loaded"));
    first.unmount();

    // THE BOUND ON THE CACHE, and the reason this is not a silent fallback. `failed` is a state the
    // caller renders — "we could not load your storages, so this goes to the default" — and showing
    // the last good answer over a fetch that did not work would be exactly the two-states-that-
    // render-the-same defect the three-state contract exists to refuse.
    get.mockRejectedValueOnce(new Error("offline"));
    const second = renderHook(() => useStorages("DEV-1"));
    expect(second.result.current.state.status).toBe("loaded"); // seeded
    await waitFor(() => expect(second.result.current.state.status).toBe("failed"));
  });

  it("a device never seen still starts loading", async () => {
    get.mockResolvedValue({ storages: [storage({})] });

    const first = renderHook(() => useStorages("DEV-1"));
    await waitFor(() => expect(first.result.current.state.status).toBe("loaded"));
    first.unmount();

    // Keyed per udid, because `will_be_full` is a fact about a (device, storage) PAIR. Seeding
    // DEV-2 from DEV-1's answer would put another phone's transfer cost on this page.
    const other = renderHook(() => useStorages("DEV-2"));
    expect(other.result.current.state.status).toBe("loading");
  });
});

// A FINISHED JOB INVALIDATES THIS LIST, and the hook cannot see jobs — so `reload` exists for the
// page to call. Without it the UI keeps advertising a cost that has been paid, which is the one
// defect in this family that makes the UI say something FALSE rather than say too little.
describe("useStorages reload", () => {
  it("refetches the device-scoped list on demand", async () => {
    get
      .mockResolvedValueOnce({ storages: [storage({ reachable: true, will_be_full: true })] })
      .mockResolvedValueOnce({ storages: [storage({ reachable: true, will_be_full: false })] });

    const { result } = renderHook(() => useStorages("DEV-1"));
    await waitFor(() => {
      const s = result.current.state;
      expect(s.status === "loaded" && s.storages[0].will_be_full).toBe(true);
    });

    act(() => result.current.reload());

    await waitFor(() => {
      const s = result.current.state;
      expect(s.status === "loaded" && s.storages[0].will_be_full).toBe(false);
    });
    expect(get).toHaveBeenCalledTimes(2);
    expect(get).toHaveBeenLastCalledWith("/api/storages?udid=DEV-1");
  });
});
