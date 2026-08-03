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
    counts_as_of: "2026-08-02T18:20:00Z",
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
    // 1.2 free of 3.6 total → 33%. The bar shows what is LEFT (battery-style), matching the spec's
    // mockup and the "1.2 TB free of 3.6 TB" line directly above it.
    expect(screen.getByText("33%")).toBeInTheDocument();
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
    // Counts survive — they are the DB's answer and the DB is reachable — but they are DATED.
    expect(screen.getByTestId("storage-counts")).toHaveTextContent("3 backups");
    expect(screen.getByTestId("storage-counts-as-of")).toBeInTheDocument();
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

  // Reachable but unmeasurable: the daemon leaves capacity null and warns. The card must not
  // render a bar at 0%, which reads as an empty disk rather than as no measurement.
  it("hides the bar when a reachable storage has no capacity figures", () => {
    show(storage({ filesystem_free_bytes: null, filesystem_total_bytes: null }));
    expect(screen.queryByTestId("storage-space")).not.toBeInTheDocument();
    expect(screen.getByText("free space unavailable")).toBeInTheDocument();
  });
});
