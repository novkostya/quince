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

  // THE ASSERTION INVERTS AS OF `qn.6g` (quince#577), and both halves matter.
  //
  // It read *"says the disk is still being served until a restart"*, which gap B required while the
  // card really did linger. With the storage applier wired it does not linger, so the sentence
  // describes a state that no longer occurs — and a test asserting it would hold the copy to a
  // fixed lie. What is asserted instead is the ABSENCE of the promise AND the SURVIVAL of the
  // ruled half, because deleting a sentence is the edit most likely to take a neighbour with it.
  it("promises no restart, and still says nothing on the disk was deleted", async () => {
    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByTestId("storage-forget-confirm"));

    const done = await screen.findByTestId("storage-forgotten");
    expect(done.textContent).not.toMatch(/restart/i);
    expect(done.textContent).toMatch(/[Nn]othing on the disk was deleted/);
  });

  // THE CONFIRM DIALOG TOO, and separately, because it carried its own copy of the promise
  // (*"quince will keep serving this disk until it restarts"*). Two sentences in two places, and a
  // change that removed one and left the other would tell a user about a restart at the moment of
  // deciding but not at the moment of acting — the worse half to miss.
  it("the confirm dialog promises no restart either", () => {
    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));

    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).not.toMatch(/restart/i);
    // And the ruled sentence is still the one shown before pressing.
    expect(dialog.textContent).toMatch(/The backups on the disk are not deleted/);
  });

  // THE NEW REFUSAL NEEDS NO NEW CODE, which is what this test is really pinning.
  //
  // `qn.6g` PR 4 added a `422` for a backup running on the storage (Operator ruling 2026-08-06).
  // `firstError` already renders the server's sentence verbatim, so the job id and both remedies
  // reach the user through the path built for the default-storage refusal. A client that reworded
  // refusals would have needed a change here; this one does not, and that is the payoff.
  it("shows the server's own refusal when a backup is running on the storage", async () => {
    forget.mockRejectedValue(
      new APIError(422, "unprocessable", "unprocessable", {
        errors: [
          {
            path: "storage",
            message:
              'a backup is running on "shuttle" (job 01JOBRUNNING) — wait for it to finish, or ' +
              "cancel it, and then forget the storage. Forgetting it now would leave that backup " +
              "unable to finish writing and unable to clean up.",
          },
        ],
      }),
    );

    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByTestId("storage-forget-confirm"));

    const err = await screen.findByTestId("storage-forget-error");
    // The JOB ID is the whole remedy: "something is running" leaves a user with nothing to find.
    expect(err.textContent).toMatch(/01JOBRUNNING/);
    expect(err.textContent).toMatch(/wait for it to finish, or cancel it/);
    expect(screen.queryByTestId("storage-forgotten")).toBeNull();
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

  // THIS TEST IS DELETED RATHER THAN INVERTED, and the distinction is worth the paragraph.
  //
  // It read *"never claims the storage is already gone from the running process"*, forbidding
  // `immediately|already stopped|no longer in use` because quince#577 was a separate rung. That
  // rung has landed and the storage IS gone from the running process, so the prohibition is void.
  //
  // The tempting move is to invert it — assert the copy now DOES say "immediately". That would be
  // wrong: the copy deliberately says nothing about the running process at all. It says the
  // backups survive, and lets the list speak for itself. **An assertion that a specific reassuring
  // word appears is how copy gets frozen into a shape nobody chose**, and the claim this rung makes
  // is about behaviour, which G1–G7 prove in Go.
  //
  // What survives from it is the `not.toMatch(/restart/i)` above, which is the same guard aimed at
  // the promise that is actually false now.
  it("still confirms by NAME after the copy change", async () => {
    renderIt();
    fireEvent.click(screen.getByTestId("storage-forget"));
    fireEvent.click(screen.getByTestId("storage-forget-confirm"));

    const done = await screen.findByTestId("storage-forgotten");
    expect(done.textContent).toMatch(/shuttle is no longer declared/);
  });
});
