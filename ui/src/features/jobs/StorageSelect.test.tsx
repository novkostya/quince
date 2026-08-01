import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { StorageSelect } from "./StorageSelect";
import type { Storage } from "@/lib/types";

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
});

describe("StorageSelect", () => {
  // With one storage there is no decision. A select with a single option teaches the user there is
  // a choice to make when there is not.
  it("renders nothing when there is only one storage", () => {
    const { container } = render(
      <StorageSelect state={{ status: "loaded", storages: [storage({})] }} value="" onChange={() => {}} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  // Listed and DISABLED, never hidden. The user plugged that disk in once; a list it silently
  // vanishes from is a list they cannot trust — and serving-while-unreachable exists precisely so
  // the UI can say which disk is missing.
  it("lists an unreachable storage, disabled, rather than hiding it", () => {
    render(<StorageSelect state={{ status: "loaded", storages: [storage({}), shuttle] }} value="" onChange={() => {}} />);
    const opt = screen.getByRole("option", { name: /shuttle/ }) as HTMLOptionElement;
    expect(opt).toBeInTheDocument();
    expect(opt.disabled).toBe(true);
    expect(opt.textContent).toMatch(/not connected/);
  });

  // The daemon's own sentence, shown rather than replaced with client copy: it names which path and
  // which marker, which no client-side string could.
  it("shows the daemon's reason for the chosen unreachable storage", () => {
    render(<StorageSelect state={{ status: "loaded", storages: [storage({}), shuttle] }} value="01JB" onChange={() => {}} />);
    expect(screen.getByTestId("storage-unreachable")).toHaveTextContent(
      /carries no quince storage marker/,
    );
  });

  // THE COST BEFORE IT IS PAID (story 8), attached to the option that carries it.
  it("warns that a first backup to this storage transfers everything", () => {
    const fresh = storage({ id: "01JB", name: "shuttle", default: false, will_be_full: true });
    render(<StorageSelect state={{ status: "loaded", storages: [storage({}), fresh] }} value="01JB" onChange={() => {}} />);
    expect(screen.getByTestId("storage-will-be-full")).toHaveTextContent(
      /transfers everything, not just what changed/,
    );
  });

  // And NOT on a storage that already holds a backup for this device — the warning is a fact about
  // the pair, so a constant one would train the user to ignore it.
  it("does not warn when this storage already holds a backup for the device", () => {
    const seen = storage({ id: "01JB", name: "shuttle", default: false, will_be_full: false });
    render(<StorageSelect state={{ status: "loaded", storages: [storage({}), seen] }} value="01JB" onChange={() => {}} />);
    expect(screen.queryByTestId("storage-will-be-full")).toBeNull();
  });

  it("reports the chosen storage's id", () => {
    const onChange = vi.fn();
    const other = storage({ id: "01JB", name: "shuttle", default: false });
    render(<StorageSelect state={{ status: "loaded", storages: [storage({}), other] }} value="" onChange={onChange} />);
    fireEvent.change(screen.getByTestId("storage-select"), { target: { value: "01JB" } });
    expect(onChange).toHaveBeenCalledWith("01JB");
  });
});

describe("StorageSelect degradation", () => {
  // THE BLOCKING FINDING (quince#452 review): a failed load used to render identically to "there is
  // only one storage" — the control simply gone. A user with two disks would press the button and
  // have the backup go to the default with nothing saying so.
  it("says the load failed rather than rendering as no-choice", () => {
    render(<StorageSelect state={{ status: "failed" }} value="" onChange={() => {}} />);
    expect(screen.getByTestId("storages-failed")).toHaveTextContent(/go to the default/);
  });

  // Distinct from a genuine single storage, which correctly renders nothing: the two states must
  // not be confusable in either direction.
  it("renders nothing for a genuine single storage", () => {
    const { container } = render(
      <StorageSelect
        state={{ status: "loaded", storages: [storage({})] }}
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
      <StorageSelect state={{ status: "loading" }} value="" onChange={() => {}} />,
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
        state={{ status: "loaded", storages: [storage({}), other] }}
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
        state={{ status: "loaded", storages: [storage({}), other] }}
        value=""
        onChange={onChange}
      />,
    );
    expect(onChange).not.toHaveBeenCalled();
  });
});
