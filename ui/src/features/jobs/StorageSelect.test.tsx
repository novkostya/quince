import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { StorageSelect, StorageNotices } from "./StorageSelect";
import type { Storage } from "@/lib/types";

import type { Storages, StoragesState } from "./useStorages";

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

// sub() wraps a bare state in the hook's return shape. These components still take the whole
// subscription even though neither uses `recheck` any more: the prop IS the hook's shape, and the
// re-check half moved to `StorageProblem` on the storage page, taking its tests with it
// (quince#627).
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

  // THE COST BEFORE IT IS PAID (story 8), attached to the option that carries it.
  //
  // The daemon's-own-sentence assertions that used to sit here moved to `StorageProblem`
  // (quince#627): the diagnosis is a storage fact and now renders on the storage's page.
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

// WHAT THIS PINS, AND WHAT IT CANNOT: quince#616.
//
// iOS Safari zooms the page when a focused control computes below 16px. This select carried
// `text-xs` = 12px, and WebKit's target scale is `16 / fontSize`, so tapping it zoomed the page to
// 1.33x — worse than the 14px full-width fields, not better, which is the correction the ruling
// made to the original report.
//
// THESE ASSERTIONS CANNOT PROVE SAFARI DOES NOT ZOOM. Only a device can, and that check is owed to
// the Operator. What they catch is the likely regression: someone restoring a plain `text-xs` with
// no iPhone in the room and every gate green.
describe("StorageSelect is 16px on mobile", () => {
  function renderBoth() {
    return render(
      <StorageSelect
        storages={sub({ status: "loaded", storages: [storage({}), shuttle] })}
        value=""
        onChange={() => {}}
      />,
    );
  }

  it("steps the select 16px -> 12px at the sm breakpoint", () => {
    renderBoth();
    const cls = screen.getByTestId("storage-select").className;
    expect(cls).toContain("text-base");
    expect(cls).toContain("sm:text-xs");
    expect(cls.split(/\s+/)).not.toContain("text-xs");
  });

  // THE LABEL STEPS WITH THE CONTROL, and this is the assertion that would not exist if the fix
  // were purely technical. WebKit reads only the focused control, so `sm:text-xs` on the select
  // alone stops the zoom — and leaves a 16px control inside a 12px sentence reading "to <select>".
  // Ruled on quince#616 as an outcome; this pins the outcome rather than the reasoning.
  it("steps the surrounding label with it, so the control is never larger than its sentence", () => {
    renderBoth();
    const label = screen.getByTestId("storage-select").closest("label");
    expect(label).not.toBeNull();
    const cls = label?.className ?? "";
    expect(cls).toContain("text-base");
    expect(cls).toContain("sm:text-xs");
    expect(cls.split(/\s+/)).not.toContain("text-xs");
  });
});

// THE DEVICE PAGE SAYS THE FACT AND NOT THE DIAGNOSIS (quince#627).
//
// What used to render here was the full diagnosis of EVERY unreachable storage in the
// configuration, each with its own Re-check button, on a page about one phone — and it never
// referenced the chosen storage at all. The screenshot the issue came from showed `shuttle`
// selected while the sentence diagnosed `ghost`.
//
// These pin both halves: the short line IS about the chosen storage, and the moved block is
// asserted ABSENT rather than merely untested, because "it happens not to render" and "it cannot
// render" are different guarantees and only the second survives a refactor.
describe("StorageNotices says a storage is unavailable without diagnosing it", () => {
  it("names the CHOSEN storage and links to it when that storage is unreachable", () => {
    render(
      <MemoryRouter>
        <StorageNotices
          storages={sub({ status: "loaded", storages: [storage({}), shuttle] })}
          value="01JB"
        />
      </MemoryRouter>,
    );
    const line = screen.getByTestId("storage-unavailable");
    expect(line).toHaveTextContent(/shuttle/);
    expect(line).toHaveTextContent(/unavailable/);
    expect(line.querySelector('a[href="/storage/shuttle"]')).not.toBeNull();
  });

  // THE DIAGNOSIS DOES NOT COME WITH IT. The daemon's sentence explains a disk; this page is about
  // a phone. It lives on the storage page now, one link away.
  it("does not reproduce the daemon's diagnosis", () => {
    render(
      <MemoryRouter>
        <StorageNotices
          storages={sub({ status: "loaded", storages: [storage({}), shuttle] })}
          value="01JB"
        />
      </MemoryRouter>,
    );
    expect(screen.queryByText(/carries no quince storage marker/)).toBeNull();
    expect(screen.queryByTestId("storage-recheck")).toBeNull();
    expect(screen.queryByTestId("storage-unreachable")).toBeNull();
  });

  // AND NOT A WORD ABOUT A STORAGE NOBODY CHOSE. This is the `ghost` case exactly: a second
  // unreachable storage, not selected, must produce no line at all.
  it("says nothing about an unreachable storage that is not the chosen one", () => {
    const ghost = storage({
      id: "01JC",
      name: "ghost",
      default: false,
      reachable: false,
      unreachable_code: "path_unreachable",
      unreachable_reason: "the path could not be read",
    });
    render(
      <MemoryRouter>
        <StorageNotices
          storages={sub({ status: "loaded", storages: [storage({}), ghost] })}
          value="01JA"
        />
      </MemoryRouter>,
    );
    // The chosen storage is reachable, so there is nothing to say — and `ghost` is not this
    // backup's business.
    expect(screen.queryByTestId("storage-unavailable")).toBeNull();
    expect(screen.queryByText(/ghost/)).toBeNull();
  });
});

// AN UNTOUCHED PAGE MUST NOT SELECT THE NEVER-CREATED STORAGE — quince#647.
//
// `value === ""` means the user has chosen nothing. `""` is ALSO the real id of a storage quince has
// never reached (quince#582, deliberately: "never created" is why it cannot be a destination). So
// `storages.find((s) => s.id === value)` matched that storage on an untouched page, the default
// fallback never ran, and the control opened pointed at the one storage that can never accept a
// backup — reported from staging, with the default sitting right there, reachable and ignored.
//
// Not "the first unreachable one" and not "the last declared" — specifically the never-created one,
// because the empty id is what it collides with. These fixtures reproduce that exactly.
const ghost = storage({
  id: "", // never created — this is the collision
  name: "ghost",
  default: false,
  reachable: false,
  // This read "the daemon also emits `unreachable`, which the TS enum lacks (quince#569)". Fixed —
  // the daemon translates at the boundary, so this IS what it sends for an unreadable path now. What
  // that does NOT change: the fixture is still hand-built, so it proves the component and not the
  // daemon. `live_test.go` is where a real path is driven through resolveSlot to a Slot, and the
  // absence of exactly that test is why this drift survived a whole rung.
  unreachable_code: "path_unreachable",
  unreachable_reason: "the path could not be read",
});

describe("an empty selection is not a selection", () => {
  const list = () => ({ status: "loaded" as const, storages: [storage({}), ghost] });

  it("selects the DEFAULT, not the never-created storage, when nothing was chosen", () => {
    render(<StorageSelect storages={sub(list())} value="" onChange={() => {}} />);
    expect((screen.getByTestId("storage-select") as HTMLSelectElement).value).toBe("01JA");
  });

  // THE NOTICE MUST AGREE WITH THE CONTROL. They resolve through the same helper now; before, both
  // resolved independently and both were wrong in the same way, which is why the screen was
  // internally consistent and consistently wrong.
  it("says nothing is unavailable when the default is fine and only the never-created one is not", () => {
    render(
      <MemoryRouter>
        <StorageNotices storages={sub(list())} value="" />
      </MemoryRouter>,
    );
    expect(screen.queryByTestId("storage-unavailable")).toBeNull();
  });

  // An EXPLICIT choice of a real storage still resolves — the guard must not swallow real ids.
  it("still honours an explicit choice", () => {
    const other = storage({ id: "01JB", name: "shuttle", default: false });
    render(
      <StorageSelect
        storages={sub({ status: "loaded", storages: [storage({}), other] })}
        value="01JB"
        onChange={() => {}}
      />,
    );
    expect((screen.getByTestId("storage-select") as HTMLSelectElement).value).toBe("01JB");
  });

  // And the never-created storage is still LISTED and still disabled — it is not hidden, which is
  // the rule a list a disk vanishes from would break. It just is not the default selection.
  it("still lists the never-created storage, disabled", () => {
    render(<StorageSelect storages={sub(list())} value="" onChange={() => {}} />);
    const opt = screen.getByRole("option", { name: /ghost/ }) as HTMLOptionElement;
    expect(opt.disabled).toBe(true);
  });
});

// qn.13 slice 8f-2 — THE PICKER FOR A DEVICE-SCOPED HOLDER.
//
// Since quince#1477 they receive `{id, name, reachable}` and nothing else (spec D3, second
// exception). Two consequences the admin path never exercises: there is no `backend` to print, and
// there is no `default` for `chosenStorage` to fall back to.
//
// THE SECOND ONE WAS A DEFECT AND WAS MEASURED RATHER THAN ANTICIPATED. With no default, `value` is
// `""` and the browser selects the first option regardless — so the select DISPLAYED a storage the
// request would not have named, and the backup would have gone to the admin's default instead. That
// is the screen and the request disagreeing, which is the hazard the fallback effect above exists
// for, arriving from the other direction.
//
// SYNTHETIC IDS. Nothing here is a real storage.

// The projected shape, cast because it is deliberately NOT a full `Storage` — that is the claim.
const projected = [
  { id: "st-1", name: "attic disk", reachable: true },
  { id: "st-2", name: "desk disk", reachable: false },
] as unknown as Storage[];

describe("the picker a scoped holder sees", () => {
  it("owns its empty value rather than displaying a storage it will not submit", () => {
    render(
      <StorageSelect storages={sub({ status: "loaded", storages: projected })} value="" onChange={vi.fn()} />,
    );

    const sel = screen.getByTestId("storage-select") as HTMLSelectElement;
    expect(sel.value).toBe("");
    expect(sel.options[sel.selectedIndex].text).toMatch(/chosen by the admin/i);
  });

  it("prints no backend, because it was not sent one", () => {
    render(
      <StorageSelect storages={sub({ status: "loaded", storages: projected })} value="" onChange={vi.fn()} />,
    );

    // The name is there and reads cleanly — no "(undefined)" where the admin sees "(zfs)".
    expect(screen.getByRole("option", { name: "attic disk" })).toBeInTheDocument();
    expect(screen.queryByText(/undefined/)).not.toBeInTheDocument();
  });

  it("still marks an unreachable storage, disabled rather than hidden", () => {
    render(
      <StorageSelect storages={sub({ status: "loaded", storages: projected })} value="" onChange={vi.fn()} />,
    );

    const opt = screen.getByRole("option", { name: /desk disk — not connected/ }) as HTMLOptionElement;
    expect(opt.disabled).toBe(true);
  });

  it("submits the id the holder picked", () => {
    const onChange = vi.fn();
    render(
      <StorageSelect storages={sub({ status: "loaded", storages: projected })} value="" onChange={onChange} />,
    );

    fireEvent.change(screen.getByTestId("storage-select"), { target: { value: "st-1" } });

    expect(onChange).toHaveBeenCalledWith("st-1");
  });
});

// THE CONTROL. The admin's list HAS a default, so `chosenStorage` resolves and the extra option must
// NOT appear — otherwise every admin gains a meaningless "chosen by the admin" row, and the scoped
// assertions above would pass for a picker that always shows it.
describe("the admin's picker — the control", () => {
  it("resolves its default and offers no placeholder", () => {
    render(
      <StorageSelect storages={sub({ status: "loaded", storages: [storage({}), shuttle] })} value="" onChange={vi.fn()} />,
    );

    const sel = screen.getByTestId("storage-select") as HTMLSelectElement;
    expect(sel.value).toBe("01JA");
    expect(screen.queryByText(/chosen by the admin/i)).not.toBeInTheDocument();
  });
});
