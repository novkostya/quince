import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { ForgetStorage } from "./ForgetStorage";
import { APIError } from "@/lib/api";
import type { Storage } from "@/lib/types";

// qn.6d stories 8 and 9, client half. The SERVER half — that the entry actually leaves config.yml
// and that the default is refused — is PR 6a's Go gates (G5, G5b, G6). What is pinned here is what
// the user is told, which is the part a Go test cannot see.

const forget = vi.fn();
vi.mock("@/lib/config", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/config")>();
  return { ...actual, forgetStorage: (name: string) => forget(name) as Promise<unknown> };
});

function storage(over: Partial<Storage> = {}): Storage {
  return {
    id: "01JSTORAGE-A",
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

function renderIt(s: Storage = storage()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <ForgetStorage storage={s} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("ForgetStorage", () => {
  beforeEach(() => {
    forget.mockReset();
    forget.mockResolvedValue({ config: {}, warnings: [], source: { path: "", mtime: "" } });
  });

  // THE CONFIRM SENTENCE IS THE RULING, verbatim. Without it a user assumes the button wiped their
  // backups — which is the fear that stops them tidying a stale disk out of their config, and the
  // whole reason detach-and-forget was ruled the way it was.
  it("asks before doing anything, and says the backups are not deleted", () => {
    renderIt();
    expect(forget).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("storage-forget"));

    expect(
      screen.getByText(/Forget removes it from quince\. The backups on the disk are not deleted\./),
    ).toBeTruthy();
    // Still nothing has happened — opening the dialog is not the act.
    expect(forget).not.toHaveBeenCalled();
  });

  it("cancelling does not call the API", () => {
    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    expect(forget).not.toHaveBeenCalled();
  });

  it("confirming forgets by NAME, never by id", async () => {
    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByTestId("storage-forget-confirm"));

    await waitFor(() => expect(forget).toHaveBeenCalledWith("shuttle"));
    // The id exists on this fixture and must not be what was sent: an unreachable storage has no
    // id, and the one a user most wants to forget is the one that never came up (quince#570).
    expect(forget).not.toHaveBeenCalledWith("01JSTORAGE-A");
  });

  // THE RESTART IS SURFACED. Gap B ruled it is never silent, and this is the only thing standing
  // between a success and a card that stays on Home with no explanation.
  it("says the disk is still being served until a restart, and that nothing was deleted", async () => {
    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByTestId("storage-forget-confirm"));

    const done = await screen.findByTestId("storage-forgotten");
    expect(done.textContent).toMatch(/still serving this disk until it restarts/i);
    expect(done.textContent).toMatch(/[Nn]othing on the disk was deleted/);
  });

  // G9's client half: the refusal is the SERVER's sentence, not ours. It names the storage and the
  // remedy, and re-wording it here would drop the half that tells the user what to do.
  it("shows the server's own refusal when the storage is the default", async () => {
    forget.mockRejectedValue(
      new APIError(422, "unprocessable", "unprocessable", {
        errors: [
          {
            path: "storage",
            message:
              'storage "pool" is the default — a backup that names no storage resolves to it, so ' +
              "quince will not forget it. Make another storage the default first, then forget this one.",
          },
        ],
      }),
    );

    renderIt(storage({ name: "pool", default: true }));
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByTestId("storage-forget-confirm"));

    const err = await screen.findByTestId("storage-forget-error");
    expect(err.textContent).toMatch(/is the default/);
    expect(err.textContent).toMatch(/Make another storage the default first/);
    // And the dialog stays open on a refusal — a user who has just been told to do something first
    // should not have to reopen the thing that told them.
    expect(screen.queryByTestId("storage-forgotten")).toBeNull();
  });

  // An error with no {errors:[...]} envelope still says something the server said. Generic copy is
  // the last resort, not the first.
  it("falls back to the API message when a failure carries no field errors", async () => {
    forget.mockRejectedValue(new APIError(500, "internal", "could not write config"));

    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByTestId("storage-forget-confirm"));

    const err = await screen.findByTestId("storage-forget-error");
    expect(err.textContent).toMatch(/could not write config/);
  });

  // The copy must NOT promise live-apply — quince#577 is a separate rung and nothing here builds
  // toward it. A sentence saying the storage is gone from the running process would be false.
  it("never claims the storage is already gone from the running process", async () => {
    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByTestId("storage-forget-confirm"));

    const done = await screen.findByTestId("storage-forgotten");
    expect(done.textContent).not.toMatch(/immediately|already stopped|no longer in use/i);
  });
});
