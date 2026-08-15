import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { MakeDefaultStorage } from "./MakeDefaultStorage";
import { APIError } from "@/lib/api";
import type { Storage } from "@/lib/types";

// quince#722, client half. The SERVER half — that the flag moves, that file order is untouched, and
// that the forget refusal's remedy now works end to end — is the Go gates in `internal/httpapi` and
// `internal/config`. What is pinned here is what the user is told and what they have to press,
// which is the part a Go test cannot see.

const makeDefault = vi.fn();
vi.mock("@/lib/config", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/config")>();
  return { ...actual, makeStorageDefault: (name: string) => makeDefault(name) as Promise<unknown> };
});

function storage(over: Partial<Storage> = {}): Storage {
  return {
    id: "01JSTORAGE-B",
    name: "shuttle",
    path: "/mnt/shuttle",
    backend: "reflink",
    default: false,
    reachable: true,
    unreachable_code: null,
    unreachable_reason: null,
    will_be_full: null,
    filesystem_free_bytes: 1_200_000_000_000,
    filesystem_total_bytes: 3_600_000_000_000,
    backup_count: 3,
    device_count: 1,
    ...over,
  } as Storage;
}

function renderIt(s: Storage = storage(), onDone: () => void = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <MakeDefaultStorage storage={s} onDone={onDone} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("MakeDefaultStorage", () => {
  beforeEach(() => {
    makeDefault.mockReset();
    makeDefault.mockResolvedValue({ config: {}, warnings: [], source: { path: "", mtime: "" } });
  });

  // ONE PRESS, NO CONFIRM — and the asymmetry with Forget beside it on the page is the claim.
  // Forget asks first because a user reasonably fears it deletes their backups; this moves nothing
  // and is undone by pressing the same button on the other storage. A confirm on a reversible,
  // non-destructive change is how people learn to click through confirms.
  it("acts on one press, with no confirmation step", async () => {
    renderIt();

    fireEvent.click(screen.getByTestId("storage-make-default"));

    await waitFor(() => {
      expect(makeDefault).toHaveBeenCalledWith("shuttle");
    });
  });

  // THE PAGE OWNS THE REFRESH, because the page owns the hook. `useStorages` is a useState +
  // useEffect hook with no shared cache, so invalidating the config query cannot reach it — and
  // unlike the forget, this control does not navigate away afterwards, so no remount will refetch
  // for it. Without this call the `Default` badge two rows up stays wrong.
  it("tells the page to reload once the server has accepted", async () => {
    const onDone = vi.fn();
    renderIt(storage(), onDone);

    fireEvent.click(screen.getByTestId("storage-make-default"));

    await waitFor(() => {
      expect(onDone).toHaveBeenCalled();
    });
  });

  // THE SERVER'S OWN SENTENCE IS WHAT IS SHOWN. A refusal from this route names the storage, and
  // re-wording it here would drop whatever the server knew and this component does not — the same
  // rule ForgetStorage follows, and the reason neither re-words a 422.
  it("renders the server's refusal verbatim", async () => {
    makeDefault.mockRejectedValue(
      new APIError(422, "unprocessable", "could not make this the default storage", {
        errors: [{ path: "storage", message: "storage “shuttle” is no longer declared" }],
      }),
    );
    renderIt();

    fireEvent.click(screen.getByTestId("storage-make-default"));

    await waitFor(() => {
      expect(screen.getByTestId("storage-make-default-error").textContent).toContain(
        "no longer declared",
      );
    });
  });

  // A FAILURE LEAVES THE BUTTON PRESSABLE. The action did not happen, so the control that performs
  // it must still be there — a disabled button beside an error is a dead end, which is the shape
  // this whole issue is about.
  it("stays pressable after a failure", async () => {
    makeDefault.mockRejectedValue(new Error("network"));
    renderIt();

    fireEvent.click(screen.getByTestId("storage-make-default"));

    await waitFor(() => {
      expect(screen.getByTestId("storage-make-default-error")).toBeTruthy();
    });
    expect(screen.getByTestId<HTMLButtonElement>("storage-make-default").disabled).toBe(false);
  });
});
