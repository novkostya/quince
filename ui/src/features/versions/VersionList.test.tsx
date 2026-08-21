import type * as React from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render as rtlRender, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { Device, Version } from "@/lib/types";
import { VersionList } from "./VersionList";
import { useVersionsStore } from "@/stores/versions";
import { useDevicesStore } from "@/stores/devices";

const del = vi.fn();
vi.mock("@/lib/api", () => ({ api: { del: (p: string) => del(p) } }));

// EVERY CASE RENDERS INSIDE A ROUTER, because the row's chevron became a `<Link>` at qn.8 slice 7
// (quince#270) and react-router throws outside one. A local wrapper rather than a per-case
// `<MemoryRouter>`: the cases below are about the ROW, and repeating the harness in each of them
// would put the thing under test one indent further from the thing being asserted.
function render(node: React.ReactNode) {
  return rtlRender(<MemoryRouter>{node}</MemoryRouter>);
}

function ver(over: Partial<Version> = {}): Version {
  return {
    id: "V1",
    udid: "DEV-1",
    backend: "reflink",
    zfs_snapshot: null,
    browse_root: "/backups/DEV-1/latest",
    created_at: "2026-07-20T00:00:00Z",
    job_id: "J1",
    kind: "incremental",
    encrypted: true,
    is_latest: true,
    structure_verified_at: "2026-07-20T00:00:00Z",
    content_verified_at: null,
    logical_bytes: 42_500_000_000,
    missing: false,
    storage_id: null,
    ...over,
  };
}

describe("VersionList", () => {
  beforeEach(() => {
    del.mockReset().mockResolvedValue({});
    useVersionsStore.setState({ byId: {}, order: [] });
    useDevicesStore.setState({ byUdid: {}, order: [] });
  });

  it("renders a live version with ONE size and no Unlock, and does NOT show the kind label (ck)", () => {
    render(<VersionList versions={[ver()]} />);
    // 42.5 GB, unqualified. The row carried "N logical · N on disk" until quince#442, where both
    // figures turned out to be the same walk — so the assertion is not just that a size appears
    // but that the words framing it as one of two measurements are gone.
    expect(screen.getByText("42.5 GB")).toBeTruthy();
    expect(screen.queryByText(/logical/i)).toBeNull();
    expect(screen.queryByText(/on disk/i)).toBeNull();
    // No confusing "Unlock" button (it made no sense for unencrypted versions) — a quiet chevron now.
    expect(screen.queryByRole("button", { name: /unlock/i })).toBeNull();
    expect(screen.queryByText(/unlock/i)).toBeNull();
    // "incremental"/"full" imports a false fragile-chain mental model — it must not appear (ck).
    expect(screen.queryByText(/incremental/i)).toBeNull();
    expect(screen.queryByText(/missing/i)).toBeNull();
  });

  // quince#1047. The row makes NO verification claim. "structure verified" was tautological — a tree
  // that fails structural verify never commits, so it appeared on every job-created row and said
  // nothing — and "decryption verified" is set by nothing in the engine. `ver()` sets
  // structure_verified_at, so this fixture is exactly the case that used to render the label.
  it("makes no verification claim, on a version that IS structurally verified", () => {
    render(<VersionList versions={[ver({ structure_verified_at: "2026-07-18T08:00:00Z" })]} />);
    expect(screen.getByText("42.5 GB")).toBeTruthy();
    expect(screen.queryByText(/verified/i)).toBeNull();
    expect(screen.queryByText(/unverified/i)).toBeNull();
  });

  // ...and it does not come back for a version carrying content_verified_at either. That field is
  // reachable only from demo fixtures today; when qn.8 makes it real the label returns deliberately,
  // through a change that has to delete this test rather than pass it by accident.
  it("makes no verification claim even when content_verified_at is set", () => {
    render(<VersionList versions={[ver({ content_verified_at: "2026-07-18T08:00:00Z" })]} />);
    expect(screen.queryByText(/verified/i)).toBeNull();
  });

  it("renders a missing version explicitly dead: no size, no Unlock, a Remove action (cr)", () => {
    render(<VersionList versions={[ver({ missing: true })]} />);
    expect(screen.getByText(/missing/i)).toBeTruthy();
    expect(screen.getByText(/artifact gone/i)).toBeTruthy();
    // No size claim and no Unlock on a dead version.
    expect(screen.queryByText("42.5 GB")).toBeNull();
    expect(screen.queryByRole("button", { name: /unlock/i })).toBeNull();
    expect(screen.getByRole("button", { name: /remove/i })).toBeTruthy();
  });

  it("Remove deletes the version and drops it from the store", async () => {
    useVersionsStore.getState().replaceAll([ver({ missing: true })]);
    render(<VersionList versions={[ver({ missing: true })]} />);
    fireEvent.click(screen.getByRole("button", { name: /remove/i }));
    expect(del).toHaveBeenCalledWith("/api/versions/V1");
    await waitFor(() => expect(useVersionsStore.getState().byId["V1"]).toBeUndefined());
  });

  it("labels each row with its device when showDevice is set (dashboard list, #3)", () => {
    useDevicesStore.setState({
      byUdid: { "DEV-1": { udid: "DEV-1", name: "family-iphone" } as Device },
      order: ["DEV-1"],
    });
    render(<VersionList versions={[ver()]} showDevice />);
    expect(screen.getByText("family-iphone")).toBeTruthy();
  });

  // qn.8 slice 7 step 2. The chevron was an explicit non-interactive placeholder for four rungs;
  // this is the assertion that it went somewhere, and that it went there WITHOUT reintroducing the
  // word the row is not allowed to say.
  it("opens the backup: the chevron is a link to the browse page, and still never says Unlock", () => {
    render(<VersionList versions={[ver()]} />);
    const link = screen.getByRole("link", { name: /browse this backup/i });
    expect(link.getAttribute("href")).toBe("/versions/V1/browse");
    expect(screen.queryByText(/unlock/i)).toBeNull();
  });

  // BOTH CLASSES, because D7 is the reason the control is a chevron rather than an "Unlock" button:
  // an unencrypted version has nothing to unlock and is browsable all the same. A link offered only
  // to encrypted versions would make the honest half of the vault unreachable from the product.
  it("offers the same entry point on an unencrypted version", () => {
    render(<VersionList versions={[ver({ encrypted: false })]} />);
    expect(screen.getByRole("link", { name: /browse this backup/i }).getAttribute("href")).toBe(
      "/versions/V1/browse",
    );
  });

  // A MISSING VERSION HAS NOTHING TO OPEN, and the row's own contract already says so — no size
  // claim, no browse. The dead branch returns before the link, so this pins that the two rows did
  // not converge: a link here would land on a page that can only say the files are gone.
  it("offers no entry point on a missing version", () => {
    render(<VersionList versions={[ver({ missing: true })]} />);
    expect(screen.queryByRole("link", { name: /browse this backup/i })).toBeNull();
  });
});
