import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { StorageSelect, StorageNotices } from "./StorageSelect";
import type { Storage } from "@/lib/types";

import type { Storages, StoragesState, RecheckState } from "./useStorages";

function storage(over: Partial<Storage>): Storage {
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
    // This default is a REACHABLE storage, so capacity is present. The unreachable case nulls both
    // — never 0, which would render as a full disk (gap A, ruled 2026-08-03).
    filesystem_free_bytes: 1_200_000_000_000,
    filesystem_total_bytes: 3_600_000_000_000,
    backup_count: 14,
    device_count: 2,
    ...over,
  };
}

// sub() wraps a bare state in the hook's return shape. The component now takes the whole
// subscription — state plus recheck — because the button lives on the row that explains the
// problem, and only the hook can reload the DEVICE-SCOPED list after a successful press.
function sub(state: StoragesState, over: Partial<Storages> = {}): Storages {
  return { state, recheck: () => {}, rechecking: {}, reload: () => {}, ...over };
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
});

describe("StorageSelect", () => {
  // With one storage there is no decision. A select with a single option teaches the user there is
  // a choice to make when there is not.
  it("renders nothing when there is only one storage", () => {
    const { container } = render(
      <StorageSelect storages={sub({ status: "loaded", storages: [storage({})] })} value="" onChange={() => {}} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  // Listed and DISABLED, never hidden. The user plugged that disk in once; a list it silently
  // vanishes from is a list they cannot trust — and serving-while-unreachable exists precisely so
  // the UI can say which disk is missing.
  it("lists an unreachable storage, disabled, rather than hiding it", () => {
    render(<StorageSelect storages={sub({ status: "loaded", storages: [storage({}), shuttle] })} value="" onChange={() => {}} />);
    const opt = screen.getByRole("option", { name: /shuttle/ }) as HTMLOptionElement;
    expect(opt).toBeInTheDocument();
    expect(opt.disabled).toBe(true);
    expect(opt.textContent).toMatch(/not connected/);
  });

  // The daemon's own sentence, shown rather than replaced with client copy: it names which path and
  // which marker, which no client-side string could.
  // WITHOUT selecting it. A disabled option cannot be chosen, so a reason that only appeared
  // on selection was unreachable code — the user saw "not connected" and could never learn
  // which path or why. Caught by G8 driving the real API; this pins it at the unit level.
  it("shows the daemon's reason for an unreachable storage without it being chosen", () => {
    // value is the DEFAULT, not the unreachable one.
    render(
      <StorageNotices
        storages={sub({ status: "loaded", storages: [storage({}), shuttle] })}
        value="01JA"
      />,
    );
    expect(screen.getByTestId("storage-unreachable")).toHaveTextContent(
      /carries no quince storage marker/,
    );
  });

  // THE COST BEFORE IT IS PAID (story 8), attached to the option that carries it.
  it("warns that a first backup to this storage transfers everything", () => {
    const fresh = storage({ id: "01JB", name: "shuttle", default: false, will_be_full: true });
    render(<StorageNotices storages={sub({ status: "loaded", storages: [storage({}), fresh] })} value="01JB" />);
    expect(screen.getByTestId("storage-will-be-full")).toHaveTextContent(
      /transfers everything, not just what changed/,
    );
  });

  // And NOT on a storage that already holds a backup for this device — the warning is a fact about
  // the pair, so a constant one would train the user to ignore it.
  it("does not warn when this storage already holds a backup for the device", () => {
    const seen = storage({ id: "01JB", name: "shuttle", default: false, will_be_full: false });
    render(<StorageNotices storages={sub({ status: "loaded", storages: [storage({}), seen] })} value="01JB" />);
    expect(screen.queryByTestId("storage-will-be-full")).toBeNull();
  });

  it("reports the chosen storage's id", () => {
    const onChange = vi.fn();
    const other = storage({ id: "01JB", name: "shuttle", default: false });
    render(<StorageSelect storages={sub({ status: "loaded", storages: [storage({}), other] })} value="" onChange={onChange} />);
    fireEvent.change(screen.getByTestId("storage-select"), { target: { value: "01JB" } });
    expect(onChange).toHaveBeenCalledWith("01JB");
  });
});

describe("StorageSelect degradation", () => {
  // THE BLOCKING FINDING (quince#452 review): a failed load used to render identically to "there is
  // only one storage" — the control simply gone. A user with two disks would press the button and
  // have the backup go to the default with nothing saying so.
  it("says the load failed rather than rendering as no-choice", () => {
    render(<StorageNotices storages={sub({ status: "failed" })} value="" />);
    expect(screen.getByTestId("storages-failed")).toHaveTextContent(/go to the default/);
  });

  // Distinct from a genuine single storage, which correctly renders nothing: the two states must
  // not be confusable in either direction.
  it("renders nothing for a genuine single storage", () => {
    const { container } = render(
      <StorageSelect
        storages={sub({ status: "loaded", storages: [storage({})] })}
        value=""
        onChange={() => {}}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  // And nothing while loading — a flash of "couldn't load" before the first response would be a lie
  // about a request still in flight.
  it("renders nothing while loading", () => {
    const { container } = render(
      <StorageSelect storages={sub({ status: "loading" })} value="" onChange={() => {}} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  // A stale selection must not display one storage and submit another. The server refuses the stale
  // id clearly, so it fails safe — but the screen and the request disagreeing is its own defect.
  it("tells the parent when a stale selection falls back to the default", () => {
    const onChange = vi.fn();
    const other = storage({ id: "01JB", name: "shuttle", default: false });
    render(
      <StorageSelect
        storages={sub({ status: "loaded", storages: [storage({}), other] })}
        value="01JGONE"
        onChange={onChange}
      />,
    );
    expect(onChange).toHaveBeenCalledWith("01JA");
  });

  // But NOT when nothing was chosen: an untouched selector must keep sending no storage_id, so the
  // server resolves the default rather than the client naming it.
  it("does not pre-fill the default when nothing was chosen", () => {
    const onChange = vi.fn();
    const other = storage({ id: "01JB", name: "shuttle", default: false });
    render(
      <StorageSelect
        storages={sub({ status: "loaded", storages: [storage({}), other] })}
        value=""
        onChange={onChange}
      />,
    );
    expect(onChange).not.toHaveBeenCalled();
  });
});

// quince#459 — "plug the disk in and press the button" (Operator ruling, quince#435) shipped its
// endpoint in quince#445 and its button nowhere. These pin the button to the row that names the
// problem, and pin the two states a press can leave behind.
describe("StorageSelect re-check", () => {
  it("offers Re-check on the unreachable row, beside its reason", () => {
    render(
      <StorageNotices
        storages={sub({ status: "loaded", storages: [storage({}), shuttle] })}
        value="01JA"
      />,
    );
    const row = screen.getByTestId("storage-unreachable");
    expect(row).toHaveTextContent(/carries no quince storage marker/);
    // Inside the SAME row as the reason — a button elsewhere on the page would not be "press it"
    // for the disk the sentence is about.
    expect(row.querySelector('[data-testid="storage-recheck"]')).not.toBeNull();
  });

  // The taste call, pinned so it cannot drift back silently: a reachable storage gets no button.
  // The press would be a no-op the user cannot interpret, and a control offered where there is
  // nothing to fix teaches that pressing it is how you make things happen.
  it("offers no Re-check when every storage is reachable", () => {
    const other = storage({ id: "01JB", name: "shuttle", default: false });
    render(
      <StorageNotices
        storages={sub({ status: "loaded", storages: [storage({}), other] })}
        value="01JA"
      />,
    );
    expect(screen.queryByTestId("storage-recheck")).toBeNull();
  });

  it("asks the hook to re-check THAT storage", () => {
    const recheck = vi.fn();
    render(
      <StorageNotices
        storages={sub({ status: "loaded", storages: [storage({}), shuttle] }, { recheck })}
        value="01JA"
      />,
    );
    fireEvent.click(screen.getByTestId("storage-recheck"));
    expect(recheck).toHaveBeenCalledWith("shuttle");
  });

  // A second press while the first is in flight would queue a request the user did not ask for.
  it("disables the button while its own re-check is pending", () => {
    const pending: Record<string, RecheckState> = { shuttle: "pending" };
    render(
      <StorageNotices
        storages={sub({ status: "loaded", storages: [storage({}), shuttle] }, { rechecking: pending })}
        value="01JA"
      />,
    );
    const btn = screen.getByTestId("storage-recheck") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.textContent).toMatch(/Checking/);
  });

  // A FAILED PRESS IS SHOWN. Without this the button looks identical whether the re-check ran and
  // the disk is still out, or the request never landed — and the user keeps pressing a control
  // that is not reaching the daemon. That is the no-silent-fallbacks rule on a button.
  it("says so when the re-check itself could not be performed", () => {
    const failed: Record<string, RecheckState> = { shuttle: "failed" };
    render(
      <StorageNotices
        storages={sub({ status: "loaded", storages: [storage({}), shuttle] }, { rechecking: failed })}
        value="01JA"
      />,
    );
    expect(screen.getByTestId("storage-recheck-failed")).toHaveTextContent(/couldn’t re-check/);
    // And the storage's OWN reason is still there: "we could not ask" does not replace "the disk
    // is not there", which is still the last thing the daemon said.
    expect(screen.getByTestId("storage-unreachable")).toHaveTextContent(
      /carries no quince storage marker/,
    );
  });

  // The button belongs to ONE row. The user plugged in one disk and pressed one button; a second
  // unreachable storage showing "Checking…" would be a claim about something nobody touched.
  it("keeps pending state to the storage that was pressed", () => {
    const third = storage({
      id: "01JC",
      name: "nas",
      default: false,
      reachable: false,
      unreachable_code: "path_unreachable",
      unreachable_reason: "the path could not be read",
    });
    const pending: Record<string, RecheckState> = { shuttle: "pending" };
    render(
      <StorageNotices
        storages={sub(
          { status: "loaded", storages: [storage({}), shuttle, third] },
          { rechecking: pending },
        )}
        value="01JA"
      />,
    );
    const buttons = screen.getAllByTestId("storage-recheck") as HTMLButtonElement[];
    expect(buttons).toHaveLength(2);
    expect(buttons.filter((b) => b.disabled)).toHaveLength(1);
    expect(buttons.filter((b) => /Checking/.test(b.textContent ?? ""))).toHaveLength(1);
  });
});

// THE THREE THINGS THE G9 RUN REPORTED, pinned. All three were found by the Operator watching a
// real transfer on the staging stand, and none of them is a layout preference — each is the UI
// saying less than it knows, or saying something untrue.
describe("StorageSelect after G9", () => {
  // 1. THE CONTROL EMITS NO PROSE. quince#325 established that a flex item is as wide as its
  // widest child, so a sentence in the action row sets the column's width and pushes the next
  // button out. StorageSelect reintroduced exactly that with the full-transfer warning; keeping
  // the control prose-free is what makes the fix structural rather than a width tweak.
  it("renders only the control — no sentence can widen the action row", () => {
    const fresh = storage({ id: "01JB", name: "shuttle", default: false, will_be_full: true });
    const { container } = render(
      <StorageSelect
        storages={sub({ status: "loaded", storages: [storage({}), fresh, shuttle] })}
        value="01JB"
        onChange={() => {}}
      />,
    );
    expect(screen.getByTestId("storage-select")).toBeInTheDocument();
    // Not one of the sentences, though this state would have produced two of them before.
    expect(container.querySelector('[data-testid="storage-will-be-full"]')).toBeNull();
    expect(container.querySelector('[data-testid="storage-unreachable"]')).toBeNull();
    expect(container.querySelector('[data-testid="storages-failed"]')).toBeNull();
  });

  // 2. THE NOTICES RENDER FOR A SINGLE STORAGE TOO. The control correctly hides when there is no
  // choice; the COST is not a choice. A user with one storage still deserves to know their next
  // backup transfers everything.
  it("states the cost even when there is only one storage and no control", () => {
    const only = storage({ will_be_full: true });
    render(<StorageNotices storages={sub({ status: "loaded", storages: [only] })} value="" />);
    expect(screen.getByTestId("storage-will-be-full")).toHaveTextContent(/transfers everything/);
  });

  // 3. AND IT MUST GO AWAY ONCE PAID. `will_be_full` false is the server saying the cost has been
  // met; rendering the warning anyway is the UI asserting something untrue, which is worse than
  // the two omissions above. On hardware this survived the transfer it described.
  it("drops the warning once the server says the cost has been paid", () => {
    const paid = storage({ will_be_full: false });
    render(<StorageNotices storages={sub({ status: "loaded", storages: [paid] })} value="" />);
    expect(screen.queryByTestId("storage-will-be-full")).toBeNull();
  });
});
