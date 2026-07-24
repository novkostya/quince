import { describe, it, expect, beforeEach } from "vitest";
import type { Version } from "@/lib/types";
import { useVersionsStore } from "./versions";

function ver(id: string, udid: string, isLatest: boolean): Version {
  return {
    id,
    udid,
    backend: "reflink",
    zfs_snapshot: null,
    browse_root: `/backups/${udid}/latest`,
    created_at: "2026-07-20T00:00:00Z",
    job_id: "J",
    kind: "full",
    encrypted: true,
    is_latest: isLatest,
    structure_verified_at: null,
    content_verified_at: null,
    logical_bytes: 1,
    physical_bytes: 1,
    missing: false,
  };
}

describe("versions store — single is_latest invariant (#7)", () => {
  beforeEach(() => useVersionsStore.setState({ byId: {}, order: [] }));

  it("demotes the previous latest of the same device when a new latest arrives", () => {
    const s = useVersionsStore.getState();
    s.replaceAll([ver("V1", "DEV-1", true)]);
    // A newer committed version for the SAME device arrives over version.created (is_latest=true).
    s.upsert(ver("V2", "DEV-1", true));

    const st = useVersionsStore.getState();
    expect(st.byId["V2"].is_latest).toBe(true);
    // Exactly one latest per device — the old one is demoted immediately, no second badge until reload.
    expect(st.byId["V1"].is_latest).toBe(false);
  });

  it("does not touch another device's latest", () => {
    const s = useVersionsStore.getState();
    s.replaceAll([ver("A1", "DEV-A", true), ver("B1", "DEV-B", true)]);
    s.upsert(ver("A2", "DEV-A", true));

    const st = useVersionsStore.getState();
    expect(st.byId["A2"].is_latest).toBe(true);
    expect(st.byId["A1"].is_latest).toBe(false);
    expect(st.byId["B1"].is_latest).toBe(true); // untouched
  });
});
