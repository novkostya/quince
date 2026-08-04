import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { StorageProblem } from "./StorageProblem";
import type { Storage } from "@/lib/types";
import type { Storages, StoragesState, RecheckState } from "@/features/jobs/useStorages";

// THESE MOVED HERE FROM `StorageSelect.test.tsx` (quince#627), because the behaviour moved.
//
// The diagnosis and `Re-check` used to render under a DEVICE's action row, so their tests lived
// beside the storage selector. Both now belong to the storage's own page, and the tests follow —
// ported rather than rewritten, so what was already pinned stays pinned: the button sits beside the
// reason, a reachable storage gets none, the press names its storage, pending disables only that
// one, and a failed press is shown rather than swallowed.
//
// The one that could NOT come with them is "keeps pending state to the storage that was pressed" —
// it rendered two unreachable storages at once, which was only possible because the old block
// listed EVERY unreachable storage in the configuration. That listing is the defect quince#627
// deleted: this component is about one storage, so a second one is not a state it has.

function storage(over: Partial<Storage> = {}): Storage {
  return {
    id: "01JA",
    name: "internal",
    path: "/backups",
    backend: "reflink",
    default: true,
    reachable: true,
    unreachable_code: null,
    unreachable_reason: null,
    will_be_full: null,
    filesystem_free_bytes: 1_200_000_000_000,
    filesystem_total_bytes: 3_600_000_000_000,
    backup_count: 14,
    device_count: 1,
    ...over,
  };
}

const shuttle = storage({
  id: "01JB",
  name: "shuttle",
  path: "/mnt/shuttle",
  backend: "unknown",
  default: false,
  reachable: false,
  unreachable_code: "missing_medium",
  unreachable_reason: "the path is readable but carries no quince storage marker",
  filesystem_free_bytes: null,
  filesystem_total_bytes: null,
});

function sub(state: StoragesState, over: Partial<Storages> = {}): Storages {
  return { state, recheck: () => {}, rechecking: {}, reload: () => {}, ...over };
}

const loaded = (): StoragesState => ({ status: "loaded", storages: [storage({}), shuttle] });

describe("StorageProblem", () => {
  it("states the daemon's own reason for an unreachable storage", () => {
    render(<StorageProblem storage={shuttle} storages={sub(loaded())} />);
    expect(screen.getByTestId("storage-detail-reason")).toHaveTextContent(
      /carries no quince storage marker/,
    );
  });

  it("offers Re-check beside that reason, in the same row", () => {
    render(<StorageProblem storage={shuttle} storages={sub(loaded())} />);
    const row = screen.getByTestId("storage-detail-reason");
    // Inside the SAME row as the reason — a button elsewhere on the page would not read as
    // "press it" for the disk the sentence is about.
    expect(row.querySelector('[data-testid="storage-recheck"]')).not.toBeNull();
  });

  // The taste call, pinned so it cannot drift back silently: a reachable storage renders NOTHING.
  // The press would be a no-op the user cannot interpret, and a control offered where there is
  // nothing to fix teaches that pressing it is how you make things happen.
  it("renders nothing at all for a reachable storage", () => {
    const { container } = render(<StorageProblem storage={storage({})} storages={sub(loaded())} />);
    expect(container).toBeEmptyDOMElement();
  });

  // Unreachable but with no reason from the daemon: there is nothing honest to print, so nothing is.
  it("renders nothing when the daemon gave no reason", () => {
    const silent = storage({ reachable: false, unreachable_reason: null });
    const { container } = render(<StorageProblem storage={silent} storages={sub(loaded())} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("asks the hook to re-check THIS storage, by name", () => {
    const recheck = vi.fn();
    render(<StorageProblem storage={shuttle} storages={sub(loaded(), { recheck })} />);
    fireEvent.click(screen.getByTestId("storage-recheck"));
    expect(recheck).toHaveBeenCalledWith("shuttle");
  });

  // A second press while the first is in flight would queue a request the user did not ask for.
  it("disables the button while its own re-check is pending", () => {
    const rechecking: Record<string, RecheckState> = { shuttle: "pending" };
    render(<StorageProblem storage={shuttle} storages={sub(loaded(), { rechecking })} />);
    const btn = screen.getByTestId("storage-recheck") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.textContent).toMatch(/Checking/);
  });

  // PENDING ON A DIFFERENT STORAGE IS NOT THIS ONE'S PENDING. The rechecking map is keyed by name
  // and shared across the page, so a component that read "is anything pending" would show
  // "Checking…" for a disk nobody touched.
  it("stays enabled while a DIFFERENT storage is being re-checked", () => {
    const rechecking: Record<string, RecheckState> = { nas: "pending" };
    render(<StorageProblem storage={shuttle} storages={sub(loaded(), { rechecking })} />);
    expect((screen.getByTestId("storage-recheck") as HTMLButtonElement).disabled).toBe(false);
  });

  // A FAILED PRESS IS SHOWN. Without this the button looks identical whether the re-check ran and
  // the disk is still out, or the request never landed — and the user keeps pressing a control that
  // is not reaching the daemon. That is the no-silent-fallbacks rule, on a button.
  it("says so when the re-check itself could not be performed", () => {
    const rechecking: Record<string, RecheckState> = { shuttle: "failed" };
    render(<StorageProblem storage={shuttle} storages={sub(loaded(), { rechecking })} />);
    expect(screen.getByTestId("storage-recheck-failed")).toHaveTextContent(/couldn’t re-check/);
    // And the storage's OWN reason is still there: "we could not ask" does not replace "the disk is
    // not there", which is still the last thing the daemon actually said.
    expect(screen.getByTestId("storage-detail-reason")).toHaveTextContent(
      /carries no quince storage marker/,
    );
  });
});
