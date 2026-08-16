import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { StorageCard } from "./StorageCard";
import type { Storage } from "@/lib/types";

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
    device_count: 2,
    ...over,
  };
}

// The card title links to the details page, so every render needs a router context.
function show(s: Storage, showDefault = false) {
  return render(
    <MemoryRouter>
      <StorageCard storage={s} showDefault={showDefault} />
    </MemoryRouter>,
  );
}

describe("StorageCard", () => {
  // Story 2 — free-of-total, a fill bar and counts.
  it("shows free of total, the free percentage, and both counts", () => {
    show(storage());

    expect(screen.getByTestId("storage-space")).toHaveTextContent("1.2 TB");
    expect(screen.getByTestId("storage-space")).toHaveTextContent("free of");
    expect(screen.getByTestId("storage-space")).toHaveTextContent("3.6 TB");
    // 1.2 free of 3.6 total → 2.4 used → 67%. The bar shows what is USED, filling as the disk
    // fills, which is what PBS and Windows Explorer do and what a person already reads.
    expect(screen.getByText("67%")).toBeInTheDocument();
    expect(screen.getByTestId("storage-counts")).toHaveTextContent("14 backups");
    expect(screen.getByTestId("storage-counts")).toHaveTextContent("2 devices");
  });

  // THE RULED ACCEPTANCE, pinned so it cannot be "fixed" back (gap A, 2026-08-03).
  //
  // Two storages that are two directories on one disk report identical figures and the card says
  // NOTHING about it. `filesystem_id` and a `filesystem_shared` boolean were both offered to the
  // Operator and both declined. A future session that adds "on this filesystem" is undoing a
  // decision, not fixing an oversight — and this test is what tells them so.
  it("never qualifies the free-space figure as the filesystem's", () => {
    show(storage());
    const space = screen.getByTestId("storage-space");
    expect(space.textContent).toMatch(/^1\.2 TB free of 3\.6 TB$/);
    expect(space.textContent).not.toMatch(/filesystem|shared|disk/i);
  });

  // Story 4 — listed, says why, DATES its counts, and claims no size.
  it("an unreachable storage states why, dates its counts, and shows no size", () => {
    show(
      storage({
        name: "shuttle",
        path: "/mnt/shuttle",
        backend: "unknown",
        default: false,
        reachable: false,
        unreachable_code: "missing_medium",
        unreachable_reason: "the path is readable but carries no quince storage marker",
        filesystem_free_bytes: null,
        filesystem_total_bytes: null,
        backup_count: 3,
        device_count: 1,
      }),
      true,
    );

    expect(screen.getByTestId("storage-unreachable-reason")).toHaveTextContent(
      "carries no quince storage marker",
    );
    // Counts survive: they are DB rows and the DB is reachable regardless of the disk. They are
    // NOT dated, because they are not stale -- quince#588.
    expect(screen.getByTestId("storage-counts")).toHaveTextContent("3 backups");
    // NO size claim at all. Null capacity must not become "0 B", which reads as a full disk.
    expect(screen.queryByTestId("storage-space")).not.toBeInTheDocument();
    expect(screen.queryByText(/0 B/)).not.toBeInTheDocument();
  });

  // Story 3 — a degraded backend is a degraded mode and must be surfaced (CLAUDE.md).
  it("shows a caution pill for the copy backend and nothing for the others", () => {
    const { unmount } = show(storage({ backend: "copy" }));
    expect(screen.getByText(/copy backend/)).toBeInTheDocument();
    unmount();

    // Backend is otherwise NOT a glance fact — it lives on the details page.
    for (const b of ["zfs", "reflink", "hardlink"] as const) {
      const r = show(storage({ backend: b }));
      expect(screen.queryByText(/copy backend/)).not.toBeInTheDocument();
      expect(screen.queryByText(b)).not.toBeInTheDocument();
      r.unmount();
    }
  });

  it("labels the default only when there is more than one storage", () => {
    const { unmount } = show(storage());
    expect(screen.queryByText("Default")).not.toBeInTheDocument();
    unmount();

    show(storage(), true);
    expect(screen.getByText("Default")).toBeInTheDocument();
  });

  // The name DEFAULTS to the path (quince#504), so a single-storage install would otherwise print
  // `/backups` twice.
  it("does not repeat the path when the name defaults to it", () => {
    show(storage({ name: "/backups" }));
    expect(screen.getAllByText("/backups")).toHaveLength(1);
  });

  // THE STAGING REGRESSION, pinned at both ends. An EMPTY storage rendered a COMPLETELY FULL bar at
  // 100% — the most alarming thing a capacity gauge can show, for the safest possible state —
  // because the bar filled with the FREE fraction. Both cases are asserted, because either one alone
  // still passes under the inverted implementation.
  it("fills with USED, so an empty disk reads 0%", () => {
    const r = show(
      storage({ filesystem_free_bytes: 431_400_000_000, filesystem_total_bytes: 431_400_000_000 }),
    );
    expect(screen.getByText("0%")).toBeInTheDocument();
    r.unmount();
  });

  it("reads 100% only when the disk really is full", () => {
    show(storage({ filesystem_free_bytes: 0, filesystem_total_bytes: 431_400_000_000 }));
    expect(screen.getByText("100%")).toBeInTheDocument();
  });

  // Reachable but unmeasurable: the daemon leaves capacity null and warns. The card must not
  // render a bar at 0%, which reads as an empty disk rather than as no measurement.
  it("hides the bar when a reachable storage has no capacity figures", () => {
    show(storage({ filesystem_free_bytes: null, filesystem_total_bytes: null }));
    expect(screen.queryByTestId("storage-space")).not.toBeInTheDocument();
    expect(screen.getByText("free space unavailable")).toBeInTheDocument();
  });
});

// quince#1042 REGRESSION GUARD — the same structural defect quince#1033 fixed on the device cards
// and did not apply to their stated mirror. The card is a grid item, and a grid item defaults to
// `min-width: auto`, so it will not shrink below the intrinsic width of its widest line; below `sm:`
// the implicit column then sizes to content and one over-wide card widens every card beside it.
//
// jsdom HAS NO LAYOUT, so nothing here can prove the page stopped scrolling — `story12` and `story5`
// at real phone viewports are what do that, and the demo fixture now carries a path long enough for
// them to see it (measured: 320px overflowed by 139px with this class removed). What this asserts is
// the CLASS, which is quince#631's convention for its stated reason: a bare `min-w-0` with no test
// reads as decoration and gets tidied away by a later pass. That is not hypothetical here — it is how
// the mirror got broken in the first place.
//
// TWO GUARDS FOR ONE PROPERTY, DELIBERATELY. The e2e pair observes the real defect and needs a
// browser, a built image and a fixture that stays long; this one is cheap, runs on every
// `make gates-ui`, and survives a fixture someone shortens. Neither subsumes the other.
describe("StorageCard grid containment", () => {
  // The demo fixture's own path (core/internal/demo/provider.go), so a reader can see the length the
  // e2e gates actually run against instead of inferring it from a shorter stand-in.
  const longPath = "/mnt/usb/external-8tb-offsite-rotation/quince-backups-and-archives-2026-q3";

  it("can shrink below its content, so one long path cannot widen the row", () => {
    const { container } = show(storage({ path: longPath }));
    const card = container.querySelector<HTMLElement>('[data-testid="storage-card"]');
    expect(card?.className.split(/\s+/)).toContain("min-w-0");
  });

  // The other half of the chain, one level in. Both are required and neither is sufficient alone —
  // and this half was already right, which is exactly why the outer one read as unnecessary.
  it("keeps the name column able to shrink too", () => {
    const { container } = show(storage({ path: longPath }));
    const path = container.querySelector<HTMLElement>(".truncate.text-xs");
    expect(path?.textContent).toBe(longPath);
    expect(path?.parentElement?.className.split(/\s+/)).toContain("min-w-0");
  });

  // The unreachable variant appends to the same className string, so the class list is BUILT
  // differently on that branch. Asserted separately because the reachable case passing says nothing
  // about a template literal one conditional away.
  it("keeps min-w-0 on an unreachable card, where the class list is built differently", () => {
    const { container } = show(storage({ reachable: false }));
    const card = container.querySelector<HTMLElement>('[data-testid="storage-card"]');
    expect(card?.className.split(/\s+/)).toContain("min-w-0");
    expect(card?.className.split(/\s+/)).toContain("border-dashed");
  });
});
