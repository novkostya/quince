import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { Device, Version } from "@/lib/types";
import { VersionList } from "./VersionList";
import { useVersionsStore } from "@/stores/versions";
import { useDevicesStore } from "@/stores/devices";

const del = vi.fn();
vi.mock("@/lib/api", () => ({ api: { del: (p: string) => del(p) } }));

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
    physical_bytes: 260_000_000,
    missing: false,
    ...over,
  };
}

describe("VersionList", () => {
  beforeEach(() => {
    del.mockReset().mockResolvedValue({});
    useVersionsStore.setState({ byId: {}, order: [] });
    useDevicesStore.setState({ byUdid: {}, order: [] });
  });

  it("renders a live version with sizes and Unlock, and does NOT show the kind label (ck)", () => {
    render(<VersionList versions={[ver()]} />);
    expect(screen.getByText(/logical/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /unlock/i })).toBeTruthy();
    // "incremental"/"full" imports a false fragile-chain mental model — it must not appear (ck).
    expect(screen.queryByText(/incremental/i)).toBeNull();
    expect(screen.queryByText(/missing/i)).toBeNull();
  });

  it("renders a missing version explicitly dead: no size, no Unlock, a Remove action (cr)", () => {
    render(<VersionList versions={[ver({ missing: true })]} />);
    expect(screen.getByText(/missing/i)).toBeTruthy();
    expect(screen.getByText(/artifact gone/i)).toBeTruthy();
    // No size claim and no Unlock on a dead version.
    expect(screen.queryByText(/logical/i)).toBeNull();
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
});
